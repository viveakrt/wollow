package parsers

import (
	"os"
	"path/filepath"
	"testing"

	"wollow/backend/internal/money/models"
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
}

func TestIsZerodhaPnLStatement(t *testing.T) {
	equity := sample(t, "pnl-YPQ985.xlsx")
	if !IsZerodhaPnLStatement(equity) {
		t.Fatal("equity P&L export was not recognized as one")
	}
	hdfc := sample(t, filepath.Join("HDFC", "188507376_FDSummary_12Aug2026.xls"))
	if IsZerodhaPnLStatement(hdfc) {
		t.Fatal("an HDFC .xls export was recognized as a Zerodha P&L .xlsx export")
	}
}

func TestParseZerodhaPnLStatementEquity(t *testing.T) {
	pnl, err := ParseZerodhaPnLStatement(sample(t, "pnl-YPQ985.xlsx"))
	if err != nil {
		t.Fatalf("parsing equity P&L statement: %v", err)
	}
	if pnl.ClientID != "YPQ985" {
		t.Errorf("clientID = %q, want %q", pnl.ClientID, "YPQ985")
	}
	if pnl.Kind != "stock" {
		t.Errorf("kind = %q, want %q", pnl.Kind, "stock")
	}
	if pnl.PeriodFrom != "2022-04-04" || pnl.PeriodTo != "2026-08-19" {
		t.Errorf("period = %s..%s, want 2022-04-04..2026-08-19", pnl.PeriodFrom, pnl.PeriodTo)
	}

	byISIN := map[string]models.ParsedZerodhaHolding{}
	for _, h := range pnl.Holdings {
		byISIN[h.ISIN] = h
		if h.Units <= 0 {
			t.Errorf("holding %s has non-positive units %.4f, a closed position leaked through", h.Symbol, h.Units)
		}
		if h.Kind != "stock" {
			t.Errorf("holding %s kind = %q, want stock", h.Symbol, h.Kind)
		}
	}
	mahabank, ok := byISIN["INE457A01014"]
	if !ok {
		t.Fatal("MAHABANK (open position) not found among parsed holdings")
	}
	if mahabank.Symbol != "MAHABANK" {
		t.Errorf("symbol = %q, want MAHABANK", mahabank.Symbol)
	}
	if mahabank.Units != 1000 {
		t.Errorf("units = %.4f, want 1000", mahabank.Units)
	}
	if mahabank.Price != 79.85 {
		t.Errorf("price = %.4f, want 79.85", mahabank.Price)
	}
	if mahabank.Value != 58800 {
		t.Errorf("value = %.4f, want 58800", mahabank.Value)
	}
	if _, ok := byISIN["INE423A01024"]; ok {
		t.Error("ADANIENT is a fully closed position (Open Quantity 0) and should not appear")
	}
}

func TestParseZerodhaPnLStatementMutualFunds(t *testing.T) {
	pnl, err := ParseZerodhaPnLStatement(sample(t, "pnl-YPQ985 (1).xlsx"))
	if err != nil {
		t.Fatalf("parsing mutual funds P&L statement: %v", err)
	}
	if pnl.Kind != "mutual_fund" {
		t.Errorf("kind = %q, want %q", pnl.Kind, "mutual_fund")
	}
	if len(pnl.Holdings) != 3 {
		t.Fatalf("parsed %d holdings, want 3", len(pnl.Holdings))
	}
	for _, h := range pnl.Holdings {
		if h.Kind != "mutual_fund" {
			t.Errorf("holding %s kind = %q, want mutual_fund", h.Symbol, h.Kind)
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
