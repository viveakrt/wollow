package emailparse

import (
	"os"
	"path/filepath"
	"testing"
)

func indmoney(t *testing.T) *Institution {
	t.Helper()
	inst := InstitutionForSender("transactions@transactions.indmoney.com")
	if inst == nil {
		t.Fatal("INDmoney is not in the institution registry")
	}
	return inst
}

// The real order confirmation, end to end.
func TestParseTradeEmailFromRealINDmoneyMail(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "statements",
		"BUY order of Take-Two Interactive Software Inc. for $245.73 is successful.eml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("sample not available: %v", err)
	}
	parsed, err := ParseEML(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	trade, ok := ParseTradeEmail(InstitutionForSender(parsed.From), parsed.Subject, parsed.TextBody)
	if !ok {
		t.Fatal("the order confirmation was not read as a trade")
	}

	if trade.Side != "buy" {
		t.Errorf("side = %q, want buy", trade.Side)
	}
	if trade.Symbol != "Take-Two Interactive Software Inc." {
		t.Errorf("symbol = %q, want the full instrument name", trade.Symbol)
	}
	if trade.Amount != 245.73 {
		t.Errorf("amount = %v, want 245.73", trade.Amount)
	}
	if trade.Price != 245 {
		t.Errorf("price = %v, want 245", trade.Price)
	}
	if trade.Shares != 1 {
		t.Errorf("shares = %v, want 1", trade.Shares)
	}
	if trade.Currency != "USD" {
		t.Errorf("currency = %q, want USD", trade.Currency)
	}
	if trade.Kind != "us_stock" {
		t.Errorf("kind = %q, want us_stock", trade.Kind)
	}
	if trade.OrderType != "Limit" {
		t.Errorf("orderType = %q, want Limit", trade.OrderType)
	}
	if trade.AccountRef != "XX002" {
		t.Errorf("accountRef = %q, want XX002", trade.AccountRef)
	}
}

// A trade mail carries an amount and the word "order", which the generic alert
// reader would otherwise book as a bank expense — money that never left any
// tracked account, against a holding that never appears.
func TestTradeEmailIsNotABankTransaction(t *testing.T) {
	const subject = "BUY order of Take-Two Interactive Software Inc. for $245.73 is successful"
	const body = "Your BUY order for Take-Two Interactive Software Inc. is successful. " +
		"Ticker: Take-Two Interactive Software Inc. Amount: $245.73 Price: $245 Shares: 1 " +
		"Order Type: Limit US a/c no. : XX002"

	if !IsTradeEmail(indmoney(t), subject, body) {
		t.Fatal("a broker order confirmation must be recognised as a trade")
	}
}

// Orders that did not complete are not holdings.
func TestCancelledOrdersAreNotTrades(t *testing.T) {
	for _, word := range []string{"cancelled", "rejected", "failed"} {
		subject := "BUY order of Apple Inc. for $200.00 is " + word
		if IsTradeEmail(indmoney(t), subject, "Your order was "+word+".") {
			t.Errorf("%q order was read as a completed trade", word)
		}
	}
}

// Only brokers produce trades; a bank alert with the word "order" must not.
func TestTradeParsingIgnoresNonBrokers(t *testing.T) {
	hdfc := InstitutionForSender("alerts@hdfcbank.net")
	if IsTradeEmail(hdfc, "BUY order successful", "Rs.500 debited") {
		t.Error("a bank alert was read as a securities trade")
	}
}

func TestParseTradeDerivesMissingFigures(t *testing.T) {
	inst := indmoney(t)

	// No shares stated: derived from amount and price.
	trade, ok := ParseTradeEmail(inst, "BUY order of Apple Inc. for $400.00 is successful",
		"Ticker: Apple Inc. Amount: $400.00 Price: $200")
	if !ok {
		t.Fatal("not parsed")
	}
	if trade.Shares != 2 {
		t.Errorf("shares = %v, want 2 derived from amount/price", trade.Shares)
	}

	// No amount stated: derived from shares and price.
	trade, ok = ParseTradeEmail(inst, "BUY order of Apple Inc. is successful",
		"Ticker: Apple Inc. Price: $150 Shares: 3")
	if !ok {
		t.Fatal("not parsed")
	}
	if trade.Amount != 450 {
		t.Errorf("amount = %v, want 450 derived from shares*price", trade.Amount)
	}

	// Neither: there is no cost basis, so it must be refused rather than
	// stored as a holding that reports an infinite gain.
	if _, ok := ParseTradeEmail(inst, "BUY order of Apple Inc. is successful", "Ticker: Apple Inc."); ok {
		t.Error("a trade with no amount and no price was accepted")
	}
}
