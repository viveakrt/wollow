package ledger

import (
	"database/sql"
	"math"
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
			quote_symbol TEXT DEFAULT '', quote_error TEXT DEFAULT '', price_source TEXT DEFAULT '',
			realized_gain REAL DEFAULT 0, realized_gain_inr REAL, invested_inr REAL,
			created_at TEXT DEFAULT '', updated_at TEXT DEFAULT '');
		CREATE TABLE investment_trades (id INTEGER PRIMARY KEY AUTOINCREMENT, investment_id INTEGER,
			side TEXT, shares REAL, price REAL, amount REAL, currency TEXT, trade_date TEXT,
			order_type TEXT DEFAULT '', source TEXT DEFAULT '', dedupe_key TEXT DEFAULT '',
			fx_rate REAL, created_at TEXT DEFAULT '');
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

func addHolding(t *testing.T, db *sql.DB, currency string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO investments (kind, institution, name, currency) VALUES ('stock', 'Test', 'Test', ?)`, currency)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addTrade(t *testing.T, db *sql.DB, holding int64, side string, shares, amount float64, currency, date string, fx interface{}) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO investment_trades (investment_id, side, shares, price, amount, currency, trade_date, fx_rate)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, holding, side, shares, amount/shares, amount, currency, date, fx); err != nil {
		t.Fatal(err)
	}
}

type figures struct {
	units, invested, realized float64
	realizedINR, investedINR  sql.NullFloat64
	status                    string
}

func readFigures(t *testing.T, db *sql.DB, id int64) figures {
	t.Helper()
	if err := RecomputeHolding(db, id); err != nil {
		t.Fatal(err)
	}
	var f figures
	if err := db.QueryRow(`SELECT COALESCE(units, 0), invested_amount, realized_gain, realized_gain_inr, invested_inr, status
		FROM investments WHERE id = ?`, id).Scan(&f.units, &f.invested, &f.realized, &f.realizedINR, &f.investedINR, &f.status); err != nil {
		t.Fatal(err)
	}
	return f
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// Buying again after a sale must not change the profit that sale already made.
func TestRealizedGainUsesMovingAverageCost(t *testing.T) {
	db := newHoldingsDB(t)
	id := addHolding(t, db, "INR")
	addTrade(t, db, id, "buy", 10, 1000, "INR", "2026-01-01", nil) // avg 100
	addTrade(t, db, id, "sell", 5, 750, "INR", "2026-02-01", nil)  // +250
	addTrade(t, db, id, "buy", 5, 1000, "INR", "2026-03-01", nil)  // 10 held at 1500, avg 150

	f := readFigures(t, db, id)
	if !near(f.units, 10) || !near(f.invested, 1500) || !near(f.realized, 250) {
		t.Fatalf("after re-buy: %+v, want 10 units, 1500 invested, 250 realised", f)
	}

	addTrade(t, db, id, "sell", 10, 1800, "INR", "2026-04-01", nil) // +300
	f = readFigures(t, db, id)
	if !near(f.units, 0) || !near(f.invested, 0) || !near(f.realized, 550) || f.status != "closed" {
		t.Errorf("after selling out: %+v, want closed with 550 realised", f)
	}
	if !f.realizedINR.Valid || !near(f.realizedINR.Float64, 550) {
		t.Errorf("realised INR = %v, want 550 for a rupee holding", f.realizedINR)
	}
}

// A US sale's rupee profit includes the rupee's move: bought at ₹80/$, sold at ₹90/$.
func TestRealizedGainInINRUsesTradeDateRates(t *testing.T) {
	db := newHoldingsDB(t)
	id := addHolding(t, db, "USD")
	addTrade(t, db, id, "buy", 10, 1000, "USD", "2025-01-10", 80.0)
	addTrade(t, db, id, "sell", 4, 480, "USD", "2026-01-10", 90.0)

	f := readFigures(t, db, id)
	if !near(f.realized, 80) {
		t.Errorf("realised = %v, want $80 (4 shares, $120 vs $100)", f.realized)
	}
	// 480 × 90 − 4 × 100 × 80
	if !f.realizedINR.Valid || !near(f.realizedINR.Float64, 11200) {
		t.Errorf("realised INR = %v, want 11200", f.realizedINR)
	}
	// 6 shares still held at $100 bought at ₹80.
	if !f.investedINR.Valid || !near(f.investedINR.Float64, 48000) || !near(f.invested, 600) {
		t.Errorf("invested = %v / INR %v, want $600 / ₹48000", f.invested, f.investedINR)
	}
}

func TestRupeeFiguresUnknownUntilEveryTradeHasARate(t *testing.T) {
	db := newHoldingsDB(t)
	id := addHolding(t, db, "USD")
	addTrade(t, db, id, "buy", 10, 1000, "USD", "2025-01-10", 80.0)
	addTrade(t, db, id, "sell", 4, 480, "USD", "2026-01-10", nil)

	f := readFigures(t, db, id)
	if f.realizedINR.Valid || f.investedINR.Valid {
		t.Errorf("INR figures = %v / %v, want unknown while a rate is missing", f.realizedINR, f.investedINR)
	}
	if !near(f.realized, 80) {
		t.Errorf("realised = %v, want $80 regardless", f.realized)
	}
}

// Pricing a hand-entered holding must not wipe the cost the user typed in.
func TestPricingAHoldingWithoutTradesKeepsItsCost(t *testing.T) {
	db := newHoldingsDB(t)
	res, _ := db.Exec(`INSERT INTO investments (kind, name, currency, units, invested_amount, current_value)
		VALUES ('us_stock', 'Apple', 'USD', 10, 500, 500)`)
	id, _ := res.LastInsertId()

	if err := SetHoldingPrice(db, id, 60, "2026-09-16", "market"); err != nil {
		t.Fatal(err)
	}
	var invested, value float64
	var source string
	db.QueryRow(`SELECT invested_amount, current_value, price_source FROM investments WHERE id = ?`, id).Scan(&invested, &value, &source)
	if invested != 500 || value != 600 || source != "market" {
		t.Errorf("invested=%v value=%v source=%q, want 500/600/market", invested, value, source)
	}
}
