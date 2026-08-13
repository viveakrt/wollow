-- Mooliq / FinTrack schema (SQLite)

CREATE TABLE IF NOT EXISTS accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    bank            TEXT NOT NULL DEFAULT '',
    account_type    TEXT NOT NULL DEFAULT 'bank', -- bank, credit_card, wallet, investment, loan, ppf, fd
    account_number  TEXT NOT NULL DEFAULT '',      -- masked, e.g. XXXXXXXX4125
    currency        TEXT NOT NULL DEFAULT 'INR',
    opening_balance REAL NOT NULL DEFAULT 0,
    current_balance REAL NOT NULL DEFAULT 0,
    ifsc            TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS categories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL DEFAULT 'expense', -- expense, income
    icon        TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '#8b5cf6',
    sort_order  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS transactions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    txn_date        TEXT NOT NULL,      -- ISO 8601 date, YYYY-MM-DD
    value_date      TEXT NOT NULL,
    narration       TEXT NOT NULL DEFAULT '',
    ref_no          TEXT NOT NULL DEFAULT '',
    withdrawal_amt  REAL NOT NULL DEFAULT 0,
    deposit_amt     REAL NOT NULL DEFAULT 0,
    closing_balance REAL,
    type            TEXT NOT NULL DEFAULT 'expense', -- income, expense, transfer
    category_id     INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    merchant        TEXT NOT NULL DEFAULT '',
    payment_method  TEXT NOT NULL DEFAULT '',         -- UPI, NEFT, IMPS, POS, ATM, etc.
    notes           TEXT NOT NULL DEFAULT '',
    import_batch_id INTEGER REFERENCES import_batches(id) ON DELETE SET NULL,
    dedupe_hash     TEXT NOT NULL DEFAULT '',
    linked_txn_id   INTEGER REFERENCES transactions(id) ON DELETE SET NULL, -- the matching leg of a cross-account transfer
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_txn_dedupe ON transactions(account_id, dedupe_hash);
CREATE INDEX IF NOT EXISTS idx_txn_date ON transactions(txn_date);
CREATE INDEX IF NOT EXISTS idx_txn_account ON transactions(account_id);
CREATE INDEX IF NOT EXISTS idx_txn_category ON transactions(category_id);

