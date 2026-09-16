package parserules

import (
	"strings"

	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/money/models"
)

// AccountFacts is what a message says about the account itself, as opposed
// to about a movement on it: a balance the bank stated, a card's limits.
type AccountFacts struct {
	Last4        string
	Balance      float64
	BalanceKnown bool
	// AsOf is the date the balance was stated for, or "" when the mail didn't
	// say (callers fall back to the message's own date).
	AsOf           string
	CreditLimit    float64
	AvailableLimit float64
	AvailableKnown bool
}

// ToFacts reads the balance and limit values a rule of any kind may carry.
func ToFacts(ext Extraction) AccountFacts {
	f := AccountFacts{Last4: firstNonEmpty(ext.Text("account_last4"), ext.Text("card_last4"))}
	if _, ok := ext["balance"]; ok {
		f.Balance = ext.Amount("balance")
		f.BalanceKnown = true
	}
	f.AsOf = ext.Date("as_of")
	f.CreditLimit = ext.Amount("total_limit")
	if _, ok := ext["available_limit"]; ok {
		f.AvailableLimit = ext.Amount("available_limit")
		f.AvailableKnown = true
	}
	return f
}

// directionWords maps the verb a rule may extract to the direction money
// moved, for senders that use one template for both debits and credits.
var directionWords = map[string]string{
	"debited": "expense", "debit": "expense", "spent": "expense", "withdrawn": "expense",
	"deducted": "expense", "paid": "expense", "charged": "expense", "sent": "expense",
	"credited": "income", "credit": "income", "received": "income", "deposited": "income",
	"added": "income", "refunded": "income", "refund": "income",
}

var sideWords = map[string]string{
	"buy": "buy", "bought": "buy", "purchase": "buy", "purchased": "buy", "invested": "buy",
	"sell": "sell", "sold": "sell", "redeemed": "sell", "redemption": "sell",
}

// ToTransaction builds the ledger's view of a transaction rule's match. text
// is the message text the values came from, used for what the rule didn't
// extract explicitly (the payment rail, a fallback description).
func ToTransaction(r *Rule, ext Extraction, text, emailDate string) *models.ParsedEmailTransaction {
	t := &models.ParsedEmailTransaction{
		Amount:         ext.Amount("amount"),
		AccountLast4:   ext.Text("account_last4"),
		Merchant:       ext.Text("counterparty"),
		RefNo:          ext.Text("reference"),
		ClosingBalance: ext.Amount("closing_balance"),
		TxnDate:        ext.Date("date"),
		PaymentMethod:  r.Attributes.PaymentMethod,
	}
	t.Type = "expense"
	if d, ok := directionWords[strings.ToLower(ext.Text("direction_word"))]; ok {
		t.Type = d
	} else if r.Attributes.Direction == "income" {
		t.Type = "income"
	}
	if t.PaymentMethod == "" {
		t.PaymentMethod = emailparse.DetectPaymentMethod(text)
	}
	if t.PaymentMethod == "" && r.AccountType == "credit_card" {
		t.PaymentMethod = "Credit Card"
	}
	if t.TxnDate == "" {
		t.TxnDate = emailDate
	}
	t.Narration = firstNonEmpty(ext.Text("narration"), t.Merchant, cleanNarration(firstLine(text)))
	return t
}

// ToBill builds a statement reminder from a bill rule's match.
func ToBill(r *Rule, ext Extraction) models.ParsedBillEmail {
	b := models.ParsedBillEmail{
		Issuer:          r.Issuer,
		CardLast4:       ext.Text("card_last4"),
		StatementPeriod: ext.Text("statement_period"),
	}
	// Stored normalized where readable so the dashboard can sort by it; the
	// raw text otherwise, which is still more useful than nothing.
	if raw := ext.Text("due_date"); raw != "" {
		b.DueDate = firstNonEmpty(emailparse.ParseAlertDate(raw), raw)
	}
	if _, ok := ext["total_due"]; ok {
		v := ext.Amount("total_due")
		b.TotalDue = &v
	}
	if _, ok := ext["minimum_due"]; ok {
		v := ext.Amount("minimum_due")
		b.MinimumDue = &v
	}
	return b
}

// ToTrade builds a broker order from a trade rule's match. Whichever of
// amount, units and price the mail omits is derived from the other two.
func ToTrade(r *Rule, ext Extraction, emailDate string) *models.ParsedTrade {
	t := &models.ParsedTrade{
		Symbol:     ext.Text("symbol"),
		Identifier: ext.Text("identifier"),
		Shares:     ext.Number("units"),
		Price:      ext.Number("price"),
		Amount:     ext.Amount("amount"),
		Currency:   firstNonEmpty(r.Attributes.Currency, "INR"),
		AccountRef: ext.Text("account_ref"),
		TradeDate:  firstNonEmpty(ext.Date("date"), emailDate),
		Broker:     firstNonEmpty(r.Attributes.Broker, r.Issuer, r.SenderDomain),
		Kind:       firstNonEmpty(r.Attributes.InstrumentKind, "stock"),
	}
	if t.Symbol == "" {
		t.Symbol = t.Identifier
	}
	t.Side = "buy"
	if s, ok := sideWords[strings.ToLower(ext.Text("side_word"))]; ok {
		t.Side = s
	} else if r.Attributes.Side == "sell" {
		t.Side = "sell"
	}
	switch {
	case t.Amount == 0 && t.Shares > 0 && t.Price > 0:
		t.Amount = t.Shares * t.Price
	case t.Price == 0 && t.Shares > 0 && t.Amount > 0:
		t.Price = t.Amount / t.Shares
	case t.Shares == 0 && t.Price > 0 && t.Amount > 0:
		t.Shares = t.Amount / t.Price
	}
	return t
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i != -1 {
		return text[:i]
	}
	return text
}

// cleanNarration makes a subject line usable as a description: no decorative
// emoji, no greeting, bounded length.
func cleanNarration(raw string) string {
	narration := strings.Join(strings.Fields(raw), " ")
	narration = strings.TrimSpace(strings.TrimLeft(narration, "❗⚠️✅🔔💰📩 "))
	for _, prefix := range []string{"Dear Customer,", "Dear Customer", "Greetings from"} {
		narration = strings.TrimSpace(strings.TrimPrefix(narration, prefix))
	}
	if len(narration) > 180 {
		narration = strings.TrimSpace(narration[:180])
	}
	return narration
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
