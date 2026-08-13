package db

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	label TEXT NOT NULL,
	provider_type TEXT NOT NULL DEFAULT 'imap',
	imap_host TEXT NOT NULL,
	imap_port INTEGER NOT NULL,
	smtp_host TEXT NOT NULL DEFAULT '',
	smtp_port INTEGER NOT NULL DEFAULT 0,
	username TEXT NOT NULL,
	encrypted_password TEXT NOT NULL,
	use_tls INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS settings (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	ai_provider TEXT NOT NULL DEFAULT 'none',
	encrypted_api_key TEXT NOT NULL DEFAULT '',
	model_name TEXT NOT NULL DEFAULT '',
	base_url TEXT NOT NULL DEFAULT ''
);

-- Local index of message headers + a short snippet. Bodies are never stored;
-- they stay live-fetched from IMAP on open. This index is what makes sender
-- aggregation, smart-view counts, and bulk actions possible at all.
CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	folder TEXT NOT NULL,
	uid INTEGER NOT NULL,
	subject TEXT NOT NULL DEFAULT '',
	from_name TEXT NOT NULL DEFAULT '',
	from_email TEXT NOT NULL DEFAULT '',
	from_domain TEXT NOT NULL DEFAULT '',
	date TEXT NOT NULL DEFAULT '',
	seen INTEGER NOT NULL DEFAULT 0,
	flagged INTEGER NOT NULL DEFAULT 0,
	size INTEGER NOT NULL DEFAULT 0,
	snippet TEXT NOT NULL DEFAULT '',
	synced_at TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(account_id, folder, uid)
);

CREATE INDEX IF NOT EXISTS idx_messages_acct_folder_date
	ON messages(account_id, folder, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_from_email
	ON messages(account_id, from_email);

CREATE TABLE IF NOT EXISTS sync_state (
	account_id INTEGER NOT NULL,
	folder TEXT NOT NULL,
	last_uid INTEGER NOT NULL DEFAULT 0,
	last_synced_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (account_id, folder)
);

-- One row per classified message. Deliberately has no "state" column: read/
-- unread/starred are always derived from live IMAP flags so they cannot drift.
CREATE TABLE IF NOT EXISTS classifications (
	message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
	category TEXT NOT NULL DEFAULT 'other',
	subcategory TEXT NOT NULL DEFAULT '',
	sender_group TEXT NOT NULL DEFAULT 'other',
	priority TEXT NOT NULL DEFAULT 'medium',
	action TEXT NOT NULL DEFAULT 'no_action',
	requires_response INTEGER NOT NULL DEFAULT 0,
	requires_payment INTEGER NOT NULL DEFAULT 0,
	has_deadline INTEGER NOT NULL DEFAULT 0,
	deadline TEXT NOT NULL DEFAULT '',
	is_newsletter INTEGER NOT NULL DEFAULT 0,
	is_promotional INTEGER NOT NULL DEFAULT 0,
	is_transactional INTEGER NOT NULL DEFAULT 0,
	is_security_alert INTEGER NOT NULL DEFAULT 0,
	confidence REAL NOT NULL DEFAULT 0,
	summary TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	classified_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_classifications_category ON classifications(category);
CREATE INDEX IF NOT EXISTS idx_classifications_priority ON classifications(priority);
`

func Open(dataDir string) (*sql.DB, error) {
	path := filepath.Join(dataDir, "wollow.db")
	// foreign_keys must be enabled explicitly for the ON DELETE CASCADE on
	// messages/classifications to fire; SQLite defaults it off.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		path,
	)

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // modernc.org/sqlite: keep writes serialized

	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	if _, err := conn.Exec(`INSERT OR IGNORE INTO settings (id) VALUES (1)`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("seeding settings: %w", err)
	}

	return conn, nil
}

// migrate applies additive schema changes to databases created by earlier
// versions of Wollow, since CREATE TABLE IF NOT EXISTS only helps new DBs.
func migrate(conn *sql.DB) error {
	rows, err := conn.Query(`PRAGMA table_info(settings)`)
	if err != nil {
		return err
	}
	hasBaseURL := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "base_url" {
			hasBaseURL = true
		}
	}
	rows.Close()

	if !hasBaseURL {
		if _, err := conn.Exec(`ALTER TABLE settings ADD COLUMN base_url TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}
