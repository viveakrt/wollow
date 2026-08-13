package db

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // sqlite: avoid "database is locked" under concurrent writers

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
