package parsers

import (
	"os"
	"path/filepath"
	"testing"
)

// sample resolves a file in the repo's statements/ folder, four levels up from
// internal/money/parsers.
func sample(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "statements", name)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("sample %s not available: %v", name, err)
	}
	return path
}

func TestParseHDFCStatement(t *testing.T) {
	statement, err := ParseHDFCStatement(sample(t, filepath.Join("HDFC", "Acct_Statement_XXXXXXXX4125_13082026.xls")))
	if err != nil {
		t.Fatalf("parsing account statement: %v", err)
	}
	if statement.AccountNumber == "" {
		t.Error("no account number read from the metadata block")
	}
	if len(statement.Transactions) == 0 {
		t.Fatal("no transactions parsed")
	}
	if statement.AccountType != "bank" {
		t.Errorf("accountType = %q, want %q", statement.AccountType, "bank")
	}
	for _, txn := range statement.Transactions {
		if len(txn.TxnDate) != 10 {
			t.Errorf("transaction date %q is not YYYY-MM-DD", txn.TxnDate)
			break
		}
	}
}

// A PPF passbook comes through the same template as a savings statement, so
// only the contents distinguish them. Calling it a bank account would file a
// locked long-term asset as spendable cash.
func TestParseHDFCStatementDetectsPPF(t *testing.T) {
	statement, err := ParseHDFCStatement(sample(t, filepath.Join("HDFC", "PPF Statement_XXXXXXXX0966_12082026.xls")))
	if err != nil {
		t.Fatalf("parsing PPF statement: %v", err)
	}
	if statement.AccountType != "ppf" {
		t.Errorf("accountType = %q, want %q", statement.AccountType, "ppf")
	}
	if len(statement.Transactions) == 0 {
		t.Fatal("no transactions parsed from the PPF passbook")
	}
	for _, txn := range statement.Transactions {
		if txn.WithdrawalAmt != 0 {
			t.Errorf("PPF passbook should have no withdrawals, saw %.2f", txn.WithdrawalAmt)
		}
	}
}

func TestParseDepositSummary(t *testing.T) {
	path := sample(t, filepath.Join("HDFC", "188507376_FDSummary_12Aug2026.xls"))

	if !IsDepositSummary(path) {
		t.Fatal("FD summary was not recognized as one, so it would be routed to the statement parser")
	}

	summary, err := ParseDepositSummary(path, "HDFC")
	if err != nil {
		t.Fatalf("parsing deposit summary: %v", err)
	}
	if len(summary.Deposits) != 2 {
		t.Fatalf("parsed %d deposits, want 2", len(summary.Deposits))
	}

	first := summary.Deposits[0]
	if first.Identifier != "50301125623955" {
		t.Errorf("identifier = %q, want %q", first.Identifier, "50301125623955")
	}
	if first.InvestedAmount != 10000 {
		t.Errorf("principal = %.2f, want 10000", first.InvestedAmount)
	}
	if first.MaturityAmount != 11489 {
		t.Errorf("maturity amount = %.2f, want 11489", first.MaturityAmount)
	}
	if first.InterestRate != 7 {
		t.Errorf("rate = %.2f, want 7", first.InterestRate)
	}
	if first.MaturityDate != "2027-03-10" {
		t.Errorf("maturity date = %q, want %q", first.MaturityDate, "2027-03-10")
	}
	if first.StartDate != "2025-03-10" {
		t.Errorf("start date = %q, want %q", first.StartDate, "2025-03-10")
	}
	if first.DedupeKey == "" {
		t.Error("no dedupe key, so a re-import would duplicate this deposit")
	}

	// The "Total INR" row carries amounts but no account number; reading it as
	// a deposit would double the portfolio.
	for _, d := range summary.Deposits {
		if !isAllDigits(d.Identifier) {
			t.Errorf("non-deposit row leaked through: identifier %q", d.Identifier)
		}
	}
}

// An account statement must not be mistaken for a deposit summary.
func TestIsDepositSummaryRejectsStatements(t *testing.T) {
	path := sample(t, filepath.Join("HDFC", "Acct_Statement_XXXXXXXX4125_13082026.xls"))
	if IsDepositSummary(path) {
		t.Error("an account statement was routed to the deposit parser")
	}
}
