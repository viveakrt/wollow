package db

import (
	"database/sql"
	"fmt"
	"log"
)

// renameLegacyTables upgrades a database created before Mail and Money were
// merged. Pre-merge Wollow called its mailbox table "accounts"; the merged
// schema calls it "mail_accounts", because Money brought its own "accounts"
// meaning a bank account.
//
// This runs before schema.sql so the rename happens while the new name is still
// free. SQLite's ALTER TABLE ... RENAME TO also rewrites the foreign keys in
// messages and sync_state that point at it, so the message index and its
// classifications survive intact.
func renameLegacyTables(conn *sql.DB) error {
	hasOld, err := tableExists(conn, "accounts")
	if err != nil {
		return err
	}
	if !hasOld {
		return nil
	}

	hasNew, err := tableExists(conn, "mail_accounts")
	if err != nil {
		return err
	}
	if hasNew {
		// Both names present: a half-finished migration, or someone recreated
		// the old table. Renaming would destroy one of them, so refuse and let
		// a human look.
		return fmt.Errorf(
			"both 'accounts' and 'mail_accounts' exist — refusing to guess which holds your mailboxes; " +
				"back up the database and drop whichever is empty")
	}

	if _, err := conn.Exec(`ALTER TABLE accounts RENAME TO mail_accounts`); err != nil {
		return fmt.Errorf("renaming accounts to mail_accounts: %w", err)
	}

	var mailboxes int
	conn.QueryRow(`SELECT COUNT(*) FROM mail_accounts`).Scan(&mailboxes)
	log.Printf("migration: renamed legacy 'accounts' table to 'mail_accounts' (%d mailbox(es) carried over)", mailboxes)
	return nil
}

func tableExists(conn *sql.DB, name string) (bool, error) {
	var n int
	err := conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&n)
	return n > 0, err
}

// runMigrations applies idempotent, additive changes that CREATE TABLE IF
// NOT EXISTS can't express — mainly ALTER TABLE ADD COLUMN on tables that
// may already exist from an earlier version of schema.sql. Every migration
// here must be safe to run repeatedly (check-before-alter) since it runs on
// every startup, not just once.
//
// Renames and FK retargets are deliberately NOT handled here. The merge that
// created this schema started from an empty database precisely so those never
// had to run against live data.
type columnMigration struct{ table, column, definition string }

var columnMigrations = []columnMigration{
	{"settings", "base_url", "TEXT NOT NULL DEFAULT ''"},
	{"mail_accounts", "enabled", "INTEGER NOT NULL DEFAULT 1"},
	{"messages", "rfc_message_id", "TEXT NOT NULL DEFAULT ''"},
	{"message_links", "message_id", "INTEGER REFERENCES messages(id) ON DELETE SET NULL"},
	{"message_links", "investment_id", "INTEGER REFERENCES investments(id) ON DELETE SET NULL"},
	{"transactions", "linked_txn_id", "INTEGER REFERENCES transactions(id) ON DELETE SET NULL"},
	{"transactions", "transfer_kind", "TEXT NOT NULL DEFAULT ''"},
	{"transactions", "counterparty", "TEXT NOT NULL DEFAULT ''"},
	{"finance_accounts", "credit_limit", "REAL NOT NULL DEFAULT 0"},
	{"finance_accounts", "source", "TEXT NOT NULL DEFAULT 'manual'"},
	{"finance_accounts", "include_in_networth", "INTEGER NOT NULL DEFAULT 1"},
	{"investments", "last_price", "REAL"},
	{"investments", "last_price_at", "TEXT NOT NULL DEFAULT ''"},
	{"bills", "paid_at", "TEXT NOT NULL DEFAULT ''"},
}

func runMigrations(conn *sql.DB) error {
	for _, m := range columnMigrations {
		if err := addColumnIfMissing(conn, m.table, m.column, m.definition); err != nil {
			return err
		}
	}
	return backfillIndexes(conn)
}

// uniqueBackfill is a unique index that cannot simply be declared in schema.sql
// because databases predating it may already violate it. Each one names the
// duplicates it has to clear out first.
type uniqueBackfill struct {
	name   string
	dedupe func(*sql.DB) (int, error)
	create string
}

var uniqueBackfills = []uniqueBackfill{{
	name:   "idx_bills_statement",
	dedupe: dedupeBills,
	create: `CREATE UNIQUE INDEX IF NOT EXISTS idx_bills_statement
	         ON bills(issuer, card_last4, statement_period)
	         WHERE statement_period != ''`,
}}

