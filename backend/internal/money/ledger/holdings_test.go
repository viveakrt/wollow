package ledger

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"wollow/backend/internal/money/models"
)

func newHoldingsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/h.db")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE investments (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER,
			kind TEXT, institution TEXT, name TEXT, identifier TEXT DEFAULT '', currency TEXT,
			invested_amount REAL DEFAULT 0, current_value REAL DEFAULT 0, maturity_amount REAL,
			interest_rate REAL, units REAL, last_price REAL, last_price_at TEXT DEFAULT '',
			start_date TEXT DEFAULT '', maturity_date TEXT DEFAULT '', status TEXT DEFAULT 'active',
			source TEXT DEFAULT '', notes TEXT DEFAULT '', dedupe_key TEXT DEFAULT '',
			created_at TEXT DEFAULT '', updated_at TEXT DEFAULT '');
		CREATE TABLE investment_trades (id INTEGER PRIMARY KEY AUTOINCREMENT, investment_id INTEGER,
			side TEXT, shares REAL, price REAL, amount REAL, currency TEXT, trade_date TEXT,
			order_type TEXT DEFAULT '', source TEXT DEFAULT '', dedupe_key TEXT DEFAULT '',
			created_at TEXT DEFAULT '');
		CREATE UNIQUE INDEX idx_trades_dedupe ON investment_trades(dedupe_key) WHERE dedupe_key != '';`); err != nil {
		t.Fatal(err)
	}
	return db
}

// A broker spells the same instrument several ways across its own mails —
// "Take-Two Interactive Software Inc." and "Take-Two Interactive Software Inc",
// "ASML Holding N.V." and "Asml Holding Nv". Each spelling opening its own
// position would split one holding's cost basis across two rows.
func TestOneHoldingPerInstrumentDespiteSpellingDrift(t *testing.T) {
	db := newHoldingsDB(t)

	for i, spelling := range []string{
		"Take-Two Interactive Software Inc.",
		"Take-Two Interactive Software Inc",
		"TAKE-TWO INTERACTIVE SOFTWARE INC.",
	} {
		trade := &models.ParsedTrade{
			Side: "buy", Symbol: spelling, Shares: 1, Price: 200, Amount: 200,
			Currency: "USD", Broker: "INDmoney", Kind: "us_stock", TradeDate: "2026-08-0" + string(rune('1'+i)),
		}
		id, err := ResolveHolding(db, trade)
		if err != nil {
			t.Fatalf("%s: %v", spelling, err)
		}
		if _, err := RecordTrade(db, id, trade, spelling); err != nil {
			t.Fatal(err)
		}
	}

	var holdings int
	db.QueryRow(`SELECT COUNT(*) FROM investments`).Scan(&holdings)
	if holdings != 1 {
		t.Errorf("%d holdings created, want 1 — spelling drift split the position", holdings)
	}
	var units, invested float64
	db.QueryRow(`SELECT COALESCE(units,0), invested_amount FROM investments WHERE id = 1`).Scan(&units, &invested)
	if units != 3 || invested != 600 {
		t.Errorf("units=%v invested=%v, want 3 and 600 — all three buys on one position", units, invested)
	}
}

// Genuinely different instruments must stay apart.
func TestDifferentInstrumentsStayApart(t *testing.T) {
	db := newHoldingsDB(t)
	for _, name := range []string{"Arista Networks", "ASML Holding N.V.", "Nvidia Corporation"} {
		trade := &models.ParsedTrade{
			Side: "buy", Symbol: name, Shares: 1, Price: 100, Amount: 100,
			Currency: "USD", Broker: "INDmoney", Kind: "us_stock",
		}
		id, err := ResolveHolding(db, trade)
		if err != nil {
			t.Fatal(err)
		}
		RecordTrade(db, id, trade, name)
	}
	var holdings int
	db.QueryRow(`SELECT COUNT(*) FROM investments`).Scan(&holdings)
	if holdings != 3 {
		t.Errorf("%d holdings, want 3 distinct instruments", holdings)
	}
}
