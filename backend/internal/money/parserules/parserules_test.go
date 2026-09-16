package parserules

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// mark names a value to select in a sample: the field, the text, and which
// occurrence of that text when it appears more than once.
type mark struct {
	name       string
	value      string
	occurrence int
}

// spansFor locates each mark in the text and returns it as the editor would:
// rune offsets plus the selected text.
func spansFor(t *testing.T, text string, marks []mark) []Span {
	t.Helper()
	var out []Span
	for _, m := range marks {
		occ := m.occurrence
		if occ == 0 {
			occ = 1
		}
		byteAt := -1
		from := 0
		for i := 0; i < occ; i++ {
			idx := strings.Index(text[from:], m.value)
			if idx == -1 {
				t.Fatalf("%s: occurrence %d of %q not found", m.name, occ, m.value)
			}
			byteAt = from + idx
			from = byteAt + len(m.value)
		}
		start := utf8.RuneCountInString(text[:byteAt])
		out = append(out, Span{Name: m.name, Start: start, End: start + utf8.RuneCountInString(m.value), Sample: m.value})
	}
	return out
}

// derived builds a compiled rule from marks on a sample.
func derived(t *testing.T, kind Kind, subject, body string, marks []mark, mutate func(*Rule)) (*Compiled, string) {
	t.Helper()
	text := SampleText(subject, body)
	fields, err := Derive(text, spansFor(t, text, marks))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	r := &Rule{Name: "test", Kind: kind, Enabled: true, SenderDomain: "example.com", AccountType: "bank", Fields: fields}
	if mutate != nil {
		mutate(r)
	}
	c, err := Compile(r)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := c.Apply(text); err != nil {
		t.Fatalf("rule does not read its own sample: %v", err)
	}
	return c, text
}

func TestNormalizeTextIsIdempotentAndTamesSpaces(t *testing.T) {
	in := "Dear Customer,\r\n\r\n  Rs.1234.00\tis   debited ​\r\n\r\n"
	want := "Dear Customer,\nRs.1234.00 is debited"
	if got := NormalizeText(in); got != want {
		t.Fatalf("NormalizeText = %q, want %q", got, want)
	}
	if got := NormalizeText(want); got != want {
		t.Fatalf("not idempotent: %q", got)
	}
}

// The HDFC UPI debit: a rule derived from one alert must read the next one,
// whose amount, payee, date and reference all differ.
func TestTransactionRuleReadsSiblingAlert(t *testing.T) {
	subject := "❗ You have done a UPI txn. Check details!"
	sample := "Dear Customer,\nRs.1234.00 is debited from your account ending 4125 towards VPA swiggy@ybl (SWIGGY LIMITED) on 12-08-26.\n" +
		"UPI transaction reference no.: 123456789012.\nIf you did not authorize this transaction, click here to modify or unsubscribe from Insta Alerts."
	sibling := "Dear Customer,\nRs.500.00 is debited from your account ending 4125 towards VPA zomato@paytm (ZOMATO) on 15-08-26.\n" +
		"UPI transaction reference no.: 987654321098.\nIf you did not authorize this transaction, click here to modify or unsubscribe from Insta Alerts."

	c, _ := derived(t, KindTransaction, subject, sample, []mark{
		{"amount", "1234.00", 0},
		{"account_last4", "4125", 0},
		{"counterparty", "SWIGGY LIMITED", 0},
		{"date", "12-08-26", 0},
		{"reference", "123456789012", 0},
	}, nil)

	ext, err := c.Apply(SampleText(subject, sibling))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := ext.Amount("amount"); got != 500 {
		t.Errorf("amount = %v, want 500", got)
	}
	if got := ext.Text("account_last4"); got != "4125" {
		t.Errorf("account_last4 = %q", got)
	}
	if got := ext.Text("counterparty"); got != "ZOMATO" {
		t.Errorf("counterparty = %q, want ZOMATO", got)
	}
	if got := ext.Date("date"); got != "2026-08-15" {
		t.Errorf("date = %q, want 2026-08-15", got)
	}
	if got := ext.Text("reference"); got != "987654321098" {
		t.Errorf("reference = %q", got)
	}

	txn := ToTransaction(c.Rule, ext, SampleText(subject, sibling), "2026-08-16")
	if txn.Type != "expense" || txn.Merchant != "ZOMATO" || txn.PaymentMethod != "UPI" || txn.TxnDate != "2026-08-15" {
		t.Errorf("ToTransaction = %+v", txn)
	}
}

