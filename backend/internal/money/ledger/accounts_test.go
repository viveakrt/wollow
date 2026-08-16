package ledger

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newAccountsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE finance_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT, bank TEXT DEFAULT '', account_type TEXT DEFAULT 'bank',
			account_number TEXT DEFAULT '', currency TEXT DEFAULT 'INR',
			opening_balance REAL DEFAULT 0, current_balance REAL DEFAULT 0,
			source TEXT DEFAULT 'manual', updated_at TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func addAccount(t *testing.T, db *sql.DB, name, bank, kind, number string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO finance_accounts (name, bank, account_type, account_number, source)
		VALUES (?, ?, ?, ?, 'manual')`, name, bank, kind, number)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// Accounts are hand-entered now, so the bank field holds whatever the user
// picked. An alert must still find its account whether they stored the issuer
// code, the display name, or left the field blank — otherwise the mail parses,
// matches nothing, and waits forever for an account that already exists.
func TestMatchAccountToleratesHowTheBankWasTyped(t *testing.T) {
	hint := AccountHint{Issuer: "HDFC", Name: "HDFC Bank", Last4: "4125", Kind: "bank"}

	for _, stored := range []string{"HDFC", "HDFC Bank", "hdfc bank", "hdfc", ""} {
		db := newAccountsDB(t)
		want := addAccount(t, db, "Savings", stored, "bank", "XXXXXXXX4125")
		if got := MatchAccount(db, hint); got != want {
			t.Errorf("bank stored as %q: matched %d, want %d", stored, got, want)
		}
	}
}

// The digits are the identity; a type the user disagrees with must not split
// the account in two.
func TestMatchAccountFindsAccountDespiteDifferentType(t *testing.T) {
	db := newAccountsDB(t)
	want := addAccount(t, db, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")

	got := MatchAccount(db, AccountHint{Issuer: "HDFC", Name: "HDFC Bank", Last4: "4125", Kind: "credit_card"})
	if got != want {
		t.Errorf("matched %d, want %d — same digits and bank is the same account", got, want)
	}

	// And the type the user chose must survive the match.
	var kind string
	db.QueryRow(`SELECT account_type FROM finance_accounts WHERE id = ?`, want).Scan(&kind)
	if kind != "bank" {
		t.Errorf("account_type = %q, want \"bank\" — matching must not rewrite it", kind)
	}
}

// Nothing registered means nothing matched: MatchAccount must never invent.
func TestMatchAccountCreatesNothing(t *testing.T) {
	db := newAccountsDB(t)

	if got := MatchAccount(db, AccountHint{Issuer: "Axis", Name: "Axis Bank", Last4: "5792", Kind: "credit_card"}); got != 0 {
		t.Errorf("matched %d, want 0", got)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM finance_accounts`).Scan(&n)
	if n != 0 {
		t.Errorf("created %d accounts, want 0", n)
	}
}

// Digit-less senders (wallets) match on institution + kind, but an unknown
// issuer must not sweep up every account that happens to have no number.
func TestMatchAccountWithoutDigitsNeedsAnIssuer(t *testing.T) {
	db := newAccountsDB(t)
	wallet := addAccount(t, db, "Amazon Pay", "AmazonPay", "wallet", "")

	if got := MatchAccount(db, AccountHint{Issuer: "AmazonPay", Name: "Amazon Pay", Kind: "wallet"}); got != wallet {
		t.Errorf("matched %d, want %d", got, wallet)
	}
	if got := MatchAccount(db, AccountHint{Kind: "wallet"}); got != 0 {
		t.Errorf("issuerless hint matched %d, want 0", got)
	}
}

func TestCreateApprovedAccountIsManualSourced(t *testing.T) {
	db := newAccountsDB(t)

	id := CreateApprovedAccount(db, AccountHint{Issuer: "HDFC", Name: "HDFC Bank", Last4: "4125", Kind: "bank"})
	if id == 0 {
		t.Fatal("no account created")
	}
	var name, bank, kind, number, source string
	db.QueryRow(`SELECT name, bank, account_type, account_number, source
		FROM finance_accounts WHERE id = ?`, id).Scan(&name, &bank, &kind, &number, &source)

	if source != "manual" {
		t.Errorf("source = %q, want \"manual\" — an approved account's type must not be rewritable by mail", source)
	}
	if number != "XXXXXXXX4125" {
		t.Errorf("account_number = %q, want the masked form statements also use", number)
	}
	if name != "HDFC Bank •• 4125" || bank != "HDFC" || kind != "bank" {
		t.Errorf("account = %q/%q/%q, want \"HDFC Bank •• 4125\"/\"HDFC\"/\"bank\"", name, bank, kind)
	}
}
