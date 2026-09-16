package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"wollow/backend/internal/money/ledger"
)

// Source is what a refresh needs from the outside world.
type Source interface {
	Quote(ctx context.Context, symbol string) (Quote, error)
	DailyCloses(ctx context.Context, symbol string, from, to time.Time) ([]Close, error)
	Search(ctx context.Context, query string) ([]SearchHit, error)
	NAVs(ctx context.Context) (map[string]NAV, error)
}

// Live is the production Source: Yahoo Finance plus AMFI.
type Live struct {
	*Yahoo
	AMFI *AMFI
}

func NewLive() *Live { return &Live{Yahoo: NewYahoo(), AMFI: NewAMFI()} }

func (l *Live) NAVs(ctx context.Context) (map[string]NAV, error) { return l.AMFI.NAVs(ctx) }

const (
	// NoQuote as a holding's quote_symbol switches price fetching off for it.
	NoQuote    = "none"
	AMFIPrefix = "AMFI:"
	// maxRateGapDays is how far back a trade date may reach for a close (weekends, holidays).
	maxRateGapDays = 10
)

var (
	anyISINRe   = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
	errNoMatch  = errors.New("couldn't find this holding on the market — set its symbol by hand")
	errNoISIN   = errors.New("no ISIN to look this fund's NAV up by — add its ISIN or set the symbol by hand")
	usExchanges = map[string]bool{"NMS": true, "NGM": true, "NCM": true, "NYQ": true, "ASE": true, "PCX": true, "BTS": true}
)

type RefreshResult struct {
	Priced       int      `json:"priced"`
	Failed       int      `json:"failed"`
	RatesUpdated []string `json:"ratesUpdated"`
	TradeRates   int      `json:"tradeRates"`
	Errors       []string `json:"errors"`
	RefreshedAt  string   `json:"refreshedAt"`
}

// Refresher runs one refresh at a time; the background job and the refresh button share it.
type Refresher struct {
	DB     *sql.DB
	Source Source
	mu     sync.Mutex
}

type holding struct {
	id                               int64
	kind, name, identifier, currency string
	symbol, quoteError               string
	units                            float64
}

// Run refreshes every active market-priced holding, or only onlyID when it is not 0.
func (r *Refresher) Run(ctx context.Context, onlyID int64) (*RefreshResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	res := &RefreshResult{RatesUpdated: []string{}, Errors: []string{}}
	holdings, err := r.loadHoldings(onlyID)
	if err != nil {
		return nil, err
	}
	for i := range holdings {
		r.resolve(ctx, &holdings[i], onlyID != 0, res)
	}
	r.backfillTradeRates(ctx, res)
	r.price(ctx, holdings, res)
	r.refreshRates(ctx, res)
	res.RefreshedAt = time.Now().UTC().Format(time.RFC3339)
	return res, nil
}

