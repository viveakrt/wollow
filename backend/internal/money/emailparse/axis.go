package emailparse

import (
	"regexp"
	"strings"
	"time"

	"wollow/backend/internal/money/models"
)

// Axis Bank credit card spend alerts (sender alerts@axis.bank.in). Body is
// plain text (quoted-printable, whitespace-padded) with label:value pairs,
// each on its own line:
//
//	Transaction Amount: INR1018.00
//	Merchant Name: SOME MERCHANT
//	Axis Bank Credit Card No. XX5792
//	Date & Time: 01-01-2026, 12:00:00 IST
//	Available Limit*: INR 50,000.00
//	Total Credit Limit*: INR 1,00,000.00
//
// The subject line "INR <amount> spent on credit card no. XX<last4>" is a
// reliable fallback for amount + card last-4 if the body regex misses.

const AxisSenderDomain = "axis.bank.in"

var (
	axisAmountRe   = regexp.MustCompile(`Transaction Amount:\s*INR\s*([\d,]+\.\d{2})`)
	axisMerchantRe = regexp.MustCompile(`Merchant Name:\s*([^\n]+)`)
	axisCardRe     = regexp.MustCompile(`Credit Card No\.?\s*(?:XX)?(\d{2,6})`)
	axisDateTimeRe = regexp.MustCompile(`Date\s*&\s*Time:\s*([\d]{1,2}-[\d]{1,2}-\d{4}),?\s*(\d{1,2}:\d{2}:\d{2})?`)

	axisSubjectRe = regexp.MustCompile(`INR\s*([\d,]+\.?\d*)\s+spent on credit card no\.?\s*(?:XX)?(\d{2,6})`)
)

// ParseAxisEmail extracts a transaction from an Axis Bank card spend alert.
func ParseAxisEmail(subject, body string) (*models.ParsedEmailTransaction, bool) {
	t := &models.ParsedEmailTransaction{
		Type:          "expense",
		PaymentMethod: "Credit Card",
	}
	found := false

	if m := axisAmountRe.FindStringSubmatch(body); m != nil {
		t.Amount = parseAmountCommas(m[1])
		found = true
	}
	if m := axisMerchantRe.FindStringSubmatch(body); m != nil {
		t.Merchant = strings.TrimSpace(m[1])
	}
	if m := axisCardRe.FindStringSubmatch(body); m != nil {
		t.AccountLast4 = m[1]
	}
	if m := axisDateTimeRe.FindStringSubmatch(body); m != nil {
		if d, err := time.Parse("02-01-2006", m[1]); err == nil {
			t.TxnDate = d.Format("2006-01-02")
		}
	}

	if !found {
		if m := axisSubjectRe.FindStringSubmatch(subject); m != nil {
			t.Amount = parseAmountCommas(m[1])
			t.AccountLast4 = m[2]
			found = true
		}
	}
	if !found {
		return nil, false
	}

	if t.Narration == "" {
		t.Narration = strings.TrimSpace(t.Merchant)
	}
	return t, true
}
