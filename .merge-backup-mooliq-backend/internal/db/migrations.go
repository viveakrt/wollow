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
func runMigrations(conn *sql.DB) error {
	if err := addColumnIfMissing(conn, "transactions", "linked_txn_id", "INTEGER REFERENCES transactions(id) ON DELETE SET NULL"); err != nil {
		return err
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

	_, err = conn.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	if err != nil {
		return fmt.Errorf("adding column %s.%s: %w", table, column, err)
	}
	return nil
}
