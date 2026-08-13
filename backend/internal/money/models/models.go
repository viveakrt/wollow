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
	IFSC           string  `json:"ifsc"`
	Branch         string  `json:"branch"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
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
	CreatedAt      string   `json:"createdAt"`
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
	Bank           string              `json:"bank"`
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

// ParsedBillEmail is what a statement/bill email parser extracts.
type ParsedBillEmail struct {
	Issuer          string
	CardLast4       string
	StatementPeriod string
	TotalDue        *float64
	MinimumDue      *float64
	DueDate         string
}