func (r *Refresher) loadHoldings(onlyID int64) ([]holding, error) {
	query := `SELECT id, kind, name, identifier, currency, quote_symbol, quote_error, COALESCE(units, 0)
		FROM investments
		WHERE status = 'active' AND quote_symbol != ?
		  AND (kind IN ('stock', 'us_stock', 'mutual_fund') OR quote_symbol != '')`
	args := []any{NoQuote}
	if onlyID != 0 {
		query += ` AND id = ?`
		args = append(args, onlyID)
	}
	rows, err := r.DB.Query(query+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []holding
	for rows.Next() {
		var h holding
		if err := rows.Scan(&h.id, &h.kind, &h.name, &h.identifier, &h.currency, &h.symbol, &h.quoteError, &h.units); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// resolve finds a symbol for a holding that has none. A search that simply
// found nothing is remembered, so it isn't repeated on every refresh.
func (r *Refresher) resolve(ctx context.Context, h *holding, force bool, res *RefreshResult) {
	if h.symbol != "" || (h.quoteError != "" && !force) {
		return
	}
	symbol, err := ResolveSymbol(ctx, r.Source, h.kind, h.name, h.identifier, h.currency)
	switch {
	case err == nil:
		h.symbol = symbol
		r.DB.Exec(`UPDATE investments SET quote_symbol = ?, quote_error = '' WHERE id = ?`, symbol, h.id)
	case errors.Is(err, errNoMatch) || errors.Is(err, errNoISIN):
		r.DB.Exec(`UPDATE investments SET quote_error = ? WHERE id = ?`, err.Error(), h.id)
		res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", h.name, err))
		res.Failed++
	default:
		res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", h.name, err))
		res.Failed++
	}
}

// ResolveSymbol finds the market symbol for a holding: AMFI by ISIN for a fund,
// Yahoo by ISIN or name otherwise, on the exchange that matches its currency.
func ResolveSymbol(ctx context.Context, src Source, kind, name, identifier, currency string) (string, error) {
	isin := strings.ToUpper(strings.TrimSpace(identifier))
	if kind == "mutual_fund" {
		if isinRe.MatchString(isin) {
			return AMFIPrefix + isin, nil
		}
		return "", errNoISIN
	}
	var queries []string
	if anyISINRe.MatchString(isin) {
		queries = append(queries, isin)
	}
	if name = strings.TrimSpace(name); name != "" {
		queries = append(queries, name)
	}
	for _, q := range queries {
		hits, err := src.Search(ctx, q)
		if err != nil {
			return "", err
		}
		if symbol := chooseSymbol(hits, currency); symbol != "" {
			return symbol, nil
		}
	}
	return "", errNoMatch
}

func chooseSymbol(hits []SearchHit, currency string) string {
	pick := func(want func(SearchHit) bool) string {
		for _, h := range hits {
			if (h.QuoteType == "EQUITY" || h.QuoteType == "ETF") && want(h) {
				return h.Symbol
			}
		}
		return ""
	}
	switch currencyOrINR(currency) {
	case "INR":
		if s := pick(func(h SearchHit) bool { return strings.HasSuffix(h.Symbol, ".NS") }); s != "" {
			return s
		}
		return pick(func(h SearchHit) bool { return strings.HasSuffix(h.Symbol, ".BO") })
	case "USD":
		return pick(func(h SearchHit) bool { return usExchanges[h.Exchange] })
	default:
		return pick(func(SearchHit) bool { return true })
	}
}

func (r *Refresher) price(ctx context.Context, holdings []holding, res *RefreshResult) {
	var navs map[string]NAV
	var navErr error
	quotes := map[string]Quote{}
	quoteErrs := map[string]error{}
	quoteFor := func(symbol string) (Quote, error) {
		if q, ok := quotes[symbol]; ok {
			return q, nil
		}
		if err, ok := quoteErrs[symbol]; ok {
			return Quote{}, err
		}
		q, err := r.Source.Quote(ctx, symbol)
		if err != nil {
			quoteErrs[symbol] = err
		} else {
			quotes[symbol] = q
		}
		return q, err
	}

	for _, h := range holdings {
		if h.symbol == "" || h.units <= 0 {
			continue
		}
		var price float64
		var asOf string
		var err error
		if isin, ok := strings.CutPrefix(h.symbol, AMFIPrefix); ok {
			if navs == nil && navErr == nil {
				navs, navErr = r.Source.NAVs(ctx)
			}
			nav, found := navs[strings.ToUpper(isin)]
			switch {
			case navErr != nil:
				err = navErr
			case currencyOrINR(h.currency) != "INR":
				err = fmt.Errorf("AMFI NAVs are in INR but this holding is in %s", h.currency)
			case !found:
				err = fmt.Errorf("AMFI has no NAV for %s", isin)
			default:
				price, asOf = nav.Value, nav.Date
			}
		} else if q, qErr := quoteFor(h.symbol); qErr != nil {
			err = qErr
		} else if !strings.EqualFold(q.Currency, currencyOrINR(h.currency)) {
			// Pricing a rupee holding in dollars would misstate it by the exchange rate.
			err = fmt.Errorf("%s trades in %s but this holding is in %s", h.symbol, q.Currency, currencyOrINR(h.currency))
		} else {
			price, asOf = q.Price, q.Time.Format(time.RFC3339)
		}

		if err == nil {
			err = ledger.SetHoldingPrice(r.DB, h.id, price, asOf, "market")
		}
		if err != nil {
			msg := err.Error()
			if errors.Is(err, ErrNotFound) {
				msg = fmt.Sprintf("no market price found for %s — check the symbol", h.symbol)
			}
			r.DB.Exec(`UPDATE investments SET quote_error = ? WHERE id = ?`, msg, h.id)
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %s", h.name, msg))
			res.Failed++
			continue
		}
		r.DB.Exec(`UPDATE investments SET quote_error = '' WHERE id = ?`, h.id)
		res.Priced++
	}
}

func (r *Refresher) refreshRates(ctx context.Context, res *RefreshResult) {
	rows, err := r.DB.Query(`SELECT DISTINCT UPPER(currency) FROM investments
		WHERE status = 'active' AND currency != '' AND UPPER(currency) != 'INR' ORDER BY 1`)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	var currencies []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			currencies = append(currencies, c)
		}
	}
	rows.Close()

	for _, c := range currencies {
		q, err := r.Source.Quote(ctx, c+"INR=X")
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s to INR rate: %v", c, err))
			continue
		}
		stored, err := ledger.SetMarketRate(r.DB, c, q.Price, q.Time.Format("2006-01-02"))
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s to INR rate: %v", c, err))
			continue
		}
		if stored {
			res.RatesUpdated = append(res.RatesUpdated, c)
		}
	}
}

