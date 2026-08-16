package emailparse

import (
	"strings"
	"testing"
)

func TestInstitutionForSenderMatchesSubdomains(t *testing.T) {
	cases := map[string]string{
		"alerts@hdfcbank.bank.in":                "HDFC",
		"emailstatements.cards@hdfcbank.bank.in": "HDFC",
		"no-reply@alerts.icicibank.com":          "ICICI",
		"credit_cards@icici.bank.in":             "ICICI",
		"donotreply@bobcard.co.in":               "BOBCARD",
		"no-reply@amazonpay.in":                  "AmazonPay",
		"noreply@reports.zerodha.net":            "Zerodha",
		"someone@example.com":                    "",
		"phish@noticicibank.com":                 "",
		"":                                       "",
	}
	for from, want := range cases {
		if got := IssuerForSender(from); got != want {
			t.Errorf("IssuerForSender(%q) = %q, want %q", from, got, want)
		}
	}
}

// A bank sends savings alerts, card alerts and loan reminders from one address,
// so the sender alone can't decide what kind of account an alert is about.
func TestKindForAlert(t *testing.T) {
	hdfc := InstitutionForSender("alerts@hdfcbank.bank.in")
	wallet := InstitutionForSender("no-reply@amazonpay.in")

	cases := []struct {
		name    string
		inst    *Institution
		subject string
		body    string
		want    AccountKind
	}{
		{"bank balance alert", hdfc, "Account update for your HDFC Bank A/c",
			"The available balance in your account ending XX4125 is Rs. INR 100.00", KindBank},
		{"card spend alert", hdfc, "INR 1018 spent on credit card no. XX5792",
			"Available Limit*: INR 99893.98", KindCreditCard},
		{"loan reminder", hdfc, "Your EMI is due", "EMI of Rs.5000 for loan account XX1111", KindLoan},
		{"wallet stays a wallet", wallet, "Credit card reward points added",
			"added to your Amazon Pay balance", KindWallet},
		{"unknown sender", nil, "anything", "anything", KindBank},
	}
	for _, tc := range cases {
		if got := KindForAlert(tc.inst, tc.subject, tc.body); got != tc.want {
			t.Errorf("%s: KindForAlert = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseGenericAlert(t *testing.T) {
	cases := []struct {
		name      string
		subject   string
		body      string
		wantAmt   float64
		wantType  string
		wantLast4 string
	}{
		{
			name:    "SBI style debit",
			subject: "Transaction alert",
			body:    "Dear Customer, Rs.2,450.00 has been debited from your A/c XXXXX3456 on 04-08-26 to VPA merchant@ybl (BLUE DART). Ref no 412233445566.",
			wantAmt: 2450, wantType: "expense", wantLast4: "3456",
		},
		{
			name:    "card spend in the subject only",
			subject: "INR 1018 spent on credit card no. XX5792",
			body:    "Here's the summary of your transaction.",
			wantAmt: 1018, wantType: "expense", wantLast4: "5792",
		},
		{
			name:    "salary credit",
			subject: "Credit alert",
			body:    "INR 75,000.00 is credited to your account ending 4125 on 01 Aug 2026. Available balance: INR 1,20,000.00",
			wantAmt: 75000, wantType: "income", wantLast4: "4125",
		},
		{
			name:    "wallet debit",
			subject: "Payment successful",
			body:    "Rs 199 has been debited from your Paytm Wallet for SPOTIFY.",
			wantAmt: 199, wantType: "expense", wantLast4: "",
		},
		{
			name:    "rupee symbol, no decimals",
			subject: "Money sent",
			body:    "₹500 debited from A/c ending 9012 towards VPA friend@okaxis (RAJ K) on 12-08-2026.",
			wantAmt: 500, wantType: "expense", wantLast4: "9012",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txn, ok := ParseGenericAlert(tc.subject, tc.body)
			if !ok {
				t.Fatal("no transaction parsed")
			}
			if txn.Amount != tc.wantAmt {
				t.Errorf("amount = %.2f, want %.2f", txn.Amount, tc.wantAmt)
			}
			if txn.Type != tc.wantType {
				t.Errorf("type = %q, want %q", txn.Type, tc.wantType)
			}
			if txn.AccountLast4 != tc.wantLast4 {
				t.Errorf("last4 = %q, want %q", txn.AccountLast4, tc.wantLast4)
			}
		})
	}
}

// What the ledger records has to describe the payment, not the email.
//
// Both cases below are taken from real alerts that produced bad rows: a salary
// credit whose merchant became the unsubscribe footer, and a UPI debit whose
// narration became the alert's subject line. A subject like "You have done a
// UPI txn. Check details!" is identical across every such mail, so it tells a
// reader nothing and gives the categorizer nothing.
func TestNarrationDescribesThePaymentNotTheEmail(t *testing.T) {
	t.Run("footer text must not become the merchant", func(t *testing.T) {
		const subject = "❗ New Deposit Alert: Check your A/c balance now!"
		const body = "Dear Customer, Greetings from HDFC Bank!\n" +
			"INR 1,62,262.00 is credited to your account ending XX4125 on 27-FEB-26 by NEFT.\n" +
			"Reference Details: NEFT Cr-ICIC0099999-MANASH E COMMERCE PRIVATE LIMITED.\n" +
			"Please click here to modify or unsubscribe from Insta Alerts.\n"

		txn, ok := ParseGenericAlert(subject, body)
		if !ok {
			t.Fatal("no transaction parsed")
		}
		if strings.Contains(strings.ToLower(txn.Merchant), "unsubscribe") ||
			strings.Contains(strings.ToLower(txn.Merchant), "insta alert") {
			t.Errorf("merchant = %q — footer boilerplate was taken as the payee", txn.Merchant)
		}
		if strings.Contains(strings.ToLower(txn.Narration), "unsubscribe") {
			t.Errorf("narration = %q — footer boilerplate leaked into the ledger", txn.Narration)
		}
		// It must still describe the credit itself.
		if !strings.Contains(txn.Narration, "1,62,262") && !strings.Contains(txn.Narration, "credited") {
			t.Errorf("narration = %q, want the sentence describing the credit", txn.Narration)
		}
		if txn.Type != "income" || txn.Amount != 162262 {
			t.Errorf("got %s %.2f, want income 162262", txn.Type, txn.Amount)
		}
	})

	t.Run("subject is not used when the body describes the payment", func(t *testing.T) {
		const subject = "❗  You have done a UPI txn. Check details!"
		const body = "Dear Customer, Greetings from HDFC Bank!\n" +
			"Rs.17000.00 is debited from your account ending 4125 on 03-06-26.\n" +
			"If you did not authorise this, report it immediately.\n"

		txn, ok := ParseGenericAlert(subject, body)
		if !ok {
			t.Fatal("no transaction parsed")
		}
		if strings.Contains(txn.Narration, "Check details") {
			t.Errorf("narration = %q — that is the email's subject, not the payment", txn.Narration)
		}
		if !strings.Contains(txn.Narration, "17000") {
			t.Errorf("narration = %q, want the sentence stating the debit", txn.Narration)
		}
		if txn.AccountLast4 != "4125" {
			t.Errorf("last4 = %q, want 4125", txn.AccountLast4)
		}
	})

	// With nothing usable in the body, the subject is still better than a
	// blank row — it just must not be the first choice.
	t.Run("subject remains the last resort", func(t *testing.T) {
		txn, ok := ParseGenericAlert("INR 1018 spent on credit card no. XX5792", "")
		if !ok {
			t.Fatal("no transaction parsed")
		}
		if txn.Narration == "" {
			t.Error("narration is empty; an unidentifiable row is worse than the subject")
		}
	})
}

func TestSentenceAround(t *testing.T) {
	const s = "Dear Customer. Rs.5000.00 is debited from your account ending 4125 on 11-08-26. Ref 127736819957."
	got := sentenceAround(s, strings.Index(s, "Rs.5000.00"))
	want := "Rs.5000.00 is debited from your account ending 4125 on 11-08-26."
	if got != want {
		t.Errorf("sentenceAround = %q, want %q", got, want)
	}
}

// A false positive here writes a wrong number into someone's ledger, so mail
// that merely mentions money must not become a transaction.
func TestParseGenericAlertRejectsNonTransactions(t *testing.T) {
	cases := []struct{ name, subject, body string }{
		{"marketing", "Get a credit card today", "Pre-approved offers await. Apply now."},
		{"balance only", "Account update", "The available balance in your account ending XX4125 is Rs. INR 2,08,870.09 as of 12-AUG-26."},
		{"no amount", "Transaction alert", "A transaction was debited from your account."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if IsBalanceOnlyAlert(tc.subject, tc.body) {
				return // correctly classified as a balance, not a transaction
			}
			txn, ok := ParseGenericAlert(tc.subject, tc.body)
			if ok && txn.Amount > 0 {
				t.Errorf("parsed a transaction of %.2f from non-transactional mail", txn.Amount)
			}
		})
	}
}

// Alerts routinely mention both directions — a debit alert closes with the
// balance "credited" boilerplate — so the direction stated first must win.
func TestParseGenericAlertPrefersTheFirstDirection(t *testing.T) {
	body := "Rs.500.00 is debited from your account ending 4125. " +
		"Your account was last credited with Rs.75,000.00 on 01-08-26."
	txn, ok := ParseGenericAlert("Debit alert", body)
	if !ok {
		t.Fatal("no transaction parsed")
	}
	if txn.Type != "expense" || txn.Amount != 500 {
		t.Errorf("got %s of %.2f, want expense of 500.00", txn.Type, txn.Amount)
	}
}

func TestParseAccountFacts(t *testing.T) {
	facts := ParseAccountFacts(
		"View: Account update for your HDFC Bank A/c",
		"The available balance in your account ending XX4125 is Rs. INR 2,08,870.09 as of 12-AUG-26.")

	if !facts.BalanceKnown || facts.Balance != 208870.09 {
		t.Errorf("balance = %.2f (known=%v), want 208870.09", facts.Balance, facts.BalanceKnown)
	}
	if facts.AccountLast4 != "4125" {
		t.Errorf("last4 = %q, want %q", facts.AccountLast4, "4125")
	}
	if facts.AsOf != "2026-08-12" {
		t.Errorf("asOf = %q, want %q", facts.AsOf, "2026-08-12")
	}
}

// "Available Limit" and "available balance" are worded alike; reading a card's
// remaining limit as its balance would put a fictional asset on the dashboard.
func TestParseAccountFactsSeparatesLimitFromBalance(t *testing.T) {
	facts := ParseAccountFacts(
		"INR 1018 spent on credit card no. XX5792",
		"Transaction Amount: INR 1018\nAvailable Limit*: INR 99893.98\nTotal Credit Limit*: INR 1,00,000.00")

	if facts.BalanceKnown {
		t.Errorf("read a balance of %.2f from a card limit line", facts.Balance)
	}
	if !facts.CreditLimitKnown || facts.CreditLimit != 100000 {
		t.Errorf("creditLimit = %.2f (known=%v), want 100000", facts.CreditLimit, facts.CreditLimitKnown)
	}
	if !facts.AvailableKnown || facts.AvailableLimit != 99893.98 {
		t.Errorf("availableLimit = %.2f, want 99893.98", facts.AvailableLimit)
	}
}

func TestParseAlertDate(t *testing.T) {
	cases := map[string]string{
		"11-08-26":      "2026-08-11",
		"31-JUL-2026":   "2026-07-31",
		"12/08/2026":    "2026-08-12",
		"1 Aug 2026":    "2026-08-01",
		"July 30, 2026": "2026-07-30",
		"not a date":    "",
	}
	for in, want := range cases {
		if got := ParseAlertDate(in); got != want {
			t.Errorf("ParseAlertDate(%q) = %q, want %q", in, got, want)
		}
	}
}

// A card statement is a bill; a broker's holding statement is not.
func TestIsBillEmail(t *testing.T) {
	cases := map[string]bool{
		"Your HDFC Bank - Diners Privilege Credit Card Statement - June-2026": true,
		"E-statement for your BOBCARD credit card ending in 3109":             true,
		"Your monthly holding statement":                                      false,
		"INR 1018 spent on credit card no. XX5792":                            false,
	}
	for subject, want := range cases {
		if got := IsBillEmail(subject); got != want {
			t.Errorf("IsBillEmail(%q) = %v, want %v", subject, got, want)
		}
	}
}

func TestParseCardName(t *testing.T) {
	cases := map[string]string{
		"Your HDFC Bank - Diners Privilege Credit Card Statement - June-2026":                       "HDFC Bank - Diners Privilege Credit Card",
		"Amazon Pay ICICI Bank Credit Card Statement for the period June 13, 2026 to July 12, 2026": "Amazon Pay ICICI Bank Credit Card",
		"Statement": "",
	}
	for subject, want := range cases {
		if got := parseCardName(subject); got != want {
			t.Errorf("parseCardName(%q) = %q, want %q", subject, got, want)
		}
	}
}
