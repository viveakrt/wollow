package moneyapi

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wollow/backend/internal/money/ledger"

	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

// investmentColumns keeps the list, get and write paths reading the same shape.
const investmentColumns = `
	id, account_id, kind, institution, name, identifier, currency,
	invested_amount, current_value, maturity_amount, interest_rate, units,
	last_price, last_price_at,
	start_date, maturity_date, status, source, notes, created_at, updated_at`

func scanInvestment(scan func(...any) error) (models.Investment, error) {
	var (
		inv       models.Investment
		accountID sql.NullInt64
		maturity  sql.NullFloat64
		rate      sql.NullFloat64
		units     sql.NullFloat64
		lastPrice sql.NullFloat64
	)
	err := scan(&inv.ID, &accountID, &inv.Kind, &inv.Institution, &inv.Name, &inv.Identifier,
		&inv.Currency, &inv.InvestedAmount, &inv.CurrentValue, &maturity, &rate, &units,
		&lastPrice, &inv.LastPriceAt,
		&inv.StartDate, &inv.MaturityDate, &inv.Status, &inv.Source, &inv.Notes,
		&inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		return inv, err
	}
	if lastPrice.Valid {
		inv.LastPrice = &lastPrice.Float64
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
}

func (s *Server) handleInvestmentSummary(w http.ResponseWriter, r *http.Request) {
	summary := investmentSummary{
		ByKind:       []investmentKindTotal{},
		ByCurrency:   []investmentCurrencyTotal{},
		MaturingSoon: []models.Investment{},
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

	httpx.WriteJSON(w, 200, summary)
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

	res, err := s.DB.Exec(`
		INSERT INTO investments
			(account_id, kind, institution, name, identifier, currency, invested_amount,
			 current_value, maturity_amount, interest_rate, units, start_date, maturity_date,
			 status, source, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'manual', ?)`,
		inv.AccountID, inv.Kind, inv.Institution, inv.Name, inv.Identifier, inv.Currency,
		inv.InvestedAmount, inv.CurrentValue, inv.MaturityAmount, inv.InterestRate, inv.Units,
		inv.StartDate, inv.MaturityDate, inv.Status, inv.Notes)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	inv.ID, _ = res.LastInsertId()
	inv.Source = "manual"
	httpx.WriteJSON(w, 201, inv)
}

func (s *Server) handleUpdateInvestment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var inv models.Investment
	if err := httpx.DecodeJSON(r, &inv); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	applyInvestmentDefaults(&inv)

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
	inv.ID = id
	httpx.WriteJSON(w, 200, inv)
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
		       trade_date, order_type, source, created_at
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
		if err := rows.Scan(&t.ID, &t.InvestmentID, &t.Side, &t.Shares, &t.Price, &t.Amount,
			&t.Currency, &t.TradeDate, &t.OrderType, &t.Source, &t.CreatedAt); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		trades = append(trades, t)
	}
	httpx.WriteJSON(w, 200, trades)
}

type setPriceRequest struct {
	Price float64 `json:"price"`
	AsOf  string  `json:"asOf"`
}

// handleSetInvestmentPrice records the current per-unit price of a holding and
// re-values it.
//
// Prices are entered rather than fetched: Money has no market data feed, and
// inventing one would mean shipping a number nobody can check. With a price
// the holding reports a real gain; without one it reports its cost and says as
// much through `priced`.
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
	if err := ledger.SetHoldingPrice(s.DB, id, req.Price, req.AsOf); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	var inv models.Investment
	row := s.DB.QueryRow(`SELECT`+investmentColumns+` FROM investments WHERE id = ?`, id)
	inv, err = scanInvestment(row.Scan)
	if err != nil {
		httpx.WriteError(w, 404, "holding not found")
		return
	}
	httpx.WriteJSON(w, 200, inv)
}
