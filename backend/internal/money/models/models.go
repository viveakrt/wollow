package models

type Account struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Bank           string  `json:"bank"`
	AccountType    string  `json:"accountType"`
	AccountNumber  string  `json:"accountNumber"`
	Currency       string  `json:"currency"`
	OpeningBalance float64 `json:"openingBalance"`
	CurrentBalance float64 `json:"currentBalance"`
	// CreditLimit is a card's sanctioned limit, learned from spend alerts.
	// Zero means no alert has reported one.
	CreditLimit float64 `json:"creditLimit"`
	IFSC        string  `json:"ifsc"`
	Branch      string  `json:"branch"`
	// Source is how the row got here: manual, email (discovered by alert
	// ingest) or statement.
	Source string `json:"source"`
	// IncludeInNetWorth decides whether this balance counts toward the
	// dashboard's net worth figures. A tracked family account or a closed
	// account stays visible but stops moving the owner's totals.
	IncludeInNetWorth bool   `json:"includeInNetworth"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

// Investment is a deposit or holding: a fixed deposit, PPF/EPF/NPS balance, a
// mutual fund folio, a stock position.
//
// These live outside finance_accounts because the facts that matter are
// different ones — maturity date, rate, units — and because a deposit has no
// transaction stream to derive a balance from. They are assets all the same, so
// the dashboard counts them toward net worth.
type Investment struct {
	ID             int64    `json:"id"`
	AccountID      *int64   `json:"accountId,omitempty"`
	Kind           string   `json:"kind"` // fd, rd, ppf, epf, nps, mutual_fund, stock, bond, gold, other
	Institution    string   `json:"institution"`
	Name           string   `json:"name"`
	Identifier     string   `json:"identifier"`
	Currency       string   `json:"currency"`
	InvestedAmount float64  `json:"investedAmount"`
	CurrentValue   float64  `json:"currentValue"`
	MaturityAmount *float64 `json:"maturityAmount,omitempty"`
	InterestRate   *float64 `json:"interestRate,omitempty"`
	Units          *float64 `json:"units,omitempty"`
	// LastPrice is the most recent per-unit price known for this holding.
	LastPrice   *float64 `json:"lastPrice,omitempty"`
	LastPriceAt string   `json:"lastPriceAt"`
	// Gain and GainPercent are derived from invested vs current value.
	Gain        float64 `json:"gain"`
	GainPercent float64 `json:"gainPercent"`
	// Priced says whether CurrentValue reflects a real price or is just the
	// cost standing in for one. Without it, an unpriced holding's zero gain
	// looks like a measurement rather than an absence.
	Priced       bool   `json:"priced"`
	StartDate    string `json:"startDate"`
	MaturityDate string `json:"maturityDate"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	Notes        string `json:"notes"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// ParsedDeposit is one row of a deposit summary file, before it is matched
// against existing holdings and persisted.
type ParsedDeposit struct {
	Kind           string  `json:"kind"`
	Institution    string  `json:"institution"`
	Name           string  `json:"name"`
	Identifier     string  `json:"identifier"`
	Branch         string  `json:"branch"`
	Currency       string  `json:"currency"`
	InvestedAmount float64 `json:"investedAmount"`
	MaturityAmount float64 `json:"maturityAmount"`
	InterestRate   float64 `json:"interestRate"`
	StartDate      string  `json:"startDate"`
	MaturityDate   string  `json:"maturityDate"`
	// DedupeKey identifies the same deposit across re-imports, so importing an
	// updated summary refreshes the rows instead of duplicating them.
	DedupeKey   string `json:"dedupeKey"`
	IsDuplicate bool   `json:"isDuplicate"`
}

// ParsedDepositSummary is a whole deposit summary file.
type ParsedDepositSummary struct {
	Institution string          `json:"institution"`
	Kind        string          `json:"kind"`
	Deposits    []ParsedDeposit `json:"deposits"`
}

type Category struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Icon      string `json:"icon"`
	Color     string `json:"color"`
	SortOrder int    `json:"sortOrder"`
}

type Transaction struct {
	ID             int64    `json:"id"`
	AccountID      int64    `json:"accountId"`
	AccountName    string   `json:"accountName,omitempty"`
	TxnDate        string   `json:"txnDate"`
	ValueDate      string   `json:"valueDate"`
	Narration      string   `json:"narration"`
	RefNo          string   `json:"refNo"`
	WithdrawalAmt  float64  `json:"withdrawalAmt"`
	DepositAmt     float64  `json:"depositAmt"`
	ClosingBalance *float64 `json:"closingBalance,omitempty"`
	Type           string   `json:"type"`
	CategoryID     *int64   `json:"categoryId,omitempty"`
	CategoryName   string   `json:"categoryName,omitempty"`
	CategoryColor  string   `json:"categoryColor,omitempty"`
	Merchant       string   `json:"merchant"`
	PaymentMethod  string   `json:"paymentMethod"`
	Notes          string   `json:"notes"`
	ImportBatchID  *int64   `json:"importBatchId,omitempty"`
	LinkedTxnID    *int64   `json:"linkedTxnId,omitempty"`
	// TransferKind classifies a type='transfer' row: 'self' between own
	// accounts, 'investment' into a holding, 'family' to a family member.
	// Empty for income/expense rows.
	TransferKind string `json:"transferKind,omitempty"`
	// Counterparty is who/what a transfer went to ("Mom", "Zerodha").
	Counterparty string `json:"counterparty,omitempty"`
	CreatedAt    string `json:"createdAt"`

	// SourceEmail is set when this transaction came from a bank or card alert
	// rather than a statement import, and carries enough to open that message
	// in Mail.
	SourceEmail *SourceEmail `json:"sourceEmail,omitempty"`

	// AI is the model's structured read of this transaction, when one has been
	// run. It is advisory and kept apart from the fields above: those are what
	// the transaction *is*, this is what a model thinks it is.
	AI *TransactionClassification `json:"ai,omitempty"`
}

// TransactionClassification is one AI reading of a transaction — the Money
// counterpart of Mail's per-message Classification.
type TransactionClassification struct {
	Category      string  `json:"category"`
	Subcategory   string  `json:"subcategory"`
	Merchant      string  `json:"merchant"`
	PaymentMethod string  `json:"paymentMethod"`
	Nature        string  `json:"nature"`
	TransferKind  string  `json:"transferKind"`
	Counterparty  string  `json:"counterparty"`
	IsRecurring   bool    `json:"isRecurring"`
	IsBill        bool    `json:"isBill"`
	IsRefund      bool    `json:"isRefund"`
	NeedsReview   bool    `json:"needsReview"`
	Confidence    float64 `json:"confidence"`
	Summary       string  `json:"summary"`
	Model         string  `json:"model"`
	ClassifiedAt  string  `json:"classifiedAt"`
	Applied       bool    `json:"applied"`
}

// SourceEmail addresses a message the way the Mail API does — by mailbox and
// IMAP UID — so the client can link straight to it.
type SourceEmail struct {
	MailAccountID int64  `json:"mailAccountId"`
	UID           uint32 `json:"uid"`
	Subject       string `json:"subject"`
	Sender        string `json:"sender"`
	ReceivedAt    string `json:"receivedAt"`
}

// TransferSuggestion pairs two transactions (from different accounts) that
// look like the same money movement — e.g. a bank debit that matches a
// credit card's payment-received amount within a few days.
type TransferSuggestion struct {
	ID         int64       `json:"id"`
	TxnA       Transaction `json:"txnA"`
	TxnB       Transaction `json:"txnB"`
	Confidence float64     `json:"confidence"`
	Status     string      `json:"status"`
	CreatedAt  string      `json:"createdAt"`
}

type ImportBatch struct {
	ID            int64  `json:"id"`
	AccountID     *int64 `json:"accountId,omitempty"`
	FileName      string `json:"fileName"`
	Bank          string `json:"bank"`
	TotalRows     int    `json:"totalRows"`
	ImportedRows  int    `json:"importedRows"`
	DuplicateRows int    `json:"duplicateRows"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	CreatedAt     string `json:"createdAt"`
}

