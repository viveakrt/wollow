package moneyapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/db"
)

// newTestServer builds a Money server over a real schema. These handlers are
// almost entirely SQL, and SQL only fails when it runs — an ON CONFLICT that
// doesn't match its index parses fine and 500s in production.
func newTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	server := NewServer(conn, nil, nil)
	mux := http.NewServeMux()
	server.Register(mux)
	return server, mux
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	return out
}

// Re-importing a deposit summary must refresh the holdings it names rather than
// duplicate them — an export is a snapshot of the whole portfolio, so importing
// a newer one is the normal way to keep principal current.
func TestImportDepositsIsIdempotent(t *testing.T) {
	server, mux := newTestServer(t)

	body := `{"fileName":"FDSummary.xls","deposits":[
		{"kind":"fd","institution":"HDFC","name":"HDFC FD","identifier":"50301125623955",
		 "currency":"INR","investedAmount":10000,"maturityAmount":11489,"interestRate":7,
		 "startDate":"2025-03-10","maturityDate":"2027-03-10","dedupeKey":"abc123"}]}`

	first := do(t, mux, "POST", "/api/money/import/deposits/commit", body)
	if first.Code != http.StatusOK {
		t.Fatalf("first commit: %d %s", first.Code, first.Body.String())
	}
	if got := decode[map[string]int](t, first); got["imported"] != 1 {
		t.Errorf("first commit imported = %d, want 1", got["imported"])
	}

	second := do(t, mux, "POST", "/api/money/import/deposits/commit", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second commit: %d %s", second.Code, second.Body.String())
	}
	result := decode[map[string]int](t, second)
	if result["imported"] != 0 || result["updated"] != 1 {
		t.Errorf("second commit imported=%d updated=%d, want 0 and 1", result["imported"], result["updated"])
	}

	var count int
	if err := server.DB.QueryRow(`SELECT COUNT(*) FROM investments`).Scan(&count); err != nil {
		t.Fatalf("counting investments: %v", err)
	}
	if count != 1 {
		t.Errorf("investments table has %d rows, want 1", count)
	}
}

