package db

import (
	"database/sql"
	"fmt"
)

// runMigrations applies idempotent, additive changes that CREATE TABLE IF
// NOT EXISTS can't express — mainly ALTER TABLE ADD COLUMN on tables that
// may already exist from an earlier version of schema.sql. Every migration
// here must be safe to run repeatedly (check-before-alter) since it runs on
// every startup, not just once.
//
// Renames and FK retargets are deliberately NOT handled here. The merge that
// created this schema started from an empty database precisely so those never
// had to run against live data.
func runMigrations(conn *sql.DB) error {
	migrations := []struct{ table, column, definition string }{
		{"settings", "base_url", "TEXT NOT NULL DEFAULT ''"},
		{"mail_accounts", "enabled", "INTEGER NOT NULL DEFAULT 1"},
		{"messages", "rfc_message_id", "TEXT NOT NULL DEFAULT ''"},
		{"message_links", "message_id", "INTEGER REFERENCES messages(id) ON DELETE SET NULL"},
		{"transactions", "linked_txn_id", "INTEGER REFERENCES transactions(id) ON DELETE SET NULL"},
	}

	for _, m := range migrations {
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
