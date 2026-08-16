package ledger

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newFXDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/fx.db")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE fx_rates (currency TEXT PRIMARY KEY, inr_per_unit REAL NOT NULL,
			as_of TEXT DEFAULT '', source TEXT DEFAULT 'manual', note TEXT DEFAULT '',
			updated_at TEXT DEFAULT '');
		CREATE TABLE transactions (id INTEGER PRIMARY KEY AUTOINCREMENT, txn_date TEXT,
			withdrawal_amt REAL DEFAULT 0, deposit_amt REAL DEFAULT 0, narration TEXT DEFAULT '');`); err != nil {
		t.Fatal(err)
	}
	return db
}

// The rate comes from the user's own remittance: INR 99,670.15 sent as
// USD 1,036.18 is a rate of 96.19 — their bank's actual conversion, spread
// included, which is a better answer than a mid-market quote for "what is my
// dollar holding worth to me".
func TestDeriveRateFromTheUsersOwnForexTransaction(t *testing.T) {
	db := newFXDB(t)
	db.Exec(`INSERT INTO transactions (txn_date, withdrawal_amt, narration)
		VALUES ('2026-08-12', 99670.15, 'RFX 110826BTT07660 USD1036.18@96.19')`)

	rate, asOf, note := DeriveRateFromForex(db, "USD")
	if rate < 96.1 || rate > 96.3 {
		t.Errorf("rate = %v, want about 96.19", rate)
	}
	if asOf != "2026-08-12" {
		t.Errorf("asOf = %q", asOf)
	}
	if note == "" {
		t.Error("no note explaining where the rate came from")
	}
}

// A truncated narration ("USD1036" for 1036.18) shifts the answer slightly but
// must still land in the right place rather than being rejected.
func TestDeriveRateToleratesTruncatedNarration(t *testing.T) {
	db := newFXDB(t)
	db.Exec(`INSERT INTO transactions (txn_date, withdrawal_amt, narration)
		VALUES ('2026-08-12', 99670.15, 'RFX 110826BTT07660 USD1036')`)

	rate, _, _ := DeriveRateFromForex(db, "USD")
	if rate < 95 || rate > 98 {
		t.Errorf("rate = %v, want about 96", rate)
	}
}

// A misread that grabbed the wrong number would silently rescale net worth, so
// implausible rates are refused rather than stored.
func TestDeriveRateRejectsImplausibleResults(t *testing.T) {
	db := newFXDB(t)
	db.Exec(`INSERT INTO transactions (txn_date, withdrawal_amt, narration)
		VALUES ('2026-08-12', 500, 'Paid USD100000 reference')`)

	if rate, _, _ := DeriveRateFromForex(db, "USD"); rate != 0 {
		t.Errorf("rate = %v, want it refused", rate)
	}
}

// A rate the user typed is authoritative and must never be replaced by a
// derived one.
func TestEnsureRateKeepsWhatTheUserSet(t *testing.T) {
	db := newFXDB(t)
	db.Exec(`INSERT INTO transactions (txn_date, withdrawal_amt, narration)
		VALUES ('2026-08-12', 99670.15, 'RFX USD1036.18@96.19')`)
	if err := SetRate(db, "USD", 90, "2026-08-01", "manual", "my rate"); err != nil {
		t.Fatal(err)
	}

	if got := EnsureRate(db, "USD"); got != 90 {
		t.Errorf("rate = %v, want the user's 90", got)
	}
}

// Without any evidence there is no rate — and no invented one.
func TestNoRateWithoutEvidence(t *testing.T) {
	db := newFXDB(t)
	if got := EnsureRate(db, "USD"); got != 0 {
		t.Errorf("rate = %v, want 0 — nothing should be invented", got)
	}
	if got := RateToINR(db, "INR"); got != 1 {
		t.Errorf("INR rate = %v, want 1", got)
	}
}
