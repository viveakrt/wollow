package ledger

import "testing"

// A bank's own alert almost never spells a broker's name plainly — SEBI
// routes client funds through the broker's UPI VPA or a clearing
// corporation's pooled account — so these are the real shapes production
// data actually takes, not synthetic ones.
func TestDetectInvestmentBrokerMatchesKnownFundingPatterns(t *testing.T) {
	cases := []struct {
		merchant, narration string
		wantBroker          string
	}{
		{"ZERODHA BROKING LIMITED", "UPI-ZERODHA BROKING LIMITED-zerodhabroking.brk@validicici", "Zerodha"},
		{"Indian Clearing Corporation", "UPI-Indian Clearing Corporation Ltd-icclzr@yespay", "Zerodha"},
		{"zerodhamf@hdfcbank", "UPI-zerodhamf@hdfcbank-zerodhamf@hdfcbank", "Zerodha"},
		{"ICCL ZERODHA", "UPI-ICCL ZERODHA-zerodha.iccl3.brk@validhdfc", "Zerodha"},
	}
	for _, tc := range cases {
		broker, ok := DetectInvestmentBroker(tc.merchant, tc.narration)
		if !ok {
			t.Errorf("merchant=%q narration=%q: not detected, want %q", tc.merchant, tc.narration, tc.wantBroker)
			continue
		}
		if broker != tc.wantBroker {
			t.Errorf("merchant=%q: broker=%q, want %q", tc.merchant, broker, tc.wantBroker)
		}
	}
}

// An unrelated payment must not be swept in — a false positive here would
// mislabel real spending as an investment transfer.
func TestDetectInvestmentBrokerIgnoresUnrelatedMerchants(t *testing.T) {
	cases := []struct{ merchant, narration string }{
		{"SWIGGY", "UPI-SWIGGY-swiggy@ybl"},
		{"Indian Railway Catering", "UPI-IRCTC-irctc@sbi"},
		{"RAMESH CHANDRA YADAV", "UPI-RAMESH CHANDRA YADAV-rcyadav28@okhdfcbank"},
	}
	for _, tc := range cases {
		if broker, ok := DetectInvestmentBroker(tc.merchant, tc.narration); ok {
			t.Errorf("merchant=%q: matched broker %q, want no match", tc.merchant, broker)
		}
	}
}
