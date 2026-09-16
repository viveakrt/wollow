package emailparse

import (
	"strconv"
	"strings"
	"time"
)

// ParseAmount reads an Indian-formatted figure ("1,00,000.00") into a float.
// Anything unreadable is 0, which every caller treats as "not stated".
func ParseAmount(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// flexDateLayouts are the date shapes alert mail uses: 01-01-26, 01/01/2026,
// 01-JAN-2026, 1 Jan 2026, Jan 1, 2026, 5 August, 2026.
var flexDateLayouts = []string{
	"2006-01-02",
	"02-Jan-06", "02-Jan-2006", "02-01-06", "02-01-2006",
	"02/01/06", "02/01/2006", "02/Jan/2006", "02/Jan/06",
	"02.01.2006", "02.01.06",
	"2 Jan 2006", "02 Jan 2006", "2 January 2006", "02 January 2006",
	"2 Jan, 2006", "02 Jan, 2006", "2 January, 2006", "02 January, 2006",
	"Jan 2, 2006", "January 2, 2006", "Jan 2 2006", "January 2 2006",
}

// ParseAlertDate normalizes any date shape alert mail uses to YYYY-MM-DD,
// returning "" when nothing matched — callers substitute the message's own
// received date rather than storing a half-parsed string, because a
// half-parsed date written into transactions.txn_date sorts nowhere and
// breaks every date-scoped query.
func ParseAlertDate(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ", "))
	s = strings.Join(strings.Fields(s), " ")
	for _, layout := range flexDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

var paymentMethodWords = []struct {
	word   string
	method string
}{
	{"upi", "UPI"},
	{"neft", "NEFT"},
	{"imps", "IMPS"},
	{"rtgs", "RTGS"},
	{"credit card", "Credit Card"},
	{"debit card", "Debit Card"},
	{"atm", "ATM"},
	{"net banking", "NetBanking"},
	{"netbanking", "NetBanking"},
	{"wallet", "Wallet"},
	{"auto debit", "Auto Debit"},
	{"standing instruction", "SI"},
	{"nach", "NACH"},
	{"ecs", "ECS"},
	{"cheque", "Cheque"},
}

// DetectPaymentMethod names the rail an alert mentions, or "" when it names
// none. It only reads the opening of the text: alert mail puts its substance
// first and the rest is boilerplate ("apply for a credit card today").
func DetectPaymentMethod(text string) string {
	const scanLimit = 1500
	if len(text) > scanLimit {
		text = text[:scanLimit]
	}
	lower := strings.ToLower(text)
	for _, pm := range paymentMethodWords {
		if strings.Contains(lower, pm.word) {
			return pm.method
		}
	}
	return ""
}
