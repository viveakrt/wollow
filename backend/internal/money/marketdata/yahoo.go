// Package marketdata fetches live prices and exchange rates: Yahoo Finance for
// stocks and currencies, AMFI for Indian mutual fund NAVs. Only symbols and
// ISINs leave the machine — never quantities or amounts.
package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound means the provider has no instrument under that symbol.
var ErrNotFound = errors.New("symbol not found")

// Yahoo rejects requests that don't look like they come from a browser.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

type Quote struct {
	Symbol   string
	Name     string
	Price    float64
	Currency string
	Time     time.Time
}

type Close struct {
	Date  string // YYYY-MM-DD in the exchange's own timezone
	Value float64
}

type SearchHit struct {
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
	Exchange  string `json:"exchange"`
	QuoteType string `json:"quoteType"`
}

// Yahoo uses Yahoo Finance's public chart and search endpoints, which are unofficial and can change.
type Yahoo struct {
	HTTP      *http.Client
	ChartURL  string
	SearchURL string
}

func NewYahoo() *Yahoo {
	return &Yahoo{
		HTTP:      &http.Client{Timeout: 20 * time.Second},
		ChartURL:  "https://query1.finance.yahoo.com/v8/finance/chart/",
		SearchURL: "https://query2.finance.yahoo.com/v1/finance/search",
	}
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("price service returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

type chartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				Currency           string  `json:"currency"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
				GMTOffset          int64   `json:"gmtoffset"`
				LongName           string  `json:"longName"`
				ShortName          string  `json:"shortName"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func (y *Yahoo) chart(ctx context.Context, symbol string, query url.Values) (*chartResponse, error) {
	var cr chartResponse
	if err := getJSON(ctx, y.HTTP, y.ChartURL+url.PathEscape(symbol)+"?"+query.Encode(), &cr); err != nil {
		return nil, err
	}
	if cr.Chart.Error != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, cr.Chart.Error.Description)
	}
	if len(cr.Chart.Result) == 0 {
		return nil, ErrNotFound
	}
	return &cr, nil
}

// minorUnits are currencies Yahoo quotes in their hundredth (pence, cents).
var minorUnits = map[string]string{"GBp": "GBP", "GBX": "GBP", "ZAc": "ZAR", "ILA": "ILS"}

// Quote returns the latest price for a symbol, in the currency it trades in.
func (y *Yahoo) Quote(ctx context.Context, symbol string) (Quote, error) {
	cr, err := y.chart(ctx, symbol, url.Values{"range": {"1d"}, "interval": {"1d"}})
	if err != nil {
		return Quote{}, err
	}
	m := cr.Chart.Result[0].Meta
	if m.RegularMarketPrice <= 0 {
		return Quote{}, fmt.Errorf("no price for %s", symbol)
	}
	q := Quote{
		Symbol:   m.Symbol,
		Name:     firstNonEmpty(m.LongName, m.ShortName),
		Price:    m.RegularMarketPrice,
		Currency: strings.ToUpper(m.Currency),
		Time:     time.Unix(m.RegularMarketTime, 0).UTC(),
	}
	if major, ok := minorUnits[m.Currency]; ok {
		q.Price /= 100
		q.Currency = major
	}
	return q, nil
}

// DailyCloses returns one closing value per trading day between from and to.
func (y *Yahoo) DailyCloses(ctx context.Context, symbol string, from, to time.Time) ([]Close, error) {
	cr, err := y.chart(ctx, symbol, url.Values{
		"period1":  {strconv.FormatInt(from.Unix(), 10)},
		"period2":  {strconv.FormatInt(to.Unix(), 10)},
		"interval": {"1d"},
	})
	if err != nil {
		return nil, err
	}
	res := cr.Chart.Result[0]
	if len(res.Indicators.Quote) == 0 {
		return nil, nil
	}
	closes := res.Indicators.Quote[0].Close
	var out []Close
	for i, ts := range res.Timestamp {
		if i >= len(closes) || closes[i] == nil || *closes[i] <= 0 {
			continue
		}
		// Shift by the exchange's offset: a bar stamped 23:00 UTC belongs to the next day in London.
		date := time.Unix(ts+res.Meta.GMTOffset, 0).UTC().Format("2006-01-02")
		out = append(out, Close{Date: date, Value: *closes[i]})
	}
	return out, nil
}

type searchResponse struct {
	Quotes []struct {
		Symbol    string `json:"symbol"`
		Exchange  string `json:"exchange"`
		QuoteType string `json:"quoteType"`
		ShortName string `json:"shortname"`
		LongName  string `json:"longname"`
	} `json:"quotes"`
}

// Search finds instruments by name, ticker or ISIN.
func (y *Yahoo) Search(ctx context.Context, query string) ([]SearchHit, error) {
	q := url.Values{"q": {query}, "quotesCount": {"8"}, "newsCount": {"0"}}
	var sr searchResponse
	if err := getJSON(ctx, y.HTTP, y.SearchURL+"?"+q.Encode(), &sr); err != nil {
		return nil, err
	}
	hits := make([]SearchHit, 0, len(sr.Quotes))
	for _, x := range sr.Quotes {
		if x.Symbol == "" {
			continue
		}
		hits = append(hits, SearchHit{
			Symbol: x.Symbol, Name: firstNonEmpty(x.LongName, x.ShortName),
			Exchange: x.Exchange, QuoteType: x.QuoteType,
		})
	}
	return hits, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