// Axis puts the amount in the subject, written with a non-breaking space and
// no decimals, and repeats it in the body next to two other figures. The
// subject's copy must be the one read, and a value that appears several times
// must be told apart by the words around it.
func TestValueMarkedInSubjectWithNBSP(t *testing.T) {
	subject := "INR 1018 spent on credit card no. XX5792"
	sample := "Transaction Amount: INR 1018\nMerchant Name: SOME MERCHANT\nAxis Bank Credit Card No. XX5792\n" +
		"Date & Time: 01-01-2026, 12:00:00 IST\nAvailable Limit*: INR 50,000.00\nTotal Credit Limit*: INR 1,00,000.00"
	c, _ := derived(t, KindTransaction, subject, sample, []mark{
		{"amount", "1018", 0}, // first occurrence: the subject line
		{"account_last4", "XX5792", 0},
		{"counterparty", "SOME MERCHANT", 0},
		{"date", "01-01-2026", 0},
		{"available_limit", "50,000.00", 0},
		{"total_limit", "1,00,000.00", 0},
	}, func(r *Rule) { r.AccountType = "credit_card" })

	siblingSubject := "INR 250 spent on credit card no. XX5792"
	sibling := "Transaction Amount: INR 250\nMerchant Name: BIG BAZAAR\nAxis Bank Credit Card No. XX5792\n" +
		"Date & Time: 03-02-2026, 09:30:00 IST\nAvailable Limit*: INR 49,750.00\nTotal Credit Limit*: INR 1,00,000.00"
	ext, err := c.Apply(SampleText(siblingSubject, sibling))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := ext.Amount("amount"); got != 250 {
		t.Errorf("amount = %v, want 250", got)
	}
	if got := ext.Text("counterparty"); got != "BIG BAZAAR" {
		t.Errorf("counterparty = %q", got)
	}
	if got := ext.Date("date"); got != "2026-02-03" {
		t.Errorf("date = %q", got)
	}
	facts := ToFacts(ext)
	if facts.CreditLimit != 100000 || facts.AvailableLimit != 49750 {
		t.Errorf("limits = %v / %v", facts.CreditLimit, facts.AvailableLimit)
	}
	if txn := ToTransaction(c.Rule, ext, SampleText(siblingSubject, sibling), ""); txn.PaymentMethod != "Credit Card" {
		t.Errorf("payment method = %q", txn.PaymentMethod)
	}
}

// A statement lays its figures out as label / value on separate lines, with
// two "Amount Due" labels. Each must anchor on enough of its own label.
func TestBillRuleAnchorsAcrossLines(t *testing.T) {
	subject := "Your ICICI Bank Credit Card Statement for the period 12-Jul-2026 to 11-Aug-2026"
	sample := "Dear Customer,\nYour credit card ending in 7001 statement is ready.\nTotal Amount Due:\n₹12,345.00\n" +
		"Minimum Amount Due: ₹1,234.00\nPayment due\nby 05 August, 2026\nThank you"
	c, _ := derived(t, KindBill, subject, sample, []mark{
		{"card_last4", "7001", 0},
		{"total_due", "12,345.00", 0},
		{"minimum_due", "1,234.00", 0},
		{"due_date", "05 August, 2026", 0},
	}, nil)

	sibling := "Dear Customer,\nYour credit card ending in 7001 statement is ready.\nTotal Amount Due:\n₹8,000.50\n" +
		"Minimum Amount Due: ₹800.00\nPayment due\nby 05 September, 2026\nThank you"
	ext, err := c.Apply(SampleText(subject, sibling))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	bill := ToBill(c.Rule, ext)
	if bill.TotalDue == nil || *bill.TotalDue != 8000.5 {
		t.Errorf("total due = %v", bill.TotalDue)
	}
	if bill.MinimumDue == nil || *bill.MinimumDue != 800 {
		t.Errorf("minimum due = %v", bill.MinimumDue)
	}
	if bill.DueDate != "2026-09-05" {
		t.Errorf("due date = %q, want 2026-09-05", bill.DueDate)
	}
	if bill.CardLast4 != "7001" {
		t.Errorf("card = %q", bill.CardLast4)
	}
}