// ParsedTransaction is an intermediate row produced by a bank statement parser,
// before it is matched against categories / persisted.
type ParsedTransaction struct {
	TxnDate        string  `json:"txnDate"` // YYYY-MM-DD
	ValueDate      string  `json:"valueDate"`
	Narration      string  `json:"narration"`
	RefNo          string  `json:"refNo"`
	WithdrawalAmt  float64 `json:"withdrawalAmt"`
	DepositAmt     float64 `json:"depositAmt"`
	ClosingBalance float64 `json:"closingBalance"`
	Merchant       string  `json:"merchant"`
	PaymentMethod  string  `json:"paymentMethod"`
	Type           string  `json:"type"` // income | expense
	CategoryName   string  `json:"categoryName,omitempty"`
	DedupeHash     string  `json:"-"`
}

// ParsedStatement is the full result of parsing a bank statement file.
type ParsedStatement struct {
	Bank string `json:"bank"`
	// AccountType is what the statement looks like it is for — "bank" for an
	// ordinary savings/current export, "ppf" for a PPF passbook. It seeds the
	// account-type picker in the import preview; the user's choice wins.
	AccountType    string              `json:"accountType"`
	AccountNumber  string              `json:"accountNumber"`
	AccountBranch  string              `json:"accountBranch"`
	IFSC           string              `json:"ifsc"`
	StatementFrom  string              `json:"statementFrom"`
	StatementTo    string              `json:"statementTo"`
	OpeningBalance float64             `json:"openingBalance"`
	ClosingBalance float64             `json:"closingBalance"`
	Transactions   []ParsedTransaction `json:"transactions"`
}

