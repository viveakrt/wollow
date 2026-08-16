package moneyapi

import (
	"net/http"
	"strings"

	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/platform/httpx"
)

// handleListFXRates returns every known rate with its provenance, so a number
// that moves net worth can always be traced to either the user or a specific
// transaction of theirs.
func (s *Server) handleListFXRates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`SELECT currency, inr_per_unit, as_of, source, note
		FROM fx_rates ORDER BY currency`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	rates := []ledger.Rate{}
	for rows.Next() {
		var rate ledger.Rate
		if err := rows.Scan(&rate.Currency, &rate.INRPerUnit, &rate.AsOf, &rate.Source, &rate.Note); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		rates = append(rates, rate)
	}
	httpx.WriteJSON(w, 200, rates)
}

type setFXRateRequest struct {
	Currency   string  `json:"currency"`
	INRPerUnit float64 `json:"inrPerUnit"`
	AsOf       string  `json:"asOf"`
}

// handleSetFXRate records a rate the user chose, which from then on outranks
// anything derived from their transactions.
func (s *Server) handleSetFXRate(w http.ResponseWriter, r *http.Request) {
	var req setFXRateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if strings.TrimSpace(req.Currency) == "" {
		httpx.WriteError(w, 400, "currency is required")
		return
	}
	if err := ledger.SetRate(s.DB, req.Currency, req.INRPerUnit, req.AsOf, "manual", "set by you"); err != nil {
		httpx.WriteError(w, 400, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"saved": true})
}
