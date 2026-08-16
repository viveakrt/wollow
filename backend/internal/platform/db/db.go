// Package db opens the single SQLite file both products share and applies
// the unified schema. See schema.sql for the table layout and the naming
// rules that keep Mail's and Money's "accounts" from colliding.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

func Open(dataDir string) (*sql.DB, error) {
	path := filepath.Join(dataDir, "wollow.db")
	// foreign_keys must be enabled explicitly for the ON DELETE CASCADE on
	// messages/classifications/transactions to fire; SQLite defaults it off.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		path,
	)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // modernc.org/sqlite: keep writes serialized

	// Renames must run before the schema, or CREATE TABLE IF NOT EXISTS would
	// create an empty table under the new name and the old one's data would be
	// stranded beside it. Columns follow, because schema.sql indexes some of
	// them and would fail on a table that predates them.
	if err := renameLegacyTables(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("renaming legacy tables: %w", err)
	}
	if err := addMissingColumnsToExistingTables(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("upgrading existing tables: %w", err)
	}

	if _, err := conn.Exec(schemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	if err := runMigrations(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return conn, nil
}
