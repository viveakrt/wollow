package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/platform/db"
)

func yahooStub(t *testing.T) *Yahoo {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/chart/AAPL":
			fmt.Fprint(w, `{"chart":{"result":[{"meta":{"symbol":"AAPL","currency":"USD","regularMarketPrice":331.34,"regularMarketTime":1789502401,"longName":"Apple Inc."}}],"error":null}}`)
		case r.URL.Path == "/chart/BARC.L":
			fmt.Fprint(w, `{"chart":{"result":[{"meta":{"symbol":"BARC.L","currency":"GBp","regularMarketPrice":250,"regularMarketTime":1789502401}}],"error":null}}`)
		case r.URL.Path == "/chart/USDINR=X":
			// Bars stamped 23:00 UTC belong to the next day in London (offset +1h); one close is missing.
			fmt.Fprint(w, `{"chart":{"result":[{"meta":{"symbol":"USDINR=X","currency":"INR","regularMarketPrice":95.8,"gmtoffset":3600},
				"timestamp":[1782860400,1782946800,1783033200],"indicators":{"quote":[{"close":[95.41,null,95.52]}]}}],"error":null}}`)
		case r.URL.Path == "/search" && r.URL.Query().Get("q") == "INE002A01018":
			fmt.Fprint(w, `{"quotes":[{"symbol":"RELIANCE.NS","exchange":"NSI","quoteType":"EQUITY","shortname":"RELIANCE INDUSTRIES LTD"},{"exchange":"NSI"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found"}}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return &Yahoo{HTTP: srv.Client(), ChartURL: srv.URL + "/chart/", SearchURL: srv.URL + "/search"}
}

func TestYahooQuote(t *testing.T) {
	y := yahooStub(t)
	ctx := context.Background()

	q, err := y.Quote(ctx, "AAPL")
	if err != nil {
		t.Fatal(err)
	}
	if q.Price != 331.34 || q.Currency != "USD" || q.Name != "Apple Inc." || q.Time.Unix() != 1789502401 {
		t.Errorf("quote = %+v", q)
	}

	// Pence must not be read as pounds.
	if q, err := y.Quote(ctx, "BARC.L"); err != nil || q.Price != 2.5 || q.Currency != "GBP" {
		t.Errorf("minor-unit quote = %+v, %v; want 2.50 GBP", q, err)
	}
	if _, err := y.Quote(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown symbol err = %v, want ErrNotFound", err)
	}
}

func TestYahooDailyClosesUseExchangeDates(t *testing.T) {
	closes, err := yahooStub(t).DailyCloses(context.Background(), "USDINR=X", time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := []Close{{"2026-07-01", 95.41}, {"2026-07-03", 95.52}}
	if len(closes) != len(want) || closes[0] != want[0] || closes[1] != want[1] {
		t.Errorf("closes = %+v, want %+v", closes, want)
	}
}

func TestYahooSearch(t *testing.T) {
	hits, err := yahooStub(t).Search(context.Background(), "INE002A01018")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Symbol != "RELIANCE.NS" || hits[0].Name != "RELIANCE INDUSTRIES LTD" {
		t.Errorf("hits = %+v", hits)
	}
}

func TestParseNAVAllReadsBothLayouts(t *testing.T) {
	const file = "Scheme Code;ISIN Div Payout/ ISIN Growth;ISIN Div Reinvestment;Scheme Name;Plan;Option;Net Asset Value;Date\n \nAxis Mutual Fund\n \n" +
		"135762;INF846K01WO1;-;Axis Children's Fund;Direct Plan;Growth Option;29.5645;15-Sep-2026\n" +
		"100027;INF200K01RJ1;INF200K01RK9;SBI Older Layout;18.2;14-Sep-2026\n" +
		"999999;INF000000000;-;No NAV Today;Direct;Growth;N.A.;15-Sep-2026\n"
	navs, err := ParseNAVAll(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if got := navs["INF846K01WO1"]; got.Value != 29.5645 || got.Date != "2026-09-15" || got.SchemeCode != "135762" {
		t.Errorf("current layout = %+v", got)
	}
	if navs["INF200K01RJ1"].Value != 18.2 || navs["INF200K01RK9"].Date != "2026-09-14" {
		t.Errorf("older layout growth/reinvestment = %+v / %+v", navs["INF200K01RJ1"], navs["INF200K01RK9"])
	}
	if _, ok := navs["INF000000000"]; ok || len(navs) != 3 {
		t.Errorf("parsed %d ISINs, want 3 and no entry without a NAV", len(navs))
	}
}

type fakeSource struct {
	quotes   map[string]Quote
	search   map[string][]SearchHit
	closes   map[string][]Close
	navs     map[string]NAV
	searches int
}

func (f *fakeSource) Quote(_ context.Context, symbol string) (Quote, error) {
	if q, ok := f.quotes[symbol]; ok {
		return q, nil
	}
	return Quote{}, ErrNotFound
}

func (f *fakeSource) DailyCloses(_ context.Context, symbol string, _, _ time.Time) ([]Close, error) {
	return f.closes[symbol], nil
}

func (f *fakeSource) Search(_ context.Context, query string) ([]SearchHit, error) {
	f.searches++
	return f.search[query], nil
}

func (f *fakeSource) NAVs(context.Context) (map[string]NAV, error) { return f.navs, nil }

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func insert(t *testing.T, conn *sql.DB, query string, args ...any) int64 {
	t.Helper()
	res, err := conn.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func holdingRow(t *testing.T, conn *sql.DB, name string, args ...any) int64 {
	t.Helper()
	return insert(t, conn, `INSERT INTO investments (kind, institution, name, identifier, currency, quote_symbol, units, invested_amount, current_value, status, source)
		VALUES (?, 'Test', ?, ?, ?, ?, ?, ?, ?, 'active', 'manual')`, append([]any{args[0], name}, args[1:]...)...)
}

func trade(t *testing.T, conn *sql.DB, holding int64, side string, shares, amount float64, currency, date string) {
	t.Helper()
	insert(t, conn, `INSERT INTO investment_trades (investment_id, side, shares, price, amount, currency, trade_date, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'manual')`, holding, side, shares, amount/shares, amount, currency, date)
}

type row struct {
	symbol, source, quoteError string
	price, value, invested     sql.NullFloat64
	realized                   float64
	realizedINR, investedINR   sql.NullFloat64
}

func readRow(t *testing.T, conn *sql.DB, id int64) row {
	t.Helper()
	var r row
	if err := conn.QueryRow(`SELECT quote_symbol, price_source, quote_error, last_price, current_value, invested_amount,
		realized_gain, realized_gain_inr, invested_inr FROM investments WHERE id = ?`, id).
		Scan(&r.symbol, &r.source, &r.quoteError, &r.price, &r.value, &r.invested, &r.realized, &r.realizedINR, &r.investedINR); err != nil {
		t.Fatal(err)
	}
	return r
}

func approx(v sql.NullFloat64, want float64) bool { return v.Valid && math.Abs(v.Float64-want) < 0.005 }

func TestRefreshPricesHoldingsRatesAndTradeDates(t *testing.T) {
	conn := openDB(t)
	apple := holdingRow(t, conn, "Apple Inc", "us_stock", "", "USD", "", nil, 0, 0)
	trade(t, conn, apple, "buy", 10, 1000, "USD", "2025-01-10")
	trade(t, conn, apple, "sell", 4, 480, "USD", "2026-01-12")
	reliance := holdingRow(t, conn, "RELIANCE", "stock", "INE002A01018", "INR", "", nil, 0, 0)
	trade(t, conn, reliance, "buy", 5, 6000, "INR", "2026-02-01")
	for _, id := range []int64{apple, reliance} {
		if err := ledger.RecomputeHolding(conn, id); err != nil {
			t.Fatal(err)
		}
	}
	fund := holdingRow(t, conn, "Axis Children's Fund", "mutual_fund", "INF846K01WO1", "INR", "", 100, 2500, 2500)
	private := holdingRow(t, conn, "Private Co", "stock", "", "INR", NoQuote, 1, 100, 100)

	when := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	src := &fakeSource{
		quotes: map[string]Quote{
			"AAPL":        {Symbol: "AAPL", Price: 150, Currency: "USD", Time: when},
			"RELIANCE.NS": {Symbol: "RELIANCE.NS", Price: 1300, Currency: "INR", Time: when},
			"USDINR=X":    {Symbol: "USDINR=X", Price: 95.85, Currency: "INR", Time: when},
		},
		search: map[string][]SearchHit{
			"INE002A01018": {{Symbol: "RELIANCE.NS", Exchange: "NSI", QuoteType: "EQUITY"}},
			"Apple Inc":    {{Symbol: "AAPL.TO", Exchange: "TOR", QuoteType: "EQUITY"}, {Symbol: "AAPL", Exchange: "NMS", QuoteType: "EQUITY"}},
		},
		closes: map[string][]Close{
			"USDINR=X": {{"2026-01-09", 90}, {"2025-01-09", 85}, {"2025-01-10", 86}},
		},
		navs: map[string]NAV{"INF846K01WO1": {Value: 29.5645, Date: "2026-09-15"}},
	}

	res, err := (&Refresher{DB: conn, Source: src}).Run(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Priced != 3 || res.TradeRates != 2 || len(res.RatesUpdated) != 1 || res.RatesUpdated[0] != "USD" {
		t.Errorf("result = %+v, want 3 priced, 2 trade rates, USD rate updated", res)
	}

	a := readRow(t, conn, apple)
	if a.symbol != "AAPL" || a.source != "market" || !approx(a.value, 900) {
		t.Errorf("apple = %+v, want the US listing priced at 6 × $150", a)
	}
	// $80 profit; in rupees 480 × 90 (sold 12 Jan, last close 9 Jan) − 4 × 100 × 86 (bought 10 Jan 2025).
	if math.Abs(a.realized-80) > 0.005 || !approx(a.realizedINR, 8800) || !approx(a.investedINR, 51600) {
		t.Errorf("apple realised = %v / INR %v, invested INR %v; want 80 / 8800 / 51600", a.realized, a.realizedINR, a.investedINR)
	}
	if r := readRow(t, conn, reliance); r.symbol != "RELIANCE.NS" || !approx(r.value, 6500) {
		t.Errorf("reliance = %+v, want NSE listing valued at 6500", r)
	}
	if f := readRow(t, conn, fund); f.symbol != AMFIPrefix+"INF846K01WO1" || !approx(f.value, 2956.45) || !approx(f.invested, 2500) {
		t.Errorf("fund = %+v, want AMFI NAV value 2956.45 with its cost kept", f)
	}
	if p := readRow(t, conn, private); p.price.Valid {
		t.Errorf("a holding switched off was priced: %+v", p)
	}
	if rate := ledger.RateToINR(conn, "USD"); rate != 95.85 {
		t.Errorf("USD rate = %v, want the live 95.85", rate)
	}
}

func TestRefreshExplainsWhatItCouldNotPrice(t *testing.T) {
	conn := openDB(t)
	mystery := holdingRow(t, conn, "Mystery Corp", "us_stock", "", "USD", "", 1, 10, 10)
	mismatch := holdingRow(t, conn, "Wrong Currency", "us_stock", "", "USD", "RELIANCE.NS", 1, 10, 10)
	gone := holdingRow(t, conn, "Delisted", "stock", "", "INR", "GONE.NS", 1, 10, 10)
	src := &fakeSource{quotes: map[string]Quote{
		"RELIANCE.NS": {Price: 1300, Currency: "INR", Time: time.Now()},
		"USDINR=X":    {Price: 95.85, Currency: "INR", Time: time.Now()},
	}}
	r := &Refresher{DB: conn, Source: src}

	res, err := r.Run(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Priced != 0 || res.Failed != 3 {
		t.Errorf("result = %+v, want nothing priced and 3 failures", res)
	}
	if m := readRow(t, conn, mystery); !strings.Contains(m.quoteError, "set its symbol by hand") {
		t.Errorf("unmatched holding error = %q", m.quoteError)
	}
	if m := readRow(t, conn, mismatch); m.price.Valid || !strings.Contains(m.quoteError, "trades in INR") {
		t.Errorf("currency mismatch = %+v, want it refused and explained", m)
	}
	if g := readRow(t, conn, gone); !strings.Contains(g.quoteError, "no market price found for GONE.NS") {
		t.Errorf("missing symbol error = %q", g.quoteError)
	}

	// A search that found nothing is not repeated on every refresh.
	searches := src.searches
	if _, err := r.Run(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if src.searches != searches {
		t.Errorf("searched %d more times for a holding already known to have no match", src.searches-searches)
	}
	// ...unless that holding is refreshed on its own, on purpose.
	if _, err := r.Run(context.Background(), mystery); err != nil {
		t.Fatal(err)
	}
	if src.searches == searches {
		t.Error("refreshing the holding directly did not retry its symbol")
	}
}