type EmailAccount struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	IMAPHost     string `json:"imapHost"`
	IMAPPort     int    `json:"imapPort"`
	AppPassword  string `json:"-"` // never serialized back to the client
	LastSyncedAt string `json:"lastSyncedAt"`
	LastUID      uint32 `json:"lastUid"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"createdAt"`
}

type Bill struct {
	ID              int64    `json:"id"`
	AccountID       *int64   `json:"accountId,omitempty"`
	Issuer          string   `json:"issuer"`
	CardLast4       string   `json:"cardLast4"`
	StatementPeriod string   `json:"statementPeriod"`
	TotalDue        *float64 `json:"totalDue,omitempty"`
	MinimumDue      *float64 `json:"minimumDue,omitempty"`
	DueDate         string   `json:"dueDate"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"createdAt"`
}

// ParsedEmailTransaction is what an email-alert parser (HDFC/Axis/etc.)
// extracts from a single email body.
type ParsedEmailTransaction struct {
	TxnDate        string  `json:"txnDate"`
	Amount         float64 `json:"amount"`
	Type           string  `json:"type"` // income | expense
	AccountLast4   string  `json:"accountLast4"`
	Merchant       string  `json:"merchant"`
	PaymentMethod  string  `json:"paymentMethod"`
	RefNo          string  `json:"refNo"`
	Narration      string  `json:"narration"`
	ClosingBalance float64 `json:"closingBalance,omitempty"`
}

// ParsedTrade is a single securities order read out of a broker's
// confirmation email.
//
// It is a trade, not a holding: a position is the running sum of its trades,
// which is the only way an average cost survives buying the same stock across
// several months. Amount is what actually left the account, including whatever
// the broker folded into it, so the position's cost basis matches the money.
type ParsedTrade struct {
	Side string `json:"side"` // buy | sell
	// Symbol is the instrument as the broker names it. INDmoney labels the
	// company name "Ticker", so this is often a full name rather than a
	// symbol; it is kept verbatim because it is what identifies the holding
	// across that broker's mails.
	Symbol     string  `json:"symbol"`
	Shares     float64 `json:"shares"`
	Price      float64 `json:"price"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	OrderType  string  `json:"orderType"`
	AccountRef string  `json:"accountRef"`
	TradeDate  string  `json:"tradeDate"`
	Broker     string  `json:"broker"`
	// Kind is the account_type-style class this instrument belongs to, e.g.
	// us_stock. It decides which tab the holding appears under.
	Kind string `json:"kind"`
	// Identifier is a stable instrument id — an ISIN for equity, a folio
	// number for a mutual fund — when the source states one. It is preferred
	// over Symbol for matching a trade to its holding, because a broker's own
	// mails don't always spell the same instrument name the same way twice
	// ("ASML Holding N.V." vs "Asml Holding Nv") while the ISIN never varies.
	Identifier string `json:"identifier"`
}

// InvestmentTrade is a stored trade, joined to the position it belongs to.
type InvestmentTrade struct {
	ID           int64   `json:"id"`
	InvestmentID int64   `json:"investmentId"`
	Side         string  `json:"side"`
	Shares       float64 `json:"shares"`
	Price        float64 `json:"price"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	TradeDate    string  `json:"tradeDate"`
	OrderType    string  `json:"orderType"`
	Source       string  `json:"source"`
	CreatedAt    string  `json:"createdAt"`
}

// ParsedBillEmail is what a statement/bill email parser extracts.
type ParsedBillEmail struct {
	Issuer string
	// CardName is the card product the statement is for ("Diners Privilege
	// Credit Card"), read off the subject line. It names the account when the
	// statement doesn't disclose a card number, which is otherwise the case
	// where every card from one issuer collapses into a single row.
	CardName        string
	CardLast4       string
	StatementPeriod string
	TotalDue        *float64
	MinimumDue      *float64
	DueDate         string
}