func TestBalanceRuleReadsMaskedAccount(t *testing.T) {
	subject := "Balance update for your HDFC Bank account"
	sample := "The available balance in your account ending XX4125 is Rs. INR 2,08,870.09 as of 12-AUG-26."
	c, _ := derived(t, KindBalance, subject, sample, []mark{
		{"balance", "2,08,870.09", 0},
		{"account_last4", "XX4125", 0},
		{"as_of", "12-AUG-26", 0},
	}, nil)

	ext, err := c.Apply(SampleText(subject, "The available balance in your account ending XX4125 is Rs. INR 1,500.00 as of 20-AUG-26."))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	facts := ToFacts(ext)
	if !facts.BalanceKnown || facts.Balance != 1500 || facts.Last4 != "4125" || facts.AsOf != "2026-08-20" {
		t.Errorf("facts = %+v", facts)
	}
}

// A broker's order mail on one line, with the same figure as both amount and
// price: the second occurrence must be anchored on its own label.
func TestTradeRuleDerivesMissingFigures(t *testing.T) {
	subject := "Your BUY order for Apple Inc for $245.73 is successful"
	sample := "Ticker: Apple Inc Amount: $245.73 Price: $245.73 Shares: 1 Order Type: Market US a/c: 12AB34"
	c, _ := derived(t, KindTrade, subject, sample, []mark{
		{"symbol", "Apple Inc", 2}, // the body's copy, after the subject's
		{"amount", "245.73", 2},
		{"price", "245.73", 3},
		{"units", "1", 0},
		{"side_word", "BUY", 0},
	}, func(r *Rule) {
		r.Attributes = Attributes{Currency: "USD", InstrumentKind: "us_stock", Broker: "INDmoney"}
	})

	ext, err := c.Apply(SampleText("Your SELL order for Tesla Inc for $100.50 is successful",
		"Ticker: Tesla Inc Amount: $100.50 Price: $50.25 Shares: 2 Order Type: Market US a/c: 12AB34"))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	trade := ToTrade(c.Rule, ext, "2026-08-16")
	if trade.Side != "sell" || trade.Symbol != "Tesla Inc" || trade.Amount != 100.5 || trade.Price != 50.25 || trade.Shares != 2 {
		t.Errorf("trade = %+v", trade)
	}
	if trade.Currency != "USD" || trade.Broker != "INDmoney" || trade.Kind != "us_stock" || trade.TradeDate != "2026-08-16" {
		t.Errorf("trade attributes = %+v", trade)
	}
}

func TestDirectionWordOverridesRuleDirection(t *testing.T) {
	subject := "Paytm wallet update"
	c, _ := derived(t, KindTransaction, subject, "Rs 200 has been credited to your Paytm wallet.", []mark{
		{"amount", "200", 0},
		{"direction_word", "credited", 0},
	}, func(r *Rule) {
		r.AccountID = 7 // bound to the wallet, so no digits are needed
		r.Attributes.Direction = "expense"
	})

	for _, tc := range []struct{ body, want string }{
		{"Rs 200 has been credited to your Paytm wallet.", "income"},
		{"Rs 300 has been debited from your Paytm wallet.", "expense"},
	} {
		text := SampleText(subject, tc.body)
		ext, err := c.Apply(text)
		if err != nil {
			t.Fatalf("%q: %v", tc.body, err)
		}
		if got := ToTransaction(c.Rule, ext, text, "").Type; got != tc.want {
			t.Errorf("%q: type = %q, want %q", tc.body, got, tc.want)
		}
	}
}

