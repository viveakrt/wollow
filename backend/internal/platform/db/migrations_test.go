package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// legacySchema is the pre-merge Wollow schema, verbatim. Keeping it exact
// matters: a hand-trimmed copy passed this test while the real migration still
// failed on columns the trimmed version happened to omit.
const legacySchema = `
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

// seedLegacy builds a pre-merge database at dir/wollow.db, without going through
// Open (which would migrate it).
func seedLegacy(t *testing.T, dir string, messageCount int) {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "wollow.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Exec(legacySchema); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}
	if _, err := conn.Exec(`
		INSERT INTO accounts (label, imap_host, imap_port, username, encrypted_password)
		VALUES ('google', 'imap.gmail.com', 993, 'someone@example.com', 'ciphertext')`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO settings (id, ai_provider, encrypted_api_key, model_name)
		VALUES (1, 'anthropic', 'encrypted-key', 'claude')`); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	for i := 1; i <= messageCount; i++ {
		res, err := conn.Exec(`
			INSERT INTO messages (account_id, folder, uid, subject, from_email)
			VALUES (1, 'INBOX', ?, ?, 'sender@example.com')`, i, "subject")
		if err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
		id, _ := res.LastInsertId()
		if i%2 == 0 {
			if _, err := conn.Exec(`
				INSERT INTO classifications (message_id, category, confidence, model)
				VALUES (?, 'finance', 0.9, 'test-model')`, id); err != nil {
				t.Fatalf("seed classification: %v", err)
			}
		}
	}
}

// TestOpenMigratesLegacyDatabase covers the upgrade path for a Wollow instance
// that was already running before Mail and Money merged. The message index and
// its classifications represent real sync time and real model spend, so the
// rename has to carry them across, not start over.
func TestOpenMigratesLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	const messages = 200
	seedLegacy(t, dir, messages)

	conn, err := Open(dir)
	if err != nil {
		t.Fatalf("Open on a legacy database: %v", err)
	}
	defer conn.Close()

	if exists, _ := tableExists(conn, "accounts"); exists {
		t.Error("legacy 'accounts' table still present after migration")
	}
	if exists, _ := tableExists(conn, "mail_accounts"); !exists {
		t.Fatal("'mail_accounts' missing after migration")
	}

	var label, username string
	if err := conn.QueryRow(`SELECT label, username FROM mail_accounts WHERE id = 1`).Scan(&label, &username); err != nil {
		t.Fatalf("mailbox did not survive: %v", err)
	}
	if label != "google" || username != "someone@example.com" {
		t.Errorf("mailbox changed: %q / %q", label, username)
	}

	var msgCount, classCount int
	conn.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	conn.QueryRow(`SELECT COUNT(*) FROM classifications`).Scan(&classCount)
	if msgCount != messages {
		t.Errorf("message index lost rows: have %d, want %d", msgCount, messages)
	}
	if classCount != messages/2 {
		t.Errorf("classifications lost rows: have %d, want %d", classCount, messages/2)
	}

	// The AI config is encrypted with the master key; losing it would mean
	// re-entering the API key.
	var provider, key string
	conn.QueryRow(`SELECT ai_provider, encrypted_api_key FROM settings WHERE id = 1`).Scan(&provider, &key)
	if provider != "anthropic" || key != "encrypted-key" {
		t.Errorf("settings changed: %q / %q", provider, key)
	}

	// The new columns and Money's tables must be there afterwards.
	if _, err := conn.Exec(`UPDATE messages SET rfc_message_id = 'x' WHERE id = 1`); err != nil {
		t.Errorf("messages.rfc_message_id missing after migration: %v", err)
	}
	for _, table := range []string{"finance_accounts", "transactions", "bills", "message_links", "categories"} {
		if exists, _ := tableExists(conn, table); !exists {
			t.Errorf("Money table %q was not created", table)
		}
	}

	// The renamed table must still be a valid FK target.
	rows, err := conn.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	violations := 0
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Errorf("%d foreign key violations after migration", violations)
	}
}

// TestOpenIsIdempotentOnMigratedDatabase makes sure a second startup does not
// try to migrate again.
func TestOpenIsIdempotentOnMigratedDatabase(t *testing.T) {
	dir := t.TempDir()
	seedLegacy(t, dir, 10)

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	first.Close()

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("second open on an already-migrated database: %v", err)
	}
	defer second.Close()

	var msgCount int
	second.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount)
	if msgCount != 10 {
		t.Errorf("second open changed the message count: %d", msgCount)
	}
}

