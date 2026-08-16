// Package txnclassify runs structured AI classification over stored
// transactions, the Money counterpart of Mail's classifier package.
//
// It follows the same shape deliberately: one model call per record, a
// validated structured result, persisted once with the model that produced it,
// and a background pass that skips failures rather than aborting. What differs
// is the vocabulary — a transaction's categories are the user's own rows in
// `categories`, not a fixed enum, so the valid set is passed in per run and
// anything outside it is flagged for review instead of silently coerced.
package txnclassify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Classification is the model's structured read of one transaction. It is
// advisory: Apply writes only the fields the user has left empty, and never
// changes a transaction's type — see run.go.
type Classification struct {
	Category      string  `json:"category"`
	Subcategory   string  `json:"subcategory"`
	Merchant      string  `json:"merchant"`
	PaymentMethod string  `json:"payment_method"`
	Nature        string  `json:"nature"`
	TransferKind  string  `json:"transfer_kind"`
	Counterparty  string  `json:"counterparty"`
	IsRecurring   bool    `json:"is_recurring"`
	IsBill        bool    `json:"is_bill"`
	IsRefund      bool    `json:"is_refund"`
	NeedsReview   bool    `json:"needs_review"`
	Confidence    float64 `json:"confidence"`
	Summary       string  `json:"summary"`
}

// Txn is the detail handed to the model: everything the bank recorded about
// one transaction. The narration and reference number carry most of the
// signal in Indian bank exports ("UPI-SWIGGY-9871@ybl-PAYMENT"), so they are
// passed whole rather than trimmed to a merchant guess.
type Txn struct {
	ID             int64
	Date           string
	AccountName    string
	AccountType    string
	Direction      string // debit | credit
	Amount         float64
	Narration      string
	RefNo          string
	Merchant       string
	PaymentMethod  string
	Type           string
	ClosingBalance *float64
	// Notes is whatever the user typed against this transaction.
	Notes string
}

var (
	validNatures = map[string]bool{"expense": true, "income": true, "transfer": true}
	validKinds   = map[string]bool{"self": true, "investment": true, "family": true}
	validMethods = map[string]bool{
		"UPI": true, "NEFT": true, "IMPS": true, "RTGS": true, "POS": true,
		"ATM": true, "card": true, "auto_debit": true, "cash": true,
		"net_banking": true, "cheque": true, "wallet": true,
	}
)

// MaxTokens is generous enough for reasoning models, which spend part of the
// budget thinking before answering. A plain model just stops early.
const MaxTokens = 700

const promptTemplate = `You are categorizing one bank transaction for a personal finance app used in India. Respond with a single JSON object and nothing else. No prose, no markdown fences.

Use exactly these fields:
{
  "category": one of the allowed categories listed below, or "" if none fits,
  "subcategory": short free-form label (1-3 words) or "",
  "merchant": the real payee/merchant name cleaned up (e.g. "UPI-SWIGGY-9871@ybl-PAYMENT" -> "Swiggy"), or "" if there is no identifiable merchant,
  "payment_method": one of [UPI, NEFT, IMPS, RTGS, POS, ATM, card, auto_debit, cash, net_banking, cheque, wallet] or "",
  "nature": one of [expense, income, transfer],
  "transfer_kind": one of [self, investment, family] when nature is "transfer", otherwise "",
  "counterparty": for a transfer, who or where the money went (e.g. "Mom", "Zerodha"), otherwise "",
  "is_recurring": true if this looks like a subscription, SIP, EMI or standing instruction,
  "is_bill": true if this is a bill or utility payment,
  "is_refund": true if this is a refund or reversal,
  "needs_review": true if you are unsure and a human should check,
  "confidence": number between 0 and 1,
  "summary": one short sentence (max 15 words) explaining your reading
}

Allowed categories for an expense: %s
Allowed categories for income: %s

Guidance:
- The narration is the bank's own wording for this payment, taken from the statement row or the sentence in the alert that states the amount. It is the strongest evidence you have — read the payee out of it. It is never an email subject line, so treat every word in it as being about the money.
- Indian narrations embed the payee in UPI/NEFT/IMPS strings: "UPI-ZERODHA BROKING LIMITED-zerodhabroking.brk@validicici" is Zerodha, "NEFT Cr-ICIC0099999-MANASH E COMMERCE PRIVATE LIMITED" is a credit from that company. The VPA handle after the name often confirms it (@okhdfcbank, @ybl, @paytm).
- A personal name as the payee (an individual, not a company) usually means money sent to a person rather than a purchase — consider whether it is a transfer to family before calling it an expense.
- "nature" is about what the money DID, not merely its direction. Money moved to the person's own other account, to a broker/mutual fund/demat, or to a family member is a "transfer", not an expense — even though it leaves the account.
- transfer_kind "self" means between the person's own accounts (e.g. a credit card bill payment); "investment" means into a holding (Zerodha, Groww, SIP, NPS, PPF); "family" means money sent to a family member.
- A regular amount arriving on a similar day each month is likely salary; a regular amount leaving is likely rent, an EMI or a subscription.
- Only pick a category from the allowed list above. If nothing fits, return "" and set needs_review to true. Do not invent a category name.

Transaction:
%s`

