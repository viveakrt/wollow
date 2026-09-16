package moneyapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"wollow/backend/internal/money/marketdata"
	"wollow/backend/internal/money/models"
)

type fakeMarket struct {
	quotes map[string]marketdata.Quote
	closes map[string][]marketdata.Close
	hits   []marketdata.SearchHit
}

func (f *fakeMarket) Quote(_ context.Context, symbol string) (marketdata.Quote, error) {
	if q, ok := f.quotes[symbol]; ok {
		return q, nil
	}
	return marketdata.Quote{}, marketdata.ErrNotFound
}

func (f *fakeMarket) DailyCloses(_ context.Context, symbol string, _, _ time.Time) ([]marketdata.Close, error) {
	return f.closes[symbol], nil
}

func (f *fakeMarket) Search(context.Context, string) ([]marketdata.SearchHit, error) { return f.hits, nil }

func (f *fakeMarket) NAVs(context.Context) (map[string]marketdata.NAV, error) {
	return map[string]marketdata.NAV{}, nil
}

// A US stock bought at ₹86/$ and partly sold at ₹90/$: its rupee cost and profit must use those
// rates, while today's value uses today's.
func TestRefreshPricesShowsUSHoldingInRupees(t *testing.T) {
	server, mux := newTestServer(t)
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	server.Market.Source = &fakeMarket{
		quotes: map[string]marketdata.Quote{
			"AAPL":     {Symbol: "AAPL", Price: 150, Currency: "USD", Time: now},
			"USDINR=X": {Symbol: "USDINR=X", Price: 95, Currency: "INR", Time: now},
		},
		closes: map[string][]marketdata.Close{
			"USDINR=X": {{Date: "2025-01-10", Value: 86}, {Date: "2026-01-09", Value: 90}},
		},
		hits: []marketdata.SearchHit{{Symbol: "AAPL", Name: "Apple Inc.", Exchange: "NMS", QuoteType: "EQUITY"}},
	}

	created := decode[models.Investment](t, do(t, mux, "POST", "/api/money/investments",
		`{"name":"Apple Inc","kind":"us_stock","institution":"INDmoney"}`))
	if created.Currency != "USD" {
		t.Errorf("currency = %q, want USD for a US stock", created.Currency)
	}
	path := fmt.Sprintf("/api/money/investments/%d", created.ID)
	do(t, mux, "POST", path+"/trades", `{"side":"buy","shares":10,"price":100,"tradeDate":"2025-01-10"}`)
	do(t, mux, "POST", path+"/trades", `{"side":"sell","shares":4,"price":120,"tradeDate":"2026-01-12"}`)

	w := do(t, mux, "POST", "/api/money/investments/refresh-prices", "")
	if w.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", w.Code, w.Body.String())
	}
	if res := decode[marketdata.RefreshResult](t, w); res.Priced != 1 || res.TradeRates != 2 {
		t.Errorf("refresh = %+v, want 1 priced and 2 trade rates", res)
	}

	list := decode[[]models.Investment](t, do(t, mux, "GET", "/api/money/investments", ""))
	if len(list) != 1 {
		t.Fatalf("holdings = %d, want 1", len(list))
	}
	h := list[0]
	if h.QuoteSymbol != "AAPL" || h.PriceSource != "market" || h.CurrentValue != 900 {
		t.Errorf("holding = %s %s %.2f, want AAPL market 900", h.QuoteSymbol, h.PriceSource, h.CurrentValue)
	}
	// $900 at ₹95 against 6 × $100 bought at ₹86.
	if h.ValueINR == nil || *h.ValueINR != 85500 || h.InvestedINR == nil || *h.InvestedINR != 51600 ||
		h.GainINR == nil || *h.GainINR != 33900 {
		t.Errorf("INR value/cost/gain = %v / %v / %v, want 85500 / 51600 / 33900", h.ValueINR, h.InvestedINR, h.GainINR)
	}
	// 480 × 90 − 4 × 100 × 86.
	if h.RealizedGain != 80 || h.RealizedGainINR == nil || *h.RealizedGainINR != 8800 {
		t.Errorf("realised = %.2f / INR %v, want 80 / 8800", h.RealizedGain, h.RealizedGainINR)
	}

	summary := decode[investmentSummary](t, do(t, mux, "GET", "/api/money/investments/summary", ""))
	if summary.TotalValueINR != 85500 || summary.UnrealizedGainINR != 33900 || summary.RealizedGainINR != 8800 {
		t.Errorf("summary INR value/unrealised/realised = %.2f / %.2f / %.2f", summary.TotalValueINR,
			summary.UnrealizedGainINR, summary.RealizedGainINR)
	}
	if summary.PricesUpdatedAt == "" {
		t.Error("pricesUpdatedAt is empty after a market refresh")
	}
}

func TestEditingAHoldingKeepsItsSymbolUnlessChanged(t *testing.T) {
	server, mux := newTestServer(t)
	server.DB.Exec(`INSERT INTO investments (id, kind, institution, name, currency, status, source, quote_symbol, quote_error)
		VALUES (1, 'stock', 'Zerodha', 'TATA MOTORS', 'INR', 'active', 'manual', 'TATAMOTORS.NS', 'no market price found')`)

	kept := decode[models.Investment](t, do(t, mux, "PUT", "/api/money/investments/1", `{"name":"Tata Motors","kind":"stock"}`))
	if kept.QuoteSymbol != "TATAMOTORS.NS" || kept.QuoteError == "" {
		t.Errorf("symbol = %q error = %q — an edit that didn't mention the symbol changed it", kept.QuoteSymbol, kept.QuoteError)
	}

	changed := decode[models.Investment](t, do(t, mux, "PUT", "/api/money/investments/1",
		`{"name":"Tata Motors","kind":"stock","quoteSymbol":" tmpv.ns "}`))
	if changed.QuoteSymbol != "TMPV.NS" || changed.QuoteError != "" {
		t.Errorf("symbol = %q error = %q, want TMPV.NS with the old error cleared", changed.QuoteSymbol, changed.QuoteError)
	}

	off := decode[models.Investment](t, do(t, mux, "PUT", "/api/money/investments/1",
		`{"name":"Tata Motors","kind":"stock","quoteSymbol":"NONE"}`))
	if off.QuoteSymbol != marketdata.NoQuote {
		t.Errorf("symbol = %q, want fetching switched off", off.QuoteSymbol)
	}
}

func TestMarketSearch(t *testing.T) {
	server, mux := newTestServer(t)
	server.Market.Source = &fakeMarket{hits: []marketdata.SearchHit{{Symbol: "TTWO", Name: "Take-Two", Exchange: "NMS", QuoteType: "EQUITY"}}}

	hits := decode[[]marketdata.SearchHit](t, do(t, mux, "GET", "/api/money/market/search?q=take-two", ""))
	if len(hits) != 1 || hits[0].Symbol != "TTWO" {
		t.Errorf("hits = %+v", hits)
	}
	if short := decode[[]marketdata.SearchHit](t, do(t, mux, "GET", "/api/money/market/search?q=a", "")); len(short) != 0 {
		t.Errorf("a one-letter query searched: %+v", short)
	}
}
