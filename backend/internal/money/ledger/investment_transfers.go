package ledger

import "strings"

// Recognising money moving into a broker, on the bank side.
//
// SEBI requires client funds to move through specific rails — a broker's own
// UPI VPA, or a pooled account at a clearing corporation — so a bank's own
// alert almost never says "Zerodha" in a way a human would recognise at a
// glance: "UPI-Indian Clearing Corporation Ltd-icclzr@yespay" is a real ₹10,000
// transfer into a Zerodha trading account, not a payment to a clearing
// corporation. Left as type='expense' this both overstates spending and
// leaves the money's actual destination invisible.
//
// investmentBrokerPatterns are the substrings — matched case-insensitively
// against merchant and narration — known to identify funds moving to or from
// a specific broker. Each is scoped tightly enough to be unambiguous: "icclzr"
// is the VPA suffix Zerodha's UPI collection specifically uses (not a bare
// "indian clearing corporation" match, which other brokers can legitimately
// share), so a false positive here would require another institution to reuse
// the exact same VPA fragment.
var investmentBrokerPatterns = []struct {
	pattern string
	broker  string
}{
	{"zerodha", "Zerodha"},
	{"icclzr", "Zerodha"},
}

// DetectInvestmentBroker reports whether a transaction's merchant or
// narration identifies it as money moving to or from a specific broker, so
// the caller can record it as a transfer instead of spending.
func DetectInvestmentBroker(merchant, narration string) (broker string, ok bool) {
	haystack := strings.ToLower(merchant + " " + narration)
	for _, p := range investmentBrokerPatterns {
		if strings.Contains(haystack, p.pattern) {
			return p.broker, true
		}
	}
	return "", false
}