func TestRequiredValuesMissingIsAnError(t *testing.T) {
	subject := "Alert"
	text := SampleText(subject, "Rs 200 debited from account ending 1234.")
	fields, err := Derive(text, spansFor(t, text, []mark{{"amount", "200", 0}}))
	if err != nil {
		t.Fatal(err)
	}
	unbound := &Rule{Name: "r", Kind: KindTransaction, SenderDomain: "x.com", Fields: fields}
	c, err := Compile(unbound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(text); err == nil {
		t.Error("unbound rule without account digits must not apply")
	}

	bound := &Rule{Name: "r", Kind: KindTransaction, SenderDomain: "x.com", Fields: fields, AccountID: 3}
	c, err = Compile(bound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(text); err != nil {
		t.Errorf("bound rule should apply: %v", err)
	}
	if _, err := c.Apply(SampleText(subject, "Nothing to see here")); err == nil {
		t.Error("a message with no amount must not apply")
	}
}

// The words around a value are the user's text and must never be read as a
// pattern, whatever they contain.
func TestAnchorsAreLiteral(t *testing.T) {
	subject := "Weird"
	body := "Total (a+)+$ 500 due [now]."
	c, text := derived(t, KindTransaction, subject, body, []mark{{"amount", "500", 0}},
		func(r *Rule) { r.AccountID = 1 })
	ext, err := c.Apply(text)
	if err != nil {
		t.Fatal(err)
	}
	if ext.Amount("amount") != 500 {
		t.Errorf("amount = %v", ext.Amount("amount"))
	}
	if _, err := c.Apply(SampleText(subject, "Total aaa 500 due")); err == nil {
		t.Error("the anchor matched as a regex rather than literally")
	}
}

func TestSelectionMustMatchTheText(t *testing.T) {
	text := SampleText("Alert", "Rs 200 debited")
	_, err := Derive(text, []Span{{Name: "amount", Start: 0, End: 5, Sample: "wrong"}})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("err = %v, want a selection mismatch", err)
	}
	if _, err := Derive(text, []Span{{Name: "nonsense", Start: 0, End: 5}}); err == nil {
		t.Error("unknown field accepted")
	}
	if _, err := Derive(text, []Span{{Name: "amount", Start: 3, End: 999}}); err == nil {
		t.Error("out-of-range span accepted")
	}
}

func TestMatchesSender(t *testing.T) {
	c, _ := derived(t, KindBalance, "Balance", "Balance: Rs 10 in a/c 1234", []mark{
		{"balance", "10", 0}, {"account_last4", "1234", 0},
	}, func(r *Rule) {
		r.SenderDomain = "hdfcbank.net"
		r.SubjectContains = "balance"
	})
	cases := []struct {
		from, subject string
		want          bool
	}{
		{"alerts@hdfcbank.net", "Balance update", true},
		{"alerts@mail.hdfcbank.net", "balance", true},
		{"alerts@nothdfcbank.net", "Balance update", false},
		{"alerts@hdfcbank.net", "Your statement", false},
	}
	for _, tc := range cases {
		if got := c.Matches(tc.from, "", tc.subject, ""); got != tc.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", tc.from, tc.subject, got, tc.want)
		}
	}

	c.Rule.SenderEmail = "exact@hdfcbank.net"
	if c.Matches("alerts@hdfcbank.net", "", "balance", "") {
		t.Error("an exact sender must exclude other addresses on the domain")
	}
	if !c.Matches("Exact@HDFCBank.net", "", "balance", "") {
		t.Error("exact sender comparison must ignore case")
	}
}

func TestDeriveRejectsOverlapsAndDuplicates(t *testing.T) {
	text := SampleText("Alert", "Rs 200 debited from 1234")
	spans := spansFor(t, text, []mark{{"amount", "200", 0}, {"closing_balance", "200", 0}})
	if _, err := Derive(text, spans); err == nil {
		t.Error("overlapping spans accepted")
	}
	spans = spansFor(t, text, []mark{{"amount", "200", 0}, {"amount", "200", 0}})
	if _, err := Derive(text, spans); err == nil {
		t.Error("duplicate field accepted")
	}
}