// A database written before bills gained their uniqueness constraint already
// holds duplicate statements — issuers resend them. Declaring the index in
// schema.sql made Open fail outright on exactly those databases, which is the
// one thing an upgrade must never do.
func TestOpenBackfillsBillIndexOverExistingDuplicates(t *testing.T) {
	dir := t.TempDir()

	// A database at the previous schema: everything present except the index.
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := first.Exec(`DROP INDEX IF EXISTS idx_bills_statement`); err != nil {
		t.Fatalf("dropping the index to simulate the old schema: %v", err)
	}

	// Three copies of one statement, as three resends would leave it. Only the
	// second states the amounts; only the third carried the PDF.
	for i, bill := range []struct{ total, min any }{
		{nil, nil},
		{8267.0, 420.0},
		{nil, nil},
	} {
		res, err := first.Exec(`
			INSERT INTO bills (issuer, card_last4, statement_period, total_due, minimum_due, due_date)
			VALUES ('ICICI', '7001', 'June 13, 2026 to July 12, 2026', ?, ?, ?)`,
			bill.total, bill.min, map[bool]string{true: "July 30, 2026", false: ""}[i == 1])
		if err != nil {
			t.Fatalf("seeding duplicate bill %d: %v", i, err)
		}
		id, _ := res.LastInsertId()
		if i == 2 {
			if _, err := first.Exec(
				`INSERT INTO pdf_attachments (bill_id, file_name, content) VALUES (?, 'stmt.pdf', ?)`,
				id, []byte("%PDF-1.4"),
			); err != nil {
				t.Fatalf("seeding attachment: %v", err)
			}
		}
	}
	// A distinct statement that must survive untouched.
	if _, err := first.Exec(`
		INSERT INTO bills (issuer, card_last4, statement_period)
		VALUES ('BOBCARD', '3109', 'June 2026')`); err != nil {
		t.Fatalf("seeding unrelated bill: %v", err)
	}
	first.Close()

	// The upgrade this test exists for.
	conn, err := Open(dir)
	if err != nil {
		t.Fatalf("Open on a database with duplicate bills: %v", err)
	}
	defer conn.Close()

	if exists, _ := indexExists(conn, "idx_bills_statement"); !exists {
		t.Error("idx_bills_statement was not created")
	}

	var icici int
	conn.QueryRow(`SELECT COUNT(*) FROM bills WHERE issuer = 'ICICI'`).Scan(&icici)
	if icici != 1 {
		t.Errorf("%d ICICI bills survived, want 1", icici)
	}
	var total int
	conn.QueryRow(`SELECT COUNT(*) FROM bills`).Scan(&total)
	if total != 2 {
		t.Errorf("%d bills total, want 2 — the unrelated statement must survive", total)
	}

	// The merged row must keep the figures only one of the copies stated.
	var totalDue, minDue sql.NullFloat64
	var dueDate string
	if err := conn.QueryRow(
		`SELECT total_due, minimum_due, due_date FROM bills WHERE issuer = 'ICICI'`,
	).Scan(&totalDue, &minDue, &dueDate); err != nil {
		t.Fatalf("reading merged bill: %v", err)
	}
	if !totalDue.Valid || totalDue.Float64 != 8267 {
		t.Errorf("total_due = %v, want 8267 — the merge dropped a figure", totalDue)
	}
	if !minDue.Valid || minDue.Float64 != 420 {
		t.Errorf("minimum_due = %v, want 420", minDue)
	}
	if dueDate != "July 30, 2026" {
		t.Errorf("due_date = %q, want the one the copies carried", dueDate)
	}

	// pdf_attachments cascades on delete: dropping a duplicate before
	// repointing would have destroyed a PDF that cannot be re-fetched.
	var attachments int
	conn.QueryRow(`SELECT COUNT(*) FROM pdf_attachments`).Scan(&attachments)
	if attachments != 1 {
		t.Errorf("%d stored statement PDFs, want 1 — the merge lost one", attachments)
	}
	var orphaned int
	conn.QueryRow(`
		SELECT COUNT(*) FROM pdf_attachments a
		WHERE NOT EXISTS (SELECT 1 FROM bills b WHERE b.id = a.bill_id)`).Scan(&orphaned)
	if orphaned != 0 {
		t.Errorf("%d attachments point at a deleted bill", orphaned)
	}

	// And the constraint has to actually bite from here on.
	if _, err := conn.Exec(`
		INSERT INTO bills (issuer, card_last4, statement_period)
		VALUES ('ICICI', '7001', 'June 13, 2026 to July 12, 2026')`); err == nil {
		t.Error("a duplicate statement was still accepted after the backfill")
	}
}

// TestOpenRefusesAmbiguousSchema: if both names exist, guessing risks throwing
// away whichever one holds the real mailboxes.
func TestOpenRefusesAmbiguousSchema(t *testing.T) {
	dir := t.TempDir()
	seedLegacy(t, dir, 1)

	conn, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "wollow.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE mail_accounts (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create conflicting table: %v", err)
	}
	conn.Close()

	if _, err := Open(dir); err == nil {
		t.Error("Open silently proceeded with both 'accounts' and 'mail_accounts' present")
	}
}
