package moneyapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wollow/backend/internal/money/marketdata"
	"wollow/backend/internal/platform/httpx"
)

// handleRefreshPrices fetches live prices and exchange rates now, for every holding or only ?id=.
func (s *Server) handleRefreshPrices(w http.ResponseWriter, r *http.Request) {
	var onlyID int64
	if raw := r.URL.Query().Get("id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			httpx.WriteError(w, 400, "invalid id")
			return
		}
		onlyID = id
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	res, err := s.Market.Run(ctx, onlyID)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, res)
}

// handleMarketSearch looks instruments up by name, ticker or ISIN, for choosing a holding's symbol.
func (s *Server) handleMarketSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 {
		httpx.WriteJSON(w, 200, []marketdata.SearchHit{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hits, err := s.Market.Source.Search(ctx, query)
	if err != nil {
		httpx.WriteError(w, 502, "search failed: "+err.Error())
		return
	}
	httpx.WriteJSON(w, 200, hits)
}
