package parserules

// Kind is what a rule's email describes, and so what ingest writes when the
// rule matches.
type Kind string

const (
	KindTransaction Kind = "transaction" // a bank, wallet or card movement
	KindBill        Kind = "bill"        // a card statement: amounts due and a due date
	KindBalance     Kind = "balance"     // a balance update, recorded as a snapshot
	KindTrade       Kind = "trade"       // a broker order, recorded against a holding
)

var kinds = map[Kind]bool{KindTransaction: true, KindBill: true, KindBalance: true, KindTrade: true}

func ValidKind(k Kind) bool { return kinds[k] }

// FieldKind decides both how a marked span is tightened to the value inside
// it and what pattern captures that value in other messages.
type FieldKind string

const (
	FieldAmount FieldKind = "amount" // money: digits with Indian grouping and up to two decimals
	FieldNumber FieldKind = "number" // any decimal figure (units, a price)
	FieldLast4  FieldKind = "last4"  // the last four digits of an account or card number
	FieldDate   FieldKind = "date"   // any date shape emailparse.ParseAlertDate reads
	FieldText   FieldKind = "text"   // free text, bounded by what follows it
	FieldWord   FieldKind = "word"   // a single word, e.g. "debited"
)

// Field is one value a rule extracts, anchored by the literal text around it
// in the sample the rule was defined on.
//
// Start/End/Sample record where the value sat in that sample (rune offsets)
// so the editor can show the rule as it was marked; Prefix/Suffix are what
// extraction actually uses.
type Field struct {
	Name string    `json:"name"`
	Kind FieldKind `json:"kind"`
	// Prefix is the text immediately before the value. Empty means the value
	// starts its line.
	Prefix string `json:"prefix"`
	// Suffix is the text immediately after the value, when one is needed to
	// know where it ends. AtLineEnd says instead that the value runs to the
	// end of its line.
	Suffix    string `json:"suffix"`
	AtLineEnd bool   `json:"atLineEnd"`
	// Occurrence is the 1-based match to take when the anchors also match
	// elsewhere in the sample; 0 means the anchors were unique.
	Occurrence int    `json:"occurrence"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Sample     string `json:"sample"`
}

// Span is what the editor sends: a field name and the rune range the user
// selected in the sample text. Sample is the selected text itself, checked
// against the offsets so a client/server disagreement about offsets fails
// loudly instead of anchoring the wrong text.
type Span struct {
	Name   string `json:"name"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Sample string `json:"sample"`
}

// Attributes are the per-rule constants a message doesn't state: whether the
// rule's mail is a debit or a credit, which rail it moved on, and so on.
type Attributes struct {
	Direction      string `json:"direction,omitempty"`      // expense | income
	PaymentMethod  string `json:"paymentMethod,omitempty"`  // UPI, Credit Card, ...
	Currency       string `json:"currency,omitempty"`       // INR unless stated
	Side           string `json:"side,omitempty"`           // buy | sell (trades)
	InstrumentKind string `json:"instrumentKind,omitempty"` // stock | us_stock | mutual_fund
	Broker         string `json:"broker,omitempty"`         // holdings institution, when not the issuer
}

// Sample identifies the message a rule was defined on, and carries its
// normalized text.
type Sample struct {
	MailAccountID int64  `json:"mailAccountId"`
	UID           uint32 `json:"uid"`
	Folder        string `json:"folder"`
	RFCMessageID  string `json:"rfcMessageId"`
	Subject       string `json:"subject"`
	From          string `json:"from"`
	Date          string `json:"date"`
	Text          string `json:"text,omitempty"`
}

// Rule is one user-defined parser, as stored in email_parser_rules.
type Rule struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     Kind   `json:"kind"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
	// Issuer is the institution code the extracted account is matched under
	// (finance_accounts.bank). It may be empty for a sender outside the
	// registry, in which case only a bound account can receive its mail.
	Issuer          string `json:"issuer"`
	SenderDomain    string `json:"senderDomain"`
	SenderEmail     string `json:"senderEmail"`
	SubjectContains string `json:"subjectContains"`
	BodyContains    string `json:"bodyContains"`
	// AccountID binds every match to one account, for senders that never
	// state which account they concern (a wallet has one balance). 0 means
	// the account is found from the extracted last digits.
	AccountID     int64      `json:"accountId"`
	AccountType   string     `json:"accountType"`
	Attributes    Attributes `json:"attributes"`
	Fields        []Field    `json:"fields"`
	Sample        Sample     `json:"sample"`
	MatchCount    int        `json:"matchCount"`
	LastMatchedAt string     `json:"lastMatchedAt"`
	CreatedAt     string     `json:"createdAt"`
	UpdatedAt     string     `json:"updatedAt"`
}

// FieldSpec describes one value a kind of rule can extract. Required marks
// the values a rule cannot do without; a few further requirements depend on
// the rule (an account's digits are not needed when the rule is bound to an
// account) and are enforced by checkRequired.
type FieldSpec struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Kind     FieldKind `json:"kind"`
	Required bool      `json:"required"`
}

var fieldsByKind = map[Kind][]FieldSpec{
	KindTransaction: {
		{"amount", "Amount", FieldAmount, true},
		{"account_last4", "Account / card last digits", FieldLast4, false},
		{"counterparty", "Sender / receiver", FieldText, false},
		{"date", "Date", FieldDate, false},
		{"reference", "Reference number", FieldText, false},
		{"narration", "Description", FieldText, false},
		{"closing_balance", "Balance after", FieldAmount, false},
		{"direction_word", "Debit / credit word", FieldWord, false},
		{"available_limit", "Available limit", FieldAmount, false},
		{"total_limit", "Total credit limit", FieldAmount, false},
	},
	KindBill: {
		{"card_last4", "Card last digits", FieldLast4, false},
		{"statement_period", "Statement period", FieldText, false},
		{"total_due", "Total amount due", FieldAmount, false},
		{"minimum_due", "Minimum amount due", FieldAmount, false},
		{"due_date", "Payment due date", FieldDate, false},
	},
	KindBalance: {
		{"balance", "Balance", FieldAmount, true},
		{"account_last4", "Account / card last digits", FieldLast4, false},
		{"as_of", "Balance as of", FieldDate, false},
		{"available_limit", "Available limit", FieldAmount, false},
		{"total_limit", "Total credit limit", FieldAmount, false},
	},
	KindTrade: {
		{"symbol", "Instrument name / ticker", FieldText, true},
		{"identifier", "ISIN / folio number", FieldText, false},
		{"amount", "Order amount", FieldAmount, false},
		{"units", "Units / shares", FieldNumber, false},
		{"price", "Price per unit", FieldNumber, false},
		{"date", "Trade date", FieldDate, false},
		{"side_word", "Buy / sell word", FieldWord, false},
		{"account_ref", "Broker account reference", FieldText, false},
	},
}

// fieldKinds maps every field name to its kind. Names shared between rule
// kinds ("amount", "date") mean the same thing everywhere.
var fieldKinds = func() map[string]FieldKind {
	out := map[string]FieldKind{}
	for _, specs := range fieldsByKind {
		for _, s := range specs {
			out[s.Name] = s.Kind
		}
	}
	return out
}()

// FieldsForKind lists what a rule of the given kind can extract, required
// values first.
func FieldsForKind(k Kind) []FieldSpec {
	specs := fieldsByKind[k]
	out := make([]FieldSpec, len(specs))
	copy(out, specs)
	return out
}

func kindForName(name string) (FieldKind, bool) {
	k, ok := fieldKinds[name]
	return k, ok
}