// backfillTradeRates gives each foreign trade the exchange rate of its own trade date.
func (r *Refresher) backfillTradeRates(ctx context.Context, res *RefreshResult) {
	trades, err := ledger.TradesNeedingRate(r.DB)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	byCurrency := map[string][]ledger.TradeNeedingRate{}
	var order []string
	for _, t := range trades {
		if _, ok := byCurrency[t.Currency]; !ok {
			order = append(order, t.Currency)
		}
		byCurrency[t.Currency] = append(byCurrency[t.Currency], t)
	}

	touched := map[int64]bool{}
	for _, currency := range order {
		var first, last time.Time
		for _, t := range byCurrency[currency] {
			d, err := time.Parse("2006-01-02", t.TradeDate)
			if err != nil {
				continue
			}
			if first.IsZero() || d.Before(first) {
				first = d
			}
			if d.After(last) {
				last = d
			}
		}
		if first.IsZero() {
			continue
		}
		closes, err := r.Source.DailyCloses(ctx, currency+"INR=X", first.AddDate(0, 0, -maxRateGapDays), last.AddDate(0, 0, 2))
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s to INR history: %v", currency, err))
			continue
		}
		sort.Slice(closes, func(i, j int) bool { return closes[i].Date < closes[j].Date })
		for _, t := range byCurrency[currency] {
			rate, ok := rateOn(closes, t.TradeDate)
			if !ok {
				continue
			}
			if ledger.SetTradeRate(r.DB, t.ID, rate) == nil {
				res.TradeRates++
				touched[t.InvestmentID] = true
			}
		}
	}
	for id := range touched {
		ledger.RecomputeHolding(r.DB, id)
	}
}

// rateOn returns the last close on or before date, if it is recent enough to stand for that day.
func rateOn(closes []Close, date string) (float64, bool) {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, false
	}
	i := sort.Search(len(closes), func(i int) bool { return closes[i].Date > date })
	if i == 0 {
		return 0, false
	}
	c := closes[i-1]
	closeDay, err := time.Parse("2006-01-02", c.Date)
	if err != nil || day.Sub(closeDay) > maxRateGapDays*24*time.Hour {
		return 0, false
	}
	return c.Value, true
}

func currencyOrINR(c string) string {
	if c = strings.ToUpper(strings.TrimSpace(c)); c == "" {
		return "INR"
	}
	return c
}
