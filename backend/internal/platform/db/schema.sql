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

-- One row per sender a user has unsubscribed from (or manually marked as
-- such). Local bookkeeping only -- it does not touch the mailing list itself
-- beyond whatever request the unsubscribe attempt already made.
CREATE TABLE IF NOT EXISTS sender_status (
    account_id          INTEGER NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
    from_email          TEXT NOT NULL,
    unsubscribed_at     TEXT NOT NULL DEFAULT '',
    unsubscribe_method  TEXT NOT NULL DEFAULT '', -- http, mailto, manual
    PRIMARY KEY (account_id, from_email)
);

-- =====================================================================
-- MONEY
-- =====================================================================

-- A bank account, card, wallet, loan or deposit. account_type decides whether
-- the balance counts as an asset or a liability on the dashboard, so alert
-- ingest works hard to get it right rather than defaulting everything to one
-- kind (see money/ledger/accounts.go).
CREATE TABLE IF NOT EXISTS finance_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    bank            TEXT NOT NULL DEFAULT '',
    account_type    TEXT NOT NULL DEFAULT 'bank', -- bank, credit_card, wallet, investment, loan, ppf, fd
    account_number  TEXT NOT NULL DEFAULT '',     -- masked, e.g. XXXXXXXX4125
    currency        TEXT NOT NULL DEFAULT 'INR',
    opening_balance REAL NOT NULL DEFAULT 0,
    current_balance REAL NOT NULL DEFAULT 0,
    credit_limit    REAL NOT NULL DEFAULT 0,      -- cards only; 0 means unknown
    ifsc            TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    -- How this row got here: manual, email (discovered by alert ingest), or
    -- statement. Only 'email' rows have their guessed account_type corrected
    -- automatically later; a type the user chose is never overwritten.
    source          TEXT NOT NULL DEFAULT 'manual',
    -- Whether this account's balance counts toward net worth. A family
    -- member's account, or a closed/business account, can be tracked without
    -- distorting the owner's own figures.
    include_in_networth INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_finance_accounts_number
    ON finance_accounts(account_number);

-- A balance the bank itself reported, from a balance-update email or the
-- summary block of a statement. This is stronger evidence than any sum over
-- transactions: RecomputeAccountBalance anchors on the most recent snapshot and
-- applies only the transactions dated after it, so an account with a partial
-- transaction history still shows a balance the bank agrees with.
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES finance_accounts(id) ON DELETE CASCADE,
    as_of      TEXT NOT NULL,                 -- YYYY-MM-DD
    balance    REAL NOT NULL,
    source     TEXT NOT NULL DEFAULT 'email', -- email, statement, manual
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(account_id, as_of)
);

-- Deposits and holdings: fixed deposits, PPF/EPF/NPS, mutual funds, stocks.
-- Kept apart from finance_accounts because the interesting facts are different
-- ones -- maturity date, rate, units -- and because a deposit has no
-- transaction stream to derive a balance from.
CREATE TABLE IF NOT EXISTS investments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER REFERENCES finance_accounts(id) ON DELETE SET NULL,
    kind            TEXT NOT NULL DEFAULT 'fd', -- fd, rd, ppf, epf, nps, mutual_fund, stock, us_stock, bond, gold, other
    institution     TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    identifier      TEXT NOT NULL DEFAULT '',   -- deposit / folio / demat number
    currency        TEXT NOT NULL DEFAULT 'INR',
    invested_amount REAL NOT NULL DEFAULT 0,
    current_value   REAL NOT NULL DEFAULT 0,
    maturity_amount REAL,
    interest_rate   REAL,
    units           REAL,
    -- Last known price per unit and when it was taken. Populated by hand or by
    -- a price feed; without it a holding can only report its cost, which is
    -- why current_value falls back to invested_amount rather than reading zero.
    last_price      REAL,
    last_price_at   TEXT NOT NULL DEFAULT '',
    start_date      TEXT NOT NULL DEFAULT '',
    maturity_date   TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active', -- active, matured, closed
    source          TEXT NOT NULL DEFAULT 'manual', -- manual, statement, email
    notes           TEXT NOT NULL DEFAULT '',
    -- Stable identity for re-imports: importing the same FD summary twice must
    -- update the rows, not duplicate them. Empty for hand-entered holdings,
    -- which is why the index below is partial.
    dedupe_key      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_investments_dedupe
    ON investments(dedupe_key) WHERE dedupe_key != '';
CREATE INDEX IF NOT EXISTS idx_investments_maturity ON investments(maturity_date);