// Hand-entered holdings carry no dedupe key. The unique index is partial for
// exactly that reason, and several of them must be able to coexist.
func TestManualInvestmentsCoexistWithoutDedupeKeys(t *testing.T) {
	server, mux := newTestServer(t)

	for _, name := range []string{"Gold", "NPS", "Some fund"} {
		w := do(t, mux, "POST", "/api/money/investments",
			`{"name":"`+name+`","kind":"other","investedAmount":1000}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("creating %s: %d %s", name, w.Code, w.Body.String())
		}
	}

	var count int
	if err := server.DB.QueryRow(`SELECT COUNT(*) FROM investments WHERE dedupe_key = ''`).Scan(&count); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 3 {
		t.Errorf("%d keyless holdings stored, want 3", count)
	}
}

func TestInvestmentSummaryTotals(t *testing.T) {
	_, mux := newTestServer(t)

	do(t, mux, "POST", "/api/money/investments", `{"name":"FD","kind":"fd","investedAmount":10000,"currentValue":11000}`)
	do(t, mux, "POST", "/api/money/investments", `{"name":"MF","kind":"mutual_fund","investedAmount":5000,"currentValue":4500}`)

	w := do(t, mux, "GET", "/api/money/investments/summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	summary := decode[investmentSummary](t, w)

	if summary.TotalInvested != 15000 {
		t.Errorf("invested = %.2f, want 15000", summary.TotalInvested)
	}
	if summary.TotalValue != 15500 {
		t.Errorf("value = %.2f, want 15500", summary.TotalValue)
	}
	if summary.Gain != 500 {
		t.Errorf("gain = %.2f, want 500", summary.Gain)
	}
	if len(summary.ByKind) != 2 {
		t.Errorf("byKind has %d entries, want 2", len(summary.ByKind))
	}
}

// Summing every balance into one figure treats a card's outstanding spend as
// savings. Net worth has to be assets minus liabilities, holdings included.
func TestDashboardNetWorthSeparatesAssetsFromLiabilities(t *testing.T) {
	server, mux := newTestServer(t)

	seedAccount(t, server.DB, "Savings", "bank", 200000)
	seedAccount(t, server.DB, "Card", "credit_card", -8000)
	do(t, mux, "POST", "/api/money/investments", `{"name":"FD","kind":"fd","investedAmount":50000}`)

	w := do(t, mux, "GET", "/api/money/dashboard/summary", "")
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", w.Code, w.Body.String())
	}
	summary := decode[dashboardSummary](t, w)

	if summary.TotalAssets != 250000 {
		t.Errorf("assets = %.2f, want 250000 (200000 cash + 50000 holdings)", summary.TotalAssets)
	}
	if summary.TotalLiabilities != 8000 {
		t.Errorf("liabilities = %.2f, want 8000", summary.TotalLiabilities)
	}
	if summary.NetWorth != 242000 {
		t.Errorf("netWorth = %.2f, want 242000", summary.NetWorth)
	}
	if summary.InvestmentValue != 50000 {
		t.Errorf("investmentValue = %.2f, want 50000", summary.InvestmentValue)
	}
}

// The add-account form offers this list so a hand-entered account records the
// issuer code the parsers key on. If it stopped serving the code, accounts
// would be saved under a display name and their alerts would never attach.
func TestInstitutionsAreOfferedWithTheirIssuerCode(t *testing.T) {
	_, mux := newTestServer(t)

	w := do(t, mux, "GET", "/api/money/institutions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("institutions: %d %s", w.Code, w.Body.String())
	}
	list := decode[[]institutionItem](t, w)
	if len(list) < 10 {
		t.Fatalf("got %d institutions, want the registry", len(list))
	}

	var hdfc *institutionItem
	for i := range list {
		if list[i].Issuer == "HDFC" {
			hdfc = &list[i]
		}
		if list[i].Issuer == "" || list[i].Name == "" {
			t.Errorf("institution %+v is missing an issuer or name", list[i])
		}
	}
	if hdfc == nil {
		t.Fatal("HDFC missing from the offered institutions")
	}
	if hdfc.Name != "HDFC Bank" || hdfc.DefaultType != "bank" {
		t.Errorf("HDFC = %+v, want name \"HDFC Bank\" and defaultType \"bank\"", *hdfc)
	}
}

// Accounts must round-trip the fields the UI reads; a column added to the
// schema but not to the SELECT reads as zero in the browser.
func TestAccountRoundTripsCreditLimitAndSource(t *testing.T) {
	_, mux := newTestServer(t)

	created := do(t, mux, "POST", "/api/money/accounts",
		`{"name":"Card","bank":"HDFC","accountType":"credit_card","creditLimit":100000}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}

	listed := decode[[]models.Account](t, do(t, mux, "GET", "/api/money/accounts", ""))
	if len(listed) != 1 {
		t.Fatalf("listed %d accounts, want 1", len(listed))
	}
	if listed[0].CreditLimit != 100000 {
		t.Errorf("creditLimit = %.2f, want 100000", listed[0].CreditLimit)
	}
	if listed[0].Source != "manual" {
		t.Errorf("source = %q, want %q", listed[0].Source, "manual")
	}
}

// The account a statement is for very often already exists as a row that alert
// ingest created, and that row only ever knew the masked tail. Failing to match
// it offers to create a second account for the same bank account, splitting the
// balance across both.
func TestStatementMatchesAnAccountDiscoveredFromMail(t *testing.T) {
	server, _ := newTestServer(t)

	if _, err := server.DB.Exec(`
		INSERT INTO finance_accounts (name, bank, account_type, account_number, source)
		VALUES ('HDFC Bank •• 4125', 'HDFC', 'bank', 'XXXXXXXX4125', 'email')`); err != nil {
		t.Fatalf("seeding discovered account: %v", err)
	}

	matched := server.matchStatementAccount("50100501934125")
	if matched == nil {
		t.Fatal("a statement for ...4125 did not match the account discovered from mail")
	}
	if matched.ID != 1 {
		t.Errorf("matched account id = %d, want 1", matched.ID)
	}

	if other := server.matchStatementAccount("50100509999999"); other != nil {
		t.Errorf("an unrelated account number matched %+v", other)
	}
	if none := server.matchStatementAccount(""); none != nil {
		t.Errorf("an empty account number matched %+v", none)
	}
}

func seedAccount(t *testing.T, conn *sql.DB, name, kind string, balance float64) {
	t.Helper()
	if _, err := conn.Exec(`
		INSERT INTO finance_accounts (name, account_type, current_balance, opening_balance)
		VALUES (?, ?, ?, ?)`, name, kind, balance, balance); err != nil {
		t.Fatalf("seeding account %s: %v", name, err)
	}
}

// Categorising one occurrence categorises the narration.
//
// A narration names the payee and the rail it arrived on, so every row
// carrying it is the same kind of payment. Tagging one and leaving its
// thirty-two twins untouched is work the user would only have to repeat, which
// is the whole complaint this behaviour answers.
func TestCategorisingOneRowSpreadsAcrossTheNarration(t *testing.T) {
	server, mux := newTestServer(t)

	if _, err := server.DB.Exec(`
		INSERT INTO finance_accounts (id, name, bank, account_type, source)
		VALUES (1, 'HDFC', 'HDFC', 'bank', 'manual')`); err != nil {
		t.Fatal(err)
	}
	// Four of the same payee, one unrelated payment.
	for i, date := range []string{"2026-08-01", "2026-07-01", "2026-06-01", "2026-05-01"} {
		if _, err := server.DB.Exec(`
			INSERT INTO transactions (id, account_id, txn_date, value_date, narration, withdrawal_amt, type, dedupe_hash)
			VALUES (?, 1, ?, ?, 'UPI-Vyapar-Vyapar', 2500, 'expense', ?)`,
			i+1, date, date, "h"+date); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.DB.Exec(`
		INSERT INTO transactions (id, account_id, txn_date, value_date, narration, withdrawal_amt, type, dedupe_hash)
		VALUES (9, 1, '2026-08-02', '2026-08-02', 'UPI-SWIGGY-9871@ybl', 450, 'expense', 'h9')`); err != nil {
		t.Fatal(err)
	}

	var shopping int64
	server.DB.QueryRow(`SELECT id FROM categories WHERE name = 'Shopping'`).Scan(&shopping)

	// Tag only the first one.
	w := do(t, mux, "POST", "/api/money/transactions/bulk-categorize",
		`{"ids":[1],"categoryId":`+itoa(shopping)+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk categorize: %d %s", w.Code, w.Body.String())
	}
	res := decode[bulkCategorizeResponse](t, w)
	if res.Matched != 3 {
		t.Errorf("matched = %d, want the 3 siblings sharing the narration", res.Matched)
	}

	var tagged int
	server.DB.QueryRow(`SELECT COUNT(*) FROM transactions
		WHERE narration = 'UPI-Vyapar-Vyapar' AND category_id = ?`, shopping).Scan(&tagged)
	if tagged != 4 {
		t.Errorf("%d of 4 rows carry the category, want all", tagged)
	}

	// The unrelated payment must be untouched.
	var other sql.NullInt64
	server.DB.QueryRow(`SELECT category_id FROM transactions WHERE id = 9`).Scan(&other)
	if other.Valid {
		t.Error("an unrelated narration was categorised too")
	}
}

// Correcting a category on one row corrects the narration everywhere, so a
// wrong tag does not have to be undone thirty-two times.
func TestCorrectingACategorySpreadsToo(t *testing.T) {
	server, mux := newTestServer(t)
	server.DB.Exec(`INSERT INTO finance_accounts (id, name, bank, account_type, source)
		VALUES (1, 'HDFC', 'HDFC', 'bank', 'manual')`)

	var shopping, food int64
	server.DB.QueryRow(`SELECT id FROM categories WHERE name = 'Shopping'`).Scan(&shopping)
	server.DB.QueryRow(`SELECT id FROM categories WHERE name = 'Food & Dining'`).Scan(&food)

	for i := 1; i <= 3; i++ {
		server.DB.Exec(`
			INSERT INTO transactions (id, account_id, txn_date, value_date, narration, withdrawal_amt, type, category_id, dedupe_hash)
			VALUES (?, 1, '2026-08-01', '2026-08-01', 'UPI-cred-cred', 100, 'expense', ?, ?)`,
			i, shopping, "h"+itoa(int64(i)))
	}

	// Fix one of them through the ordinary edit path.
	body := `{"accountId":1,"txnDate":"2026-08-01","narration":"UPI-cred-cred",` +
		`"withdrawalAmt":100,"type":"expense","categoryId":` + itoa(food) + `}`
	if w := do(t, mux, "PUT", "/api/money/transactions/1", body); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}

	var corrected int
	server.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE category_id = ?`, food).Scan(&corrected)
	if corrected != 3 {
		t.Errorf("%d of 3 rows corrected, want all — fixing one fixes the narration", corrected)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

// A US stock counts toward net worth, converted at a rate taken from the
// user's own forex remittance.
//
// Both failure modes here are silent and expensive: adding $245.73 to a rupee
// total overstates net worth ~96x, and leaving the holding out understates it
// by the whole position. The rate is neither invented nor assumed — it is read
// off a transaction where the user's bank actually converted rupees to dollars.
func TestUSHoldingCountsTowardNetWorthAtTheUsersOwnRate(t *testing.T) {
	server, mux := newTestServer(t)

	server.DB.Exec(`INSERT INTO finance_accounts (id, name, bank, account_type, source, current_balance)
		VALUES (1, 'HDFC', 'HDFC', 'bank', 'manual', 200000)`)
	// INR 99,670.15 sent as USD 1,036.18 — a rate of 96.19.
	server.DB.Exec(`INSERT INTO transactions (account_id, txn_date, value_date, withdrawal_amt, narration, dedupe_hash)
		VALUES (1, '2026-08-12', '2026-08-12', 99670.15, 'RFX 110826BTT07660 USD1036.18@96.19', 'fx1')`)
	// A US holding worth $245.73.
	server.DB.Exec(`INSERT INTO investments (kind, institution, name, currency, invested_amount, current_value, status, source)
		VALUES ('us_stock', 'INDmoney', 'Take-Two Interactive Software Inc.', 'USD', 245.73, 245.73, 'active', 'email')`)

	summary := decode[dashboardSummary](t, do(t, mux, "GET", "/api/money/dashboard/summary", ""))

	if len(summary.ForeignHoldings) != 1 {
		t.Fatalf("foreign holdings = %d, want 1", len(summary.ForeignHoldings))
	}
	f := summary.ForeignHoldings[0]
	if f.Currency != "USD" || f.Value != 245.73 {
		t.Errorf("holding = %s %.2f, want USD 245.73", f.Currency, f.Value)
	}
	if f.Rate < 96.1 || f.Rate > 96.3 {
		t.Errorf("rate = %v, want the 96.19 stated by the user's own remittance", f.Rate)
	}
	if f.RateSource != "derived" {
		t.Errorf("rateSource = %q, want %q so the number is traceable", f.RateSource, "derived")
	}

	wantINR := 245.73 * f.Rate
	if diff := summary.InvestmentValue - wantINR; diff > 0.01 || diff < -0.01 {
		t.Errorf("investmentValue = %.2f, want the converted %.2f", summary.InvestmentValue, wantINR)
	}
	if summary.ForeignConvertedINR <= 0 {
		t.Error("foreignConvertedInr = 0 — the UI could not say what rests on a rate")
	}
	if len(summary.UnconvertedCurrencies) != 0 {
		t.Errorf("unconverted = %v, want none", summary.UnconvertedCurrencies)
	}
	// And it must actually reach net worth, not just be reported beside it.
	if summary.NetWorth < 200000+wantINR-0.01 {
		t.Errorf("netWorth = %.2f, want it to include the %.2f US holding", summary.NetWorth, wantINR)
	}
}

// Without any evidence of a rate the holding is left out and named, rather
// than folded in at a made-up number.
func TestForeignHoldingWithoutARateIsExcludedAndNamed(t *testing.T) {
	server, mux := newTestServer(t)
	server.DB.Exec(`INSERT INTO finance_accounts (id, name, bank, account_type, source, current_balance)
		VALUES (1, 'HDFC', 'HDFC', 'bank', 'manual', 200000)`)
	server.DB.Exec(`INSERT INTO investments (kind, institution, name, currency, invested_amount, current_value, status, source)
		VALUES ('us_stock', 'INDmoney', 'ASML', 'USD', 499.99, 499.99, 'active', 'email')`)

	summary := decode[dashboardSummary](t, do(t, mux, "GET", "/api/money/dashboard/summary", ""))

	if summary.InvestmentValue != 0 {
		t.Errorf("investmentValue = %.2f, want 0 — no rate means no conversion", summary.InvestmentValue)
	}
	if len(summary.UnconvertedCurrencies) != 1 || summary.UnconvertedCurrencies[0] != "USD" {
		t.Errorf("unconverted = %v, want [USD] so the gap is visible", summary.UnconvertedCurrencies)
	}
}

// A holding with no trades is a free-standing figure: editing units/invested/
// current value must simply work, the way it always has.
func TestEditingAManualHoldingChangesItsNumbers(t *testing.T) {
	_, mux := newTestServer(t)
	created := decode[models.Investment](t, do(t, mux, "POST", "/api/money/investments",
		`{"name":"HDFC FD","kind":"fd","investedAmount":10000,"currentValue":10000}`))

	updated := decode[models.Investment](t, do(t, mux, "PUT",
		fmt.Sprintf("/api/money/investments/%d", created.ID),
		`{"name":"HDFC FD","kind":"fd","investedAmount":15000,"currentValue":15500,"interestRate":7.5}`))

	if updated.InvestedAmount != 15000 || updated.CurrentValue != 15500 {
		t.Errorf("invested=%.2f value=%.2f, want 15000/15500 — a manual holding's figures should edit freely",
			updated.InvestedAmount, updated.CurrentValue)
	}
	if updated.HasTrades {
		t.Error("hasTrades = true for a holding with no trades")
	}
}

// The bug this whole design exists to prevent: a trade-backed holding's
// units/invested/current value must NOT be settable by PUT, because the very
// next trade or price update recomputes them from investment_trades and
// silently discards whatever the client sent — an edit that "works" today and
// vanishes without explanation next month.
func TestEditingATradeBackedHoldingIgnoresFigureFields(t *testing.T) {
	server, mux := newTestServer(t)
	server.DB.Exec(`INSERT INTO investments (id, kind, institution, name, identifier, currency,
		invested_amount, current_value, units, status, source)
		VALUES (1, 'stock', 'Zerodha', 'TATA MOTORS', 'INE155A01022', 'INR', 1992, 1992, 6, 'active', 'email')`)
	server.DB.Exec(`INSERT INTO investment_trades (investment_id, side, shares, price, amount, currency, trade_date, source)
		VALUES (1, 'buy', 6, 332, 1992, 'INR', '2026-08-14', 'email')`)

	// The client sends wildly different figures — as it would if it pre-filled
	// a form from a stale read and the user typed over them.
	updated := decode[models.Investment](t, do(t, mux, "PUT", "/api/money/investments/1",
		`{"name":"Tata Motors","kind":"stock","investedAmount":99999,"currentValue":99999,"units":999,"notes":"corrected label"}`))

	if updated.InvestedAmount != 1992 || updated.CurrentValue != 1992 || updated.Units == nil || *updated.Units != 6 {
		t.Errorf("invested=%.2f value=%.2f units=%v — trade-derived figures must survive a PUT unchanged",
			updated.InvestedAmount, updated.CurrentValue, updated.Units)
	}
	// Metadata still goes through.
	if updated.Name != "Tata Motors" || updated.Notes != "corrected label" {
		t.Errorf("name=%q notes=%q — metadata edits should still apply", updated.Name, updated.Notes)
	}
	if !updated.HasTrades {
		t.Error("hasTrades = false for a holding with a trade on it")
	}
}

// Adding, editing and deleting trades is the actual lever for correcting a
// trade-backed holding's numbers — this exercises all three and confirms the
// holding re-derives after each.
func TestTradeCRUDRecomputesTheHoldingEachTime(t *testing.T) {
	server, mux := newTestServer(t)
	server.DB.Exec(`INSERT INTO investments (id, kind, institution, name, identifier, currency, status, source)
		VALUES (1, 'stock', 'Zerodha', 'TATA MOTORS', 'INE155A01022', 'INR', 'active', 'email')`)

	// Add: a real cost the user knows, correcting an approximated seed.
	added := decode[models.Investment](t, do(t, mux, "POST", "/api/money/investments/1/trades",
		`{"side":"buy","shares":6,"price":300,"tradeDate":"2026-01-01"}`))
	if added.Units == nil || *added.Units != 6 || added.InvestedAmount != 1800 {
		t.Fatalf("after add: units=%v invested=%.2f, want 6/1800", added.Units, added.InvestedAmount)
	}

	trades := decode[[]models.InvestmentTrade](t, do(t, mux, "GET", "/api/money/investments/1/trades", ""))
	if len(trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(trades))
	}
	tradeID := trades[0].ID

	// Update: correct the price.
	updated := decode[models.Investment](t, do(t, mux, "PUT",
		fmt.Sprintf("/api/money/investments/1/trades/%d", tradeID),
		`{"side":"buy","shares":6,"price":332,"tradeDate":"2026-01-01"}`))
	if updated.InvestedAmount != 1992 {
		t.Errorf("after update: invested=%.2f, want 1992 (6 * 332)", updated.InvestedAmount)
	}

	// Delete: the position empties out rather than the holding being force-removed.
	deleted := decode[models.Investment](t, do(t, mux, "DELETE",
		fmt.Sprintf("/api/money/investments/1/trades/%d", tradeID), ""))
	if deleted.InvestedAmount != 0 || deleted.Units == nil || *deleted.Units != 0 {
		t.Errorf("after delete: invested=%.2f units=%v, want 0/0", deleted.InvestedAmount, deleted.Units)
	}
	if deleted.HasTrades {
		t.Error("hasTrades = true after its only trade was deleted")
	}
}

// A trade belongs to exactly the holding it was created on — editing or
// deleting it through a DIFFERENT holding's id must be refused, not silently
// mutate the wrong position.
func TestTradeMutationIsScopedToItsOwnHolding(t *testing.T) {
	server, mux := newTestServer(t)
	server.DB.Exec(`INSERT INTO investments (id, kind, institution, name, currency, status, source)
		VALUES (1, 'stock', 'Zerodha', 'A', 'INR', 'active', 'email'),
		       (2, 'stock', 'Zerodha', 'B', 'INR', 'active', 'email')`)
	server.DB.Exec(`INSERT INTO investment_trades (id, investment_id, side, shares, price, amount, currency, trade_date, source)
		VALUES (1, 1, 'buy', 1, 100, 100, 'INR', '2026-01-01', 'email')`)

	w := do(t, mux, "PUT", "/api/money/investments/2/trades/1",
		`{"side":"buy","shares":1,"price":200,"tradeDate":"2026-01-01"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("editing trade 1 via holding 2 = %d, want 404", w.Code)
	}
	var price float64
	server.DB.QueryRow(`SELECT price FROM investment_trades WHERE id = 1`).Scan(&price)
	if price != 100 {
		t.Errorf("trade 1's price = %v, want it untouched at 100", price)
	}
}