// BuildPrompt renders the classification prompt for one transaction.
func BuildPrompt(t Txn, expenseCategories, incomeCategories []string) string {
	var detail strings.Builder
	fmt.Fprintf(&detail, "Date: %s\n", t.Date)
	fmt.Fprintf(&detail, "Account: %s (%s)\n", t.AccountName, t.AccountType)
	fmt.Fprintf(&detail, "Direction: %s\n", t.Direction)
	fmt.Fprintf(&detail, "Amount: %.2f INR\n", t.Amount)
	fmt.Fprintf(&detail, "Narration: %s\n", orNone(t.Narration))
	fmt.Fprintf(&detail, "Reference: %s\n", orNone(t.RefNo))
	if t.Merchant != "" {
		fmt.Fprintf(&detail, "Merchant recorded by the importer: %s\n", t.Merchant)
	}
	if t.PaymentMethod != "" {
		fmt.Fprintf(&detail, "Payment method recorded by the importer: %s\n", t.PaymentMethod)
	}
	if t.ClosingBalance != nil {
		fmt.Fprintf(&detail, "Balance after: %.2f\n", *t.ClosingBalance)
	}
	// The user's own words about this transaction outrank anything inferred
	// from the bank's text, so they go last where they are read last.
	if t.Notes != "" {
		fmt.Fprintf(&detail, "The user's own note on this transaction: %s\n", t.Notes)
	}

	return fmt.Sprintf(promptTemplate,
		joinOrNone(expenseCategories), joinOrNone(incomeCategories), detail.String())
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func joinOrNone(v []string) string {
	if len(v) == 0 {
		return "(none defined)"
	}
	return strings.Join(v, ", ")
}

// Parse extracts and validates a Classification from raw model output.
//
// It refuses to invent data: with no JSON object present it returns an error
// so the transaction stays unclassified and is retried on a later pass. A
// category outside the user's own list is dropped and flagged for review
// rather than coerced to a neighbour — a wrong-but-confident category is worse
// than none, because the whole point is that the user can trust what it did
// without re-checking every row.
func Parse(raw string, allowedCategories map[string]string, fallbackNature string) (*Classification, error) {
	jsonText, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}

	var c Classification
	if err := json.Unmarshal([]byte(jsonText), &c); err != nil {
		return nil, fmt.Errorf("classification: invalid JSON: %w", err)
	}

	// Nature is settled first: whether a missing category is a problem depends
	// on it. A transfer has no spending category by design, so demanding one
	// would flag every correctly-read transfer for review and make the review
	// queue noise.
	c.Nature = strings.ToLower(strings.TrimSpace(c.Nature))
	if !validNatures[c.Nature] {
		c.Nature = fallbackNature
	}

	// Category is matched case-insensitively against the user's own rows, and
	// the stored value is the canonical name so lookups later are exact.
	if canonical, ok := allowedCategories[strings.ToLower(strings.TrimSpace(c.Category))]; ok {
		c.Category = canonical
	} else {
		c.Category = ""
		if c.Nature != "transfer" {
			c.NeedsReview = true
		}
	}

	c.TransferKind = strings.ToLower(strings.TrimSpace(c.TransferKind))
	switch {
	case c.Nature != "transfer":
		// A kind on a non-transfer is contradictory; the nature wins.
		c.TransferKind = ""
		c.Counterparty = strings.TrimSpace(c.Counterparty)
	case !validKinds[c.TransferKind]:
		// It called this a transfer but not what kind — "self" is the safe
		// reading and the only one that needs no counterparty.
		c.TransferKind = "self"
	}

	if !validMethods[c.PaymentMethod] {
		// Case drift ("upi") is common; only give up if it isn't a known one.
		matched := ""
		for method := range validMethods {
			if strings.EqualFold(method, c.PaymentMethod) {
				matched = method
				break
			}
		}
		c.PaymentMethod = matched
	}

	c.Subcategory = strings.TrimSpace(c.Subcategory)
	c.Merchant = strings.TrimSpace(c.Merchant)
	c.Counterparty = strings.TrimSpace(c.Counterparty)
	c.Summary = strings.TrimSpace(c.Summary)

	switch {
	case c.Confidence < 0:
		c.Confidence = 0
	case c.Confidence > 1:
		c.Confidence = 1
	}

	return &c, nil
}

// extractJSONObject pulls the first balanced JSON object out of model output,
// stripping markdown fences and any reasoning text around it.
func extractJSONObject(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("classification: empty model response")
	}

	if fence := strings.Index(text, "```"); fence != -1 {
		rest := text[fence+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimPrefix(rest, "JSON")
		if end := strings.Index(rest, "```"); end != -1 {
			rest = rest[:end]
		}
		text = strings.TrimSpace(rest)
	}

	start := strings.Index(text, "{")
	if start == -1 {
		return "", fmt.Errorf("classification: no JSON object in response: %s", truncate(text, 200))
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// skip structural characters inside strings
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return text[start : i+1], nil
			}
		}
	}

	return "", fmt.Errorf("classification: unterminated JSON object: %s", truncate(text[start:], 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
