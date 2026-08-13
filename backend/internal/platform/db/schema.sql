-- Wollow unified schema (SQLite).
--
-- Two products share this file: Mail (mailbox index, classification) and
-- Money (accounts, transactions, bills). Table names carry the product
-- prefix wherever the same word means different things in each:
--
--   mail_accounts     -- an IMAP mailbox
--   finance_accounts  -- a bank / card / wallet account
--
-- Both were called "accounts" in the two apps this was merged from. They are
-- both INTEGER PRIMARY KEY, so a crossed foreign key would silently attach
-- transactions to a mailbox with no error raised. Do not reintroduce a bare
-- "accounts" table.

-- =====================================================================
-- PLATFORM
-- =====================================================================

-- One connected mailbox. This is the single credential store for the whole
-- app: Mail syncs it, Money reads finance mail out of it. Passwords are
-- AES-GCM encrypted with WOLLOW_MASTER_KEY, never stored in plaintext.
CREATE TABLE IF NOT EXISTS mail_accounts (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    label              TEXT NOT NULL,
    provider_type      TEXT NOT NULL DEFAULT 'imap',
    imap_host          TEXT NOT NULL,
    imap_port          INTEGER NOT NULL,
    smtp_host          TEXT NOT NULL DEFAULT '',
    smtp_port          INTEGER NOT NULL DEFAULT 0,
    username           TEXT NOT NULL,
    encrypted_password TEXT NOT NULL,
    use_tls            INTEGER NOT NULL DEFAULT 1,
    enabled            INTEGER NOT NULL DEFAULT 1,
    created_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Single-row table: AI provider config, shared by both products.
CREATE TABLE IF NOT EXISTS settings (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    ai_provider       TEXT NOT NULL DEFAULT 'none',
    encrypted_api_key TEXT NOT NULL DEFAULT '',
    model_name        TEXT NOT NULL DEFAULT '',
    base_url          TEXT NOT NULL DEFAULT ''
);

-- =====================================================================
-- MAIL
-- =====================================================================

-- Local index of message headers + a short snippet. Bodies are never stored;
-- they stay live-fetched from IMAP on open. This index is what makes sender
-- aggregation, smart-view counts, and bulk actions possible at all -- and as
-- of the merge it is also what Money ingest reads instead of opening its own
-- IMAP session.
CREATE TABLE IF NOT EXISTS messages (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id     INTEGER NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
    folder         TEXT NOT NULL,
    uid            INTEGER NOT NULL,
    rfc_message_id TEXT NOT NULL DEFAULT '',  -- RFC 5322 Message-ID; Money dedupes on this
    subject        TEXT NOT NULL DEFAULT '',
    from_name      TEXT NOT NULL DEFAULT '',
    from_email     TEXT NOT NULL DEFAULT '',
    from_domain    TEXT NOT NULL DEFAULT '',
    date           TEXT NOT NULL DEFAULT '',
    seen           INTEGER NOT NULL DEFAULT 0,
    flagged        INTEGER NOT NULL DEFAULT 0,
    size           INTEGER NOT NULL DEFAULT 0,
    snippet        TEXT NOT NULL DEFAULT '',
    synced_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(account_id, folder, uid)
);

CREATE INDEX IF NOT EXISTS idx_messages_acct_folder_date
    ON messages(account_id, folder, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_from_email
    ON messages(account_id, from_email);
CREATE INDEX IF NOT EXISTS idx_messages_rfc_id
    ON messages(rfc_message_id);

CREATE TABLE IF NOT EXISTS sync_state (
    account_id     INTEGER NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
    folder         TEXT NOT NULL,
    last_uid       INTEGER NOT NULL DEFAULT 0,
    last_synced_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, folder)
);

-- One row per classified message. Deliberately has no "state" column: read/
-- unread/starred are always derived from live IMAP flags so they cannot drift.
CREATE TABLE IF NOT EXISTS classifications (
    message_id        INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    category          TEXT NOT NULL DEFAULT 'other',
    subcategory       TEXT NOT NULL DEFAULT '',
    sender_group      TEXT NOT NULL DEFAULT 'other',
    priority          TEXT NOT NULL DEFAULT 'medium',
    action            TEXT NOT NULL DEFAULT 'no_action',
    requires_response INTEGER NOT NULL DEFAULT 0,
    requires_payment  INTEGER NOT NULL DEFAULT 0,
    has_deadline      INTEGER NOT NULL DEFAULT 0,
    deadline          TEXT NOT NULL DEFAULT '',
    is_newsletter     INTEGER NOT NULL DEFAULT 0,
    is_promotional    INTEGER NOT NULL DEFAULT 0,
    is_transactional  INTEGER NOT NULL DEFAULT 0,
    is_security_alert INTEGER NOT NULL DEFAULT 0,
    confidence        REAL NOT NULL DEFAULT 0,
    summary           TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    classified_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_classifications_category ON classifications(category);
CREATE INDEX IF NOT EXISTS idx_classifications_priority ON classifications(priority);

-- =====================================================================
-- MONEY
-- =====================================================================

CREATE TABLE IF NOT EXISTS finance_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    bank            TEXT NOT NULL DEFAULT '',
    account_type    TEXT NOT NULL DEFAULT 'bank', -- bank, credit_card, wallet, investment, loan, ppf, fd
    account_number  TEXT NOT NULL DEFAULT '',     -- masked, e.g. XXXXXXXX4125
    currency        TEXT NOT NULL DEFAULT 'INR',
    opening_balance REAL NOT NULL DEFAULT 0,
    current_balance REAL NOT NULL DEFAULT 0,
    ifsc            TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS categories (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    type       TEXT NOT NULL DEFAULT 'expense', -- expense, income
    icon       TEXT NOT NULL DEFAULT '',
    color      TEXT NOT NULL DEFAULT '#8b5cf6',
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS import_batches (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id     INTEGER REFERENCES finance_accounts(id) ON DELETE SET NULL,
    file_name      TEXT NOT NULL,
    bank           TEXT NOT NULL DEFAULT 'HDFC',
    total_rows     INTEGER NOT NULL DEFAULT 0,
    imported_rows  INTEGER NOT NULL DEFAULT 0,
    duplicate_rows INTEGER NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'pending', -- pending, review, done, failed
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS transactions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL REFERENCES finance_accounts(id) ON DELETE CASCADE,
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
    payment_method  TEXT NOT NULL DEFAULT '',        -- UPI, NEFT, IMPS, POS, ATM, etc.
    notes           TEXT NOT NULL DEFAULT '',
    import_batch_id INTEGER REFERENCES import_batches(id) ON DELETE SET NULL,
    dedupe_hash     TEXT NOT NULL DEFAULT '',
    linked_txn_id   INTEGER REFERENCES transactions(id) ON DELETE SET NULL, -- matching leg of a cross-account transfer
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_txn_dedupe ON transactions(account_id, dedupe_hash);
CREATE INDEX IF NOT EXISTS idx_txn_date ON transactions(txn_date);
CREATE INDEX IF NOT EXISTS idx_txn_account ON transactions(account_id);
CREATE INDEX IF NOT EXISTS idx_txn_category ON transactions(category_id);

CREATE TABLE IF NOT EXISTS category_rules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    match_field TEXT NOT NULL DEFAULT 'narration', -- narration, merchant
    match_type  TEXT NOT NULL DEFAULT 'contains',  -- contains, starts_with, regex
    match_value TEXT NOT NULL,
    category_id INTEGER REFERENCES categories(id) ON DELETE CASCADE,
    set_type    TEXT NOT NULL DEFAULT '',          -- optionally force income/expense/transfer
    priority    INTEGER NOT NULL DEFAULT 0,
    enabled     INTEGER NOT NULL DEFAULT 1
);

-- Credit card bill/statement reminders extracted from statement emails
-- (due date + total/min due).
CREATE TABLE IF NOT EXISTS bills (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id       INTEGER REFERENCES finance_accounts(id) ON DELETE SET NULL,
    issuer           TEXT NOT NULL DEFAULT '',      -- HDFC, ICICI, Axis, BOBCARD...
    card_last4       TEXT NOT NULL DEFAULT '',
    statement_period TEXT NOT NULL DEFAULT '',
    total_due        REAL,
    minimum_due      REAL,
    due_date         TEXT NOT NULL DEFAULT '',
    source_email_id  INTEGER REFERENCES message_links(id) ON DELETE SET NULL,
    status           TEXT NOT NULL DEFAULT 'unpaid', -- unpaid, paid
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_bills_due_date ON bills(due_date);

-- The join between a mail message and whatever Money made of it. This is the
-- table that powers cross-product links in both directions: inbox message ->
-- "this created a transaction", and transaction -> "source email".
--
-- rfc_message_id is the dedupe key today. message_id (the FK into the shared
-- mail index) is populated once Money ingest reads from that index instead of
-- fetching its own mail.
CREATE TABLE IF NOT EXISTS message_links (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    mail_account_id INTEGER NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
    message_id      INTEGER REFERENCES messages(id) ON DELETE SET NULL,
    rfc_message_id  TEXT NOT NULL,
    uid             INTEGER NOT NULL DEFAULT 0,
    sender          TEXT NOT NULL DEFAULT '',
    subject         TEXT NOT NULL DEFAULT '',
    received_at     TEXT NOT NULL DEFAULT '',
    parsed_as       TEXT NOT NULL DEFAULT '',      -- transaction | bill | unrecognized
    transaction_id  INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
    bill_id         INTEGER REFERENCES bills(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_link_dedupe
    ON message_links(mail_account_id, rfc_message_id);
CREATE INDEX IF NOT EXISTS idx_message_link_message ON message_links(message_id);

-- Money's own IMAP cursor, separate from sync_state because it tracks a
-- different thing: the highest UID *parsed for finance*, not the highest UID
-- indexed. Dropped once Money ingest reads the shared index and derives its
-- backlog from messages that have no message_links row yet.
CREATE TABLE IF NOT EXISTS money_ingest_state (
    mail_account_id INTEGER PRIMARY KEY REFERENCES mail_accounts(id) ON DELETE CASCADE,
    last_uid        INTEGER NOT NULL DEFAULT 0,
    last_synced_at  TEXT NOT NULL DEFAULT ''
);

-- One password formula per card issuer (most Indian issuers use a fixed
-- formula like name+DOB). Encrypted at rest with the same master key as
-- mailbox passwords.
CREATE TABLE IF NOT EXISTS pdf_passwords (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    issuer             TEXT NOT NULL UNIQUE,
    encrypted_password TEXT NOT NULL,
    created_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Raw PDF attachment bytes from a bill email, kept so the itemized
-- transaction parser can run (immediately, or retried later once a password
-- has been configured) without re-fetching the email over IMAP. This is a
-- deliberate, documented exception to the "no message bodies at rest" rule
-- the mail index otherwise follows.
CREATE TABLE IF NOT EXISTS pdf_attachments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    bill_id     INTEGER REFERENCES bills(id) ON DELETE CASCADE,
    file_name   TEXT NOT NULL DEFAULT '',
    content     BLOB NOT NULL,
    parsed      INTEGER NOT NULL DEFAULT 0,     -- 1 once itemized transactions were extracted
    parse_error TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Auto-detected candidate pairs (e.g. a bank debit that looks like a credit
-- card bill payment) awaiting user confirmation. Confirming sets
-- linked_txn_id on both transactions; dismissing just marks status so the
-- same pair isn't re-suggested on the next scan.
CREATE TABLE IF NOT EXISTS transfer_suggestions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    txn_id_a   INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    txn_id_b   INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    confidence REAL NOT NULL DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'pending', -- pending, confirmed, dismissed
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_transfer_suggestion_pair ON transfer_suggestions(
    min(txn_id_a, txn_id_b), max(txn_id_a, txn_id_b)
);

-- =====================================================================
-- SEED
-- =====================================================================

INSERT OR IGNORE INTO settings (id) VALUES (1);

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