-- Individual securities orders, from broker confirmation emails.
--
-- A holding is the running sum of its trades, not a figure written once. Two
-- buys of the same stock months apart have to combine into one position with a
-- blended cost, and only the trade list can do that. It also makes ingest
-- idempotent: dedupe_key is the source email, so re-reading the mailbox
-- re-writes the same rows instead of buying the stock again.
CREATE TABLE IF NOT EXISTS investment_trades (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    investment_id INTEGER NOT NULL REFERENCES investments(id) ON DELETE CASCADE,
    side          TEXT NOT NULL DEFAULT 'buy',   -- buy, sell
    shares        REAL NOT NULL DEFAULT 0,
    price         REAL NOT NULL DEFAULT 0,       -- per share, in the trade's currency
    amount        REAL NOT NULL DEFAULT 0,       -- what actually moved
    currency      TEXT NOT NULL DEFAULT 'INR',
    trade_date    TEXT NOT NULL DEFAULT '',
    order_type    TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL DEFAULT 'email', -- email, manual
    dedupe_key    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_investment_trades_dedupe
    ON investment_trades(dedupe_key) WHERE dedupe_key != '';
CREATE INDEX IF NOT EXISTS idx_investment_trades_holding
    ON investment_trades(investment_id, trade_date);

-- Exchange rates used to bring foreign holdings into the rupee net worth.
--
-- There is no market feed here, so a rate is either something the user typed
-- or something derived from their own bank's forex transactions — a remittance
-- of INR 99,670.15 for USD 1,036.18 states a rate of 96.19 more credibly than
-- any constant this code could hardcode. `source` records which, so a number
-- moving net worth is never anonymous.
CREATE TABLE IF NOT EXISTS fx_rates (
    currency     TEXT PRIMARY KEY,
    inr_per_unit REAL NOT NULL,
    as_of        TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT 'manual', -- manual, derived
    note         TEXT NOT NULL DEFAULT '',
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
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
    -- What kind of movement a type='transfer' row is: 'self' (between own
    -- accounts), 'investment' (own account -> an investment), 'family' (own
    -- account -> a family member). Empty for income/expense rows. A transfer
    -- needs no linked leg: money sent to a family member or into a demat
    -- account has no second transaction stream to pair with.
    transfer_kind   TEXT NOT NULL DEFAULT '',
    -- Who or what the transfer went to ("Mom", "Zerodha"). Free text.
    counterparty    TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_txn_dedupe ON transactions(account_id, dedupe_hash);
CREATE INDEX IF NOT EXISTS idx_txn_date ON transactions(txn_date);
CREATE INDEX IF NOT EXISTS idx_txn_account ON transactions(account_id);
CREATE INDEX IF NOT EXISTS idx_txn_category ON transactions(category_id);

-- One row per AI-classified transaction — the Money counterpart of the
-- `classifications` table Mail keeps for messages, and it works the same way:
-- classification happens once, is persisted with the model that produced it,
-- and the UI reads it from here instead of calling a model on every render.
--
-- It is deliberately a SEPARATE table from transactions rather than columns on
-- it. The model's read of a transaction and the user's own edits are different
-- facts, and keeping them apart is what lets a suggestion be shown, ignored,
-- or re-run without ever destroying what the user typed. `applied` records
-- whether the suggestion was written through to the transaction itself.
CREATE TABLE IF NOT EXISTS transaction_classifications (
    transaction_id INTEGER PRIMARY KEY REFERENCES transactions(id) ON DELETE CASCADE,
    category       TEXT NOT NULL DEFAULT '',  -- must name an existing categories.name
    subcategory    TEXT NOT NULL DEFAULT '',
    merchant       TEXT NOT NULL DEFAULT '',  -- cleaned payee, e.g. "UPI-SWIGGY-123@ybl" -> "Swiggy"
    payment_method TEXT NOT NULL DEFAULT '',  -- UPI, NEFT, IMPS, POS, ATM, card, auto_debit, cash
    nature         TEXT NOT NULL DEFAULT '',  -- expense, income, transfer
    transfer_kind  TEXT NOT NULL DEFAULT '',  -- self, investment, family (only when nature='transfer')
    counterparty   TEXT NOT NULL DEFAULT '',
    is_recurring   INTEGER NOT NULL DEFAULT 0, -- subscription, SIP, EMI
    is_bill        INTEGER NOT NULL DEFAULT 0,
    is_refund      INTEGER NOT NULL DEFAULT 0,
    needs_review   INTEGER NOT NULL DEFAULT 0, -- model was unsure, or named no known category
    confidence     REAL NOT NULL DEFAULT 0,
    summary        TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    classified_at  TEXT NOT NULL DEFAULT (datetime('now')),
    applied        INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_txn_class_review
    ON transaction_classifications(needs_review);

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
    paid_at          TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

-- One statement per card per period (issuers resend the same statement mail,
-- and without that constraint the dashboard grows a fresh "upcoming bill" for
-- each copy) is enforced by idx_bills_statement. It is NOT declared here: a
-- database written before the constraint existed already holds duplicates, and
-- CREATE UNIQUE INDEX would fail outright on it. migrations.go dedupes first,
-- then creates it. See dedupeBills.

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