// backfillIndexes adds unique indexes that arrived after data did.
//
// These live here rather than in schema.sql because schema.sql runs as one
// block against a database that may already hold rows violating the new
// constraint — CREATE UNIQUE INDEX then fails and the server refuses to start,
// which is exactly what an upgrade must never do.
func backfillIndexes(conn *sql.DB) error {
	for _, backfill := range uniqueBackfills {
		exists, err := indexExists(conn, backfill.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		removed, err := backfill.dedupe(conn)
		if err != nil {
			return fmt.Errorf("deduplicating for %s: %w", backfill.name, err)
		}
		if removed > 0 {
			log.Printf("migration: merged %d duplicate row(s) before creating %s", removed, backfill.name)
		}
		if _, err := conn.Exec(backfill.create); err != nil {
			return fmt.Errorf("creating %s: %w", backfill.name, err)
		}
	}
	return nil
}

// dedupeBills collapses repeated statements for the same card and period down
// to the earliest row, and returns how many it removed.
//
// The duplicates are real data, not junk: each arrived from its own email, and
// each may own a stored statement PDF and a message_links row pointing at it.
// So everything is repointed at the survivor *before* anything is deleted —
// pdf_attachments cascades on delete, and dropping a duplicate first would
// destroy a password-protected PDF that can't be re-fetched once the mail is
// gone.
func dedupeBills(conn *sql.DB) (int, error) {
	exists, err := tableExists(conn, "bills")
	if err != nil || !exists {
		return 0, err
	}

	tx, err := conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// survivors maps every duplicate bill id to the id it is being merged into.
	const survivorQuery = `
		SELECT b.id, keep.keep_id
		FROM bills b
		JOIN (
			SELECT issuer, card_last4, statement_period, MIN(id) AS keep_id
			FROM bills
			WHERE statement_period != ''
			GROUP BY issuer, card_last4, statement_period
			HAVING COUNT(*) > 1
		) keep
		  ON  b.issuer = keep.issuer
		  AND b.card_last4 = keep.card_last4
		  AND b.statement_period = keep.statement_period
		WHERE b.id != keep.keep_id`

	rows, err := tx.Query(survivorQuery)
	if err != nil {
		return 0, err
	}
	type merge struct{ from, into int64 }
	var merges []merge
	for rows.Next() {
		var m merge
		if err := rows.Scan(&m.from, &m.into); err != nil {
			rows.Close()
			return 0, err
		}
		merges = append(merges, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(merges) == 0 {
		return 0, nil
	}

	for _, m := range merges {
		// The survivor keeps whichever copy actually carried the figures: a
		// reminder resend often omits the amounts the original stated.
		if _, err := tx.Exec(`
			UPDATE bills SET
				total_due   = COALESCE(total_due,   (SELECT total_due   FROM bills WHERE id = ?)),
				minimum_due = COALESCE(minimum_due, (SELECT minimum_due FROM bills WHERE id = ?)),
				due_date    = CASE WHEN due_date = ''
				              THEN (SELECT due_date FROM bills WHERE id = ?) ELSE due_date END,
				account_id  = COALESCE(account_id,  (SELECT account_id  FROM bills WHERE id = ?))
			WHERE id = ?`, m.from, m.from, m.from, m.from, m.into); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE pdf_attachments SET bill_id = ? WHERE bill_id = ?`, m.into, m.from); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE message_links SET bill_id = ? WHERE bill_id = ?`, m.into, m.from); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM bills WHERE id = ?`, m.from); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(merges), nil
}

func indexExists(conn *sql.DB, name string) (bool, error) {
	var n int
	err := conn.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
	).Scan(&n)
	return n > 0, err
}

// addMissingColumnsToExistingTables runs the same column migrations *before*
// schema.sql, but only against tables that already exist.
//
// schema.sql is not purely additive: it declares indexes over columns the newer
// schema introduced, and CREATE INDEX on a pre-merge table fails with "no such
// column" before the ALTER TABLE has run. Tables that do not exist yet are
// skipped — schema.sql is about to create them with the columns already in
// place.
func addMissingColumnsToExistingTables(conn *sql.DB) error {
	for _, m := range columnMigrations {
		exists, err := tableExists(conn, m.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := addColumnIfMissing(conn, m.table, m.column, m.definition); err != nil {
			return err
		}
	}
	return nil
}

func addColumnIfMissing(conn *sql.DB, table, column, definition string) error {
	rows, err := conn.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", table, err)
	}
	defer rows.Close()

	exists := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dfltValue interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			exists = true
			break
		}
	}
	rows.Close()

	if exists {
		return nil
	}

	if _, err := conn.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		return fmt.Errorf("adding column %s.%s: %w", table, column, err)
	}
	return nil
}