CREATE TABLE IF NOT EXISTS import_batches (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    file_name       TEXT NOT NULL,
    bank            TEXT NOT NULL DEFAULT 'HDFC',
    total_rows      INTEGER NOT NULL DEFAULT 0,
    imported_rows   INTEGER NOT NULL DEFAULT 0,
    duplicate_rows  INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending, review, done, failed
    error           TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS category_rules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    match_field     TEXT NOT NULL DEFAULT 'narration', -- narration, merchant
    match_type      TEXT NOT NULL DEFAULT 'contains',  -- contains, starts_with, regex
    match_value     TEXT NOT NULL,
    category_id     INTEGER REFERENCES categories(id) ON DELETE CASCADE,
    set_type        TEXT NOT NULL DEFAULT '',           -- optionally force income/expense/transfer
    priority        INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS email_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    email           TEXT NOT NULL UNIQUE,
    imap_host       TEXT NOT NULL DEFAULT 'imap.gmail.com',
    imap_port       INTEGER NOT NULL DEFAULT 993,
    app_password    TEXT NOT NULL,               -- stored locally only; this app has no server component
    last_synced_at  TEXT NOT NULL DEFAULT '',
    last_uid        INTEGER NOT NULL DEFAULT 0,    -- last IMAP UID processed, for incremental sync
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Tracks every allowlisted-sender email we've already processed, so re-sync
-- never double-imports. message_id is the email's Message-ID header.
CREATE TABLE IF NOT EXISTS email_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    email_account_id INTEGER NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
    message_id      TEXT NOT NULL,
    uid             INTEGER NOT NULL DEFAULT 0,
    sender          TEXT NOT NULL DEFAULT '',
    subject         TEXT NOT NULL DEFAULT '',
    received_at     TEXT NOT NULL DEFAULT '',
    parsed_as       TEXT NOT NULL DEFAULT '',      -- transaction | bill | unrecognized
    transaction_id  INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    bill_id         INTEGER REFERENCES bills(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_msg_dedupe ON email_messages(email_account_id, message_id);

-- Credit card bill/statement reminders extracted from statement emails
-- (due date + total/min due). Full itemized transactions from the PDF
-- attachment are not parsed yet.
CREATE TABLE IF NOT EXISTS bills (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    issuer          TEXT NOT NULL DEFAULT '',      -- HDFC, ICICI, Axis, BOBCARD...
    card_last4      TEXT NOT NULL DEFAULT '',
    statement_period TEXT NOT NULL DEFAULT '',
    total_due       REAL,
    minimum_due     REAL,
    due_date        TEXT NOT NULL DEFAULT '',
    source_email_id INTEGER REFERENCES email_messages(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'unpaid', -- unpaid, paid
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_bills_due_date ON bills(due_date);

-- One password formula per card issuer (most Indian banks/card issuers use
-- a fixed, non-secret formula like name+DOB, so this rarely changes and is
-- entered once). Stored locally only, same as the Gmail app password.
CREATE TABLE IF NOT EXISTS pdf_passwords (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    issuer      TEXT NOT NULL UNIQUE,
    password    TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Raw PDF attachment bytes from a bill email, kept so the itemized
-- transaction parser can run (immediately, or retried later once a
-- password has been configured) without re-fetching the email over IMAP.
CREATE TABLE IF NOT EXISTS pdf_attachments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    bill_id         INTEGER REFERENCES bills(id) ON DELETE CASCADE,
    file_name       TEXT NOT NULL DEFAULT '',
    content         BLOB NOT NULL,
    parsed          INTEGER NOT NULL DEFAULT 0,     -- 1 once itemized transactions were extracted
    parse_error     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Auto-detected candidate pairs (e.g. bank debit that looks like a credit
-- card bill payment) awaiting user confirmation. Confirming one sets
-- linked_txn_id on both transactions and status here; dismissing just
-- marks status so the same pair isn't re-suggested on the next scan.
CREATE TABLE IF NOT EXISTS transfer_suggestions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    txn_id_a        INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    txn_id_b        INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    confidence      REAL NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending, confirmed, dismissed
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_transfer_suggestion_pair ON transfer_suggestions(
    min(txn_id_a, txn_id_b), max(txn_id_a, txn_id_b)
);

INSERT OR IGNORE INTO categories (name, type, icon, color, sort_order) VALUES
    ('Food & Dining', 'expense', 'utensils', '#ef4444', 1),
    ('Groceries', 'expense', 'shopping-basket', '#f97316', 2),
    ('Transport', 'expense', 'car', '#eab308', 3),
    ('Shopping', 'expense', 'shopping-bag', '#22c55e', 4),
    ('Utilities', 'expense', 'zap', '#06b6d4', 5),
    ('Entertainment', 'expense', 'film', '#a855f7', 6),
    ('Health', 'expense', 'heart', '#ec4899', 7),
    ('Subscriptions', 'expense', 'repeat', '#3b82f6', 8),
    ('Family', 'expense', 'users', '#8b5cf6', 9),
    ('Rent', 'expense', 'home', '#f59e0b', 10),
    ('Investment', 'expense', 'trending-up', '#10b981', 11),
    ('Insurance', 'expense', 'shield', '#14b8a6', 12),
    ('Fees & Charges', 'expense', 'receipt', '#64748b', 13),
    ('Others', 'expense', 'more-horizontal', '#6b7280', 14),
    ('Salary', 'income', 'briefcase', '#22c55e', 15),
    ('Interest', 'income', 'percent', '#06b6d4', 16),
    ('Refund', 'income', 'undo', '#3b82f6', 17),
    ('Other Income', 'income', 'plus-circle', '#84cc16', 18);
