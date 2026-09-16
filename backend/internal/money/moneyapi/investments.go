package moneyapi

import (
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/marketdata"

	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

// investmentColumns keeps the list, get and write paths reading the same shape.
const investmentColumns = `
	id, account_id, kind, institution, name, identifier, currency,
	invested_amount, current_value, maturity_amount, interest_rate, units,
	last_price, last_price_at,
	price_source, quote_symbol, quote_error, realized_gain, realized_gain_inr, invested_inr,
	start_date, maturity_date, status, source, notes, created_at, updated_at`

func scanInvestment(scan func(...any) error) (models.Investment, error) {
	var (
		inv         models.Investment
		accountID   sql.NullInt64
		maturity    sql.NullFloat64
		rate        sql.NullFloat64
		units       sql.NullFloat64
		lastPrice   sql.NullFloat64
		realizedINR sql.NullFloat64
		investedINR sql.NullFloat64
	)
	err := scan(&inv.ID, &accountID, &inv.Kind, &inv.Institution, &inv.Name, &inv.Identifier,
		&inv.Currency, &inv.InvestedAmount, &inv.CurrentValue, &maturity, &rate, &units,
		&lastPrice, &inv.LastPriceAt,
		&inv.PriceSource, &inv.QuoteSymbol, &inv.QuoteError, &inv.RealizedGain, &realizedINR, &investedINR,
		&inv.StartDate, &inv.MaturityDate, &inv.Status, &inv.Source, &inv.Notes,
		&inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		return inv, err
	}
	if lastPrice.Valid {
		inv.LastPrice = &lastPrice.Float64
	}
	if realizedINR.Valid {
		inv.RealizedGainINR = &realizedINR.Float64
	}
	if investedINR.Valid {
		inv.InvestedINR = &investedINR.Float64
	}
	// Gain is derived rather than stored so it can never disagree with the two
	// figures it comes from.
	inv.Gain = inv.CurrentValue - inv.InvestedAmount
	if inv.InvestedAmount > 0 {
		inv.GainPercent = inv.Gain / inv.InvestedAmount * 100
	}
	// A holding nobody has priced reports its cost as its value, so say so —
	// otherwise a flat gain of zero looks like a real measurement.
	inv.Priced = lastPrice.Valid && lastPrice.Float64 > 0
	if accountID.Valid {
		inv.AccountID = &accountID.Int64
	}
	if maturity.Valid {
		inv.MaturityAmount = &maturity.Float64
	}
	if rate.Valid {
		inv.InterestRate = &rate.Float64
	}
	if units.Valid {
		inv.Units = &units.Float64
	}
	return inv, nil
}

// attachTradeCounts sets HasTrades on every holding in one extra query,
// rather than one query per row.
func attachTradeCounts(db interface {
	Query(string, ...any) (*sql.Rows, error)
}, invs []models.Investment) error {
	if len(invs) == 0 {
		return nil
	}
	counted := make(map[int64]bool, len(invs))
	rows, err := db.Query(`SELECT DISTINCT investment_id FROM investment_trades`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		counted[id] = true
	}
	for i := range invs {
		invs[i].HasTrades = counted[invs[i].ID]
	}
	return rows.Err()
}

func investmentHasTrades(db interface {
	QueryRow(string, ...any) *sql.Row
}, id int64) bool {
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM investment_trades WHERE investment_id = ?`, id).Scan(&n)
	return n > 0
}

// inrRates loads every stored exchange rate up front; the pool's single connection can't query mid-iteration.
func inrRates(db *sql.DB) map[string]float64 {
	rates := map[string]float64{"INR": 1}
	rows, err := db.Query(`SELECT UPPER(currency), inr_per_unit FROM fx_rates WHERE inr_per_unit > 0`)
	if err != nil {
		return rates
	}
	defer rows.Close()
	for rows.Next() {
		var currency string
		var rate float64
		if rows.Scan(&currency, &rate) == nil {
			rates[currency] = rate
		}
	}
	return rates
}

// attachINR states each holding in rupees: value at today's rate, cost at its trades' own rates where known.
func attachINR(invs []models.Investment, rates map[string]float64) {
	for i := range invs {
		inv := &invs[i]
		currency := strings.ToUpper(strings.TrimSpace(inv.Currency))
		if currency == "" || currency == "INR" {
			if inv.InvestedINR == nil {
				invested := inv.InvestedAmount
				inv.InvestedINR = &invested
			}
			if inv.RealizedGainINR == nil {
				realized := inv.RealizedGain
				inv.RealizedGainINR = &realized
			}
		}
		rate := rates[currency]
		if currency == "" {
			rate = 1
		}
		if rate <= 0 {
			continue
		}
		value := inv.CurrentValue * rate
		inv.ValueINR = &value
		if inv.InvestedINR != nil {
			gain := value - *inv.InvestedINR
			inv.GainINR = &gain
		}
	}
}

// normalizeQuoteSymbol upper-cases a market symbol, keeping the "none" switch recognisable.
func normalizeQuoteSymbol(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if strings.EqualFold(symbol, marketdata.NoQuote) {
		return marketdata.NoQuote
	}
	return strings.ToUpper(symbol)
}

func (s *Server) handleListInvestments(w http.ResponseWriter, r *http.Request) {
	where := "1 = 1"
	args := []any{}
	if kind := r.URL.Query().Get("kind"); kind != "" {
		where += " AND kind = ?"
		args = append(args, kind)
	}
	// Matured and closed holdings are hidden by default: they are history, and
	// leaving them in the list quietly inflates the portfolio total.
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	if status != "all" {
		where += " AND status = ?"
		args = append(args, status)
	}

	rows, err := s.DB.Query(`SELECT`+investmentColumns+`
		FROM investments WHERE `+where+`
		ORDER BY CASE WHEN maturity_date = '' THEN 1 ELSE 0 END, maturity_date, id`, args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	investments := []models.Investment{}
	for rows.Next() {
		inv, err := scanInvestment(rows.Scan)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		investments = append(investments, inv)
	}
	if err := attachTradeCounts(s.DB, investments); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	attachINR(investments, inrRates(s.DB))
	httpx.WriteJSON(w, 200, investments)
}

// investmentKindTotal is one row of the portfolio breakdown.
type investmentKindTotal struct {
	Kind     string  `json:"kind"`
	Count    int     `json:"count"`
	Invested float64 `json:"invested"`
	Value    float64 `json:"value"`
}

// investmentCurrencyTotal keeps each currency's figures apart. Summing a
// dollar holding into a rupee total overstates it by the exchange rate, so the
// portfolio is reported per currency and the UI shows each in its own.
type investmentCurrencyTotal struct {
	Currency string  `json:"currency"`
	Count    int     `json:"count"`
	Invested float64 `json:"invested"`
	Value    float64 `json:"value"`
	Gain     float64 `json:"gain"`
}

type investmentSummary struct {
	// These totals cover rupee holdings only; ByCurrency has the rest.
	TotalInvested float64                   `json:"totalInvested"`
	TotalValue    float64                   `json:"totalValue"`
	Gain          float64                   `json:"gain"`
	Count         int                       `json:"count"`
	ByCurrency    []investmentCurrencyTotal `json:"byCurrency"`
	ByKind        []investmentKindTotal     `json:"byKind"`
	// MaturingSoon is the holdings coming due within the next 90 days — the one
	// thing about a deposit portfolio that is actually time-sensitive.
	MaturingSoon []models.Investment `json:"maturingSoon"`
	// Every holding in rupees: value at today's rate, cost and realised profit at each trade's own rate where known.
	TotalValueINR         float64  `json:"totalValueInr"`
	TotalInvestedINR      float64  `json:"totalInvestedInr"`
	UnrealizedGainINR     float64  `json:"unrealizedGainInr"`
	RealizedGainINR       float64  `json:"realizedGainInr"`
	UnconvertedCurrencies []string `json:"unconvertedCurrencies"`
	PricesUpdatedAt       string   `json:"pricesUpdatedAt"`
}

func (s *Server) handleInvestmentSummary(w http.ResponseWriter, r *http.Request) {
	summary := investmentSummary{
		ByKind:                []investmentKindTotal{},
		ByCurrency:            []investmentCurrencyTotal{},
		MaturingSoon:          []models.Investment{},
		UnconvertedCurrencies: []string{},
	}

	rows, err := s.DB.Query(`
		SELECT kind, COUNT(*), COALESCE(SUM(invested_amount), 0), COALESCE(SUM(current_value), 0)
		FROM investments WHERE status = 'active'
		GROUP BY kind ORDER BY SUM(current_value) DESC`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for rows.Next() {
		var row investmentKindTotal
		if err := rows.Scan(&row.Kind, &row.Count, &row.Invested, &row.Value); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		summary.ByKind = append(summary.ByKind, row)
	}
	rows.Close()

	byCurrency, err := s.DB.Query(`
		SELECT currency, COUNT(*), COALESCE(SUM(invested_amount), 0), COALESCE(SUM(current_value), 0)
		FROM investments WHERE status = 'active'
		GROUP BY currency ORDER BY SUM(current_value) DESC`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for byCurrency.Next() {
		var row investmentCurrencyTotal
		if err := byCurrency.Scan(&row.Currency, &row.Count, &row.Invested, &row.Value); err != nil {
			byCurrency.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		row.Gain = row.Value - row.Invested
		summary.ByCurrency = append(summary.ByCurrency, row)
		summary.Count += row.Count
		// The headline totals stay rupee-only so they remain addable.
		if row.Currency == "INR" {
			summary.TotalInvested += row.Invested
			summary.TotalValue += row.Value
		}
	}
	byCurrency.Close()
	summary.Gain = summary.TotalValue - summary.TotalInvested

	maturing, err := s.DB.Query(`SELECT` + investmentColumns + `
		FROM investments
		WHERE status = 'active' AND maturity_date != ''
		  AND maturity_date >= date('now') AND maturity_date <= date('now', '+90 day')
		ORDER BY maturity_date LIMIT 10`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer maturing.Close()
	for maturing.Next() {
		inv, err := scanInvestment(maturing.Scan)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		summary.MaturingSoon = append(summary.MaturingSoon, inv)
	}
	maturing.Close()

	if err := s.addRupeeTotals(&summary); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, summary)
}

// addRupeeTotals sums every holding in rupees. Realised profit counts closed holdings too — that is where it lives.
func (s *Server) addRupeeTotals(summary *investmentSummary) error {
	rates := inrRates(s.DB)
	rows, err := s.DB.Query(`
		SELECT UPPER(currency), status, current_value, invested_amount, invested_inr, realized_gain, realized_gain_inr
		FROM investments`)
	if err != nil {
		return err
	}
	unconverted := map[string]bool{}
	for rows.Next() {
		var currency, status string
		var value, invested, realized float64
		var investedINR, realizedINR sql.NullFloat64
		if err := rows.Scan(&currency, &status, &value, &invested, &investedINR, &realized, &realizedINR); err != nil {
			rows.Close()
			return err
		}
		if currency == "" {
			currency = "INR"
		}
		rate := rates[currency]
		if rate <= 0 {
			if status == "active" || realized != 0 {
				unconverted[currency] = true
			}
			continue
		}
		if realizedINR.Valid {
			summary.RealizedGainINR += realizedINR.Float64
		} else {
			summary.RealizedGainINR += realized * rate
		}
		if status != "active" {
			continue
		}
		summary.TotalValueINR += value * rate
		if investedINR.Valid {
			summary.TotalInvestedINR += investedINR.Float64
		} else {
			summary.TotalInvestedINR += invested * rate
		}
	}
	rows.Close()
	summary.UnrealizedGainINR = summary.TotalValueINR - summary.TotalInvestedINR
	for currency := range unconverted {
		summary.UnconvertedCurrencies = append(summary.UnconvertedCurrencies, currency)
	}
	sort.Strings(summary.UnconvertedCurrencies)
	return s.DB.QueryRow(`SELECT COALESCE(MAX(last_price_at), '') FROM investments WHERE price_source = 'market'`).
		Scan(&summary.PricesUpdatedAt)
}

func (s *Server) handleCreateInvestment(w http.ResponseWriter, r *http.Request) {
	var inv models.Investment
	if err := httpx.DecodeJSON(r, &inv); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if strings.TrimSpace(inv.Name) == "" {
		httpx.WriteError(w, 400, "name is required")
		return
	}
	applyInvestmentDefaults(&inv)
	inv.QuoteSymbol = normalizeQuoteSymbol(inv.QuoteSymbol)

	res, err := s.DB.Exec(`
		INSERT INTO investments
			(account_id, kind, institution, name, identifier, currency, invested_amount,
			 current_value, maturity_amount, interest_rate, units, start_date, maturity_date,
			 status, source, notes, quote_symbol)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'manual', ?, ?)`,
		inv.AccountID, inv.Kind, inv.Institution, inv.Name, inv.Identifier, inv.Currency,
		inv.InvestedAmount, inv.CurrentValue, inv.MaturityAmount, inv.InterestRate, inv.Units,
		inv.StartDate, inv.MaturityDate, inv.Status, inv.Notes, inv.QuoteSymbol)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	inv.ID, _ = res.LastInsertId()
	inv.Source = "manual"
	httpx.WriteJSON(w, 201, inv)
}

// handleUpdateInvestment edits a holding's descriptive fields freely.
//
// Units, invested amount and current value are honoured only when the
// holding has no trades. A trade-backed holding's numbers are DERIVED (see
// ledger.RecomputeHolding) — accepting a client's copy of them here would work
// once and then vanish the next time any trade or price update recomputes the
// position, with nothing telling the user their edit was reverted. For those
// holdings the correct lever is the trades themselves: see
// handleAddInvestmentTrade / handleUpdateInvestmentTrade /
// handleDeleteInvestmentTrade, all of which recompute afterward.
func (s *Server) handleUpdateInvestment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var payload investmentPayload
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	inv := payload.Investment
	applyInvestmentDefaults(&inv)

	hasTrades := investmentHasTrades(s.DB, id)
	if hasTrades {
		if _, err := s.DB.Exec(`
			UPDATE investments SET
				account_id = ?, kind = ?, institution = ?, name = ?, identifier = ?,
				maturity_amount = ?, interest_rate = ?, start_date = ?, maturity_date = ?,
				notes = ?, updated_at = datetime('now')
			WHERE id = ?`,
			inv.AccountID, inv.Kind, inv.Institution, inv.Name, inv.Identifier,
			inv.MaturityAmount, inv.InterestRate, inv.StartDate, inv.MaturityDate, inv.Notes, id); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		// Currency and status follow the trades, not the request — changing
		// either here without touching the trades would make the two disagree.
		if err := ledger.RecomputeHolding(s.DB, id); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
	} else {
		if _, err := s.DB.Exec(`
			UPDATE investments SET
				account_id = ?, kind = ?, institution = ?, name = ?, identifier = ?, currency = ?,
				invested_amount = ?, current_value = ?, maturity_amount = ?, interest_rate = ?,
				units = ?, start_date = ?, maturity_date = ?, status = ?, notes = ?,
				updated_at = datetime('now')
			WHERE id = ?`,
			inv.AccountID, inv.Kind, inv.Institution, inv.Name, inv.Identifier, inv.Currency,
			inv.InvestedAmount, inv.CurrentValue, inv.MaturityAmount, inv.InterestRate, inv.Units,
			inv.StartDate, inv.MaturityDate, inv.Status, inv.Notes, id); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
	}

	if payload.QuoteSymbolOpt != nil {
		symbol := normalizeQuoteSymbol(*payload.QuoteSymbolOpt)
		// A changed symbol starts clean: the old one's error no longer applies.
		if _, err := s.DB.Exec(`UPDATE investments
			SET quote_error = CASE WHEN quote_symbol = ? THEN quote_error ELSE '' END, quote_symbol = ?
			WHERE id = ?`, symbol, symbol, id); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
	}
	s.writeInvestmentWithTrades(w, id)
}

// investmentPayload tells an omitted quoteSymbol apart from a cleared one, so an edit never wipes a symbol it didn't mention.
type investmentPayload struct {
	models.Investment
	QuoteSymbolOpt *string `json:"quoteSymbol"`
}

func (s *Server) handleDeleteInvestment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM investments WHERE id = ?`, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// applyInvestmentDefaults fills the fields a client may reasonably omit, and
// seeds current value from the amount invested — for a deposit held to
// maturity those are the same until interest is credited.
func applyInvestmentDefaults(inv *models.Investment) {
	if inv.Kind == "" {
		inv.Kind = "other"
	}
	if inv.Currency == "" {
		inv.Currency = "INR"
		if inv.Kind == "us_stock" {
			inv.Currency = "USD"
		}
	}
	if inv.Status == "" {
		inv.Status = "active"
	}
	if inv.CurrentValue == 0 {
		inv.CurrentValue = inv.InvestedAmount
	}
}

// handleListInvestmentTrades returns the orders that built one position, newest
// first. This is what makes a holding auditable: the average cost is a claim,
// and these are the trades it is a claim about.
func (s *Server) handleListInvestmentTrades(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	rows, err := s.DB.Query(`
		SELECT id, investment_id, side, shares, price, amount, currency,
		       trade_date, order_type, source, fx_rate, created_at
		FROM investment_trades WHERE investment_id = ?
		ORDER BY trade_date DESC, id DESC`, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	trades := []models.InvestmentTrade{}
	for rows.Next() {
		var t models.InvestmentTrade
		var fx sql.NullFloat64
		if err := rows.Scan(&t.ID, &t.InvestmentID, &t.Side, &t.Shares, &t.Price, &t.Amount,
			&t.Currency, &t.TradeDate, &t.OrderType, &t.Source, &fx, &t.CreatedAt); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		if fx.Valid {
			t.FXRate = &fx.Float64
		}
		trades = append(trades, t)
	}
	httpx.WriteJSON(w, 200, trades)
}

type setPriceRequest struct {
	Price float64 `json:"price"`
	AsOf  string  `json:"asOf"`
}

// handleSetInvestmentPrice records a price the user typed; the next market refresh replaces it for a holding with a symbol.
func (s *Server) handleSetInvestmentPrice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var req setPriceRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if req.Price <= 0 {
		httpx.WriteError(w, 400, "price must be greater than zero")
		return
	}
	if req.AsOf == "" {
		req.AsOf = time.Now().UTC().Format("2006-01-02")
	}
	if err := ledger.SetHoldingPrice(s.DB, id, req.Price, req.AsOf, "manual"); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	s.writeInvestmentWithTrades(w, id)
}

type addTradeRequest struct {
	Side      string  `json:"side"` // buy | sell
	Shares    float64 `json:"shares"`
	Price     float64 `json:"price"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	TradeDate string  `json:"tradeDate"`
	OrderType string  `json:"orderType"`
}

// handleAddInvestmentTrade records a trade the user enters by hand — filling
// in a purchase the mail parser never saw, or correcting a demat statement's
// approximated opening quantity with the real cost. It is the supported way
// to change a trade-backed holding's numbers: see handleUpdateInvestment.
func (s *Server) handleAddInvestmentTrade(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var req addTradeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if req.Side != "buy" && req.Side != "sell" {
		httpx.WriteError(w, 400, "side must be buy or sell")
		return
	}
	if req.Shares <= 0 {
		httpx.WriteError(w, 400, "shares must be greater than zero")
		return
	}
	if req.Amount <= 0 {
		if req.Price <= 0 {
			httpx.WriteError(w, 400, "amount or price is required")
			return
		}
		req.Amount = req.Shares * req.Price
	}
	if req.Price <= 0 {
		req.Price = req.Amount / req.Shares
	}
	if req.Currency == "" {
		s.DB.QueryRow(`SELECT currency FROM investments WHERE id = ?`, id).Scan(&req.Currency)
	}
	if req.TradeDate == "" {
		req.TradeDate = time.Now().UTC().Format("2006-01-02")
	}

	if _, err := s.DB.Exec(`
		INSERT INTO investment_trades (investment_id, side, shares, price, amount, currency, trade_date, order_type, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'manual')`,
		id, req.Side, req.Shares, req.Price, req.Amount, req.Currency, req.TradeDate, req.OrderType); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if err := ledger.RecomputeHolding(s.DB, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	s.writeInvestmentWithTrades(w, id)
}

// handleUpdateInvestmentTrade edits one trade already on a holding — the
// lever for correcting a figure that came from an approximation (a demat
// statement's opening-balance seed, say) once the real cost is known.
func (s *Server) handleUpdateInvestmentTrade(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	tradeID, err := strconv.ParseInt(r.PathValue("tradeId"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid trade id")
		return
	}
	var req addTradeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if req.Side != "buy" && req.Side != "sell" {
		httpx.WriteError(w, 400, "side must be buy or sell")
		return
	}
	if req.Shares <= 0 {
		httpx.WriteError(w, 400, "shares must be greater than zero")
		return
	}
	if req.Amount <= 0 {
		if req.Price <= 0 {
			httpx.WriteError(w, 400, "amount or price is required")
			return
		}
		req.Amount = req.Shares * req.Price
	}
	if req.Price <= 0 {
		req.Price = req.Amount / req.Shares
	}

	// A moved trade date needs its exchange rate looked up again.
	res, err := s.DB.Exec(`
		UPDATE investment_trades SET side = ?, shares = ?, price = ?, amount = ?,
			fx_rate = CASE WHEN trade_date = ? THEN fx_rate ELSE NULL END, trade_date = ?, order_type = ?
		WHERE id = ? AND investment_id = ?`,
		req.Side, req.Shares, req.Price, req.Amount, req.TradeDate, req.TradeDate, req.OrderType, tradeID, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "trade not found on this holding")
		return
	}
	if err := ledger.RecomputeHolding(s.DB, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	s.writeInvestmentWithTrades(w, id)
}

// handleDeleteInvestmentTrade removes one trade and re-derives the position
// from what's left — including all the way down to zero units if it was the
// only one, at which point the holding simply reports nothing invested rather
// than being force-deleted itself.
func (s *Server) handleDeleteInvestmentTrade(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	tradeID, err := strconv.ParseInt(r.PathValue("tradeId"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid trade id")
		return
	}
	res, err := s.DB.Exec(`DELETE FROM investment_trades WHERE id = ? AND investment_id = ?`, tradeID, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "trade not found on this holding")
		return
	}
	if err := ledger.RecomputeHolding(s.DB, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	s.writeInvestmentWithTrades(w, id)
}

// writeInvestmentWithTrades responds with the holding as it stands after a
// trade mutation, so the client can update its numbers without a second
// round trip.
func (s *Server) writeInvestmentWithTrades(w http.ResponseWriter, id int64) {
	row := s.DB.QueryRow(`SELECT`+investmentColumns+` FROM investments WHERE id = ?`, id)
	inv, err := scanInvestment(row.Scan)
	if err != nil {
		httpx.WriteError(w, 404, "holding not found")
		return
	}
	inv.HasTrades = investmentHasTrades(s.DB, id)
	one := []models.Investment{inv}
	attachINR(one, inrRates(s.DB))
	httpx.WriteJSON(w, 200, one[0])
}
