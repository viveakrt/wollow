package api

import (
	"net/http"
	"strconv"
)

// Insights are whole-mailbox aggregates computed in SQL. They must never be
// derived from the currently loaded page in the client, which would understate
// every count.
type insightsResponse struct {
	Totals     insightTotals   `json:"totals"`
	Categories []insightCount  `json:"categories"`
	Priorities []insightCount  `json:"priorities"`
	Senders    []insightSender `json:"senders"`
	SmartViews map[string]int  `json:"smartViews"`
}

type insightTotals struct {
	Messages   int `json:"messages"`
	Unread     int `json:"unread"`
	Flagged    int `json:"flagged"`
	Classified int `json:"classified"`
}

type insightCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type insightSender struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Count    int    `json:"count"`
	LastSeen string `json:"lastSeen"`
}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	out := insightsResponse{
		Categories: []insightCount{},
		Priorities: []insightCount{},
		Senders:    []insightSender{},
		SmartViews: map[string]int{},
	}

	row := s.DB.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN seen = 0 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN flagged = 1 THEN 1 ELSE 0 END), 0)
		FROM messages WHERE account_id = ? AND folder = ?`, id, folder)
	if err := row.Scan(&out.Totals.Messages, &out.Totals.Unread, &out.Totals.Flagged); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute totals")
		return
	}

	_ = s.DB.QueryRow(`
		SELECT COUNT(*) FROM classifications c
		JOIN messages m ON m.id = c.message_id
		WHERE m.account_id = ? AND m.folder = ?`, id, folder).Scan(&out.Totals.Classified)

	out.Categories = s.groupCount(id, folder, "c.category")
	out.Priorities = s.groupCount(id, folder, "c.priority")

	// Top senders by volume — the basis for sender grouping and bulk actions.
	rows, err := s.DB.Query(`
		SELECT m.from_email,
		       COALESCE(MAX(m.from_name), ''),
		       COALESCE(MAX(m.from_domain), ''),
		       COUNT(*) AS n,
		       MAX(m.date)
		FROM messages m
		WHERE m.account_id = ? AND m.folder = ? AND m.from_email != ''
		GROUP BY m.from_email
		ORDER BY n DESC
		LIMIT 10`, id, folder)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s insightSender
			if err := rows.Scan(&s.Email, &s.Name, &s.Domain, &s.Count, &s.LastSeen); err == nil {
				out.Senders = append(out.Senders, s)
			}
		}
	}

	for name, clause := range smartViewClauses {
		var n int
		query := `SELECT COUNT(*) FROM messages m
			LEFT JOIN classifications c ON c.message_id = m.id
			WHERE m.account_id = ? AND m.folder = ? AND ` + clause
		if err := s.DB.QueryRow(query, id, folder).Scan(&n); err == nil {
			out.SmartViews[name] = n
		}
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) groupCount(accountID int64, folder, column string) []insightCount {
	out := []insightCount{}
	// column is never user input — callers pass a literal.
	rows, err := s.DB.Query(`
		SELECT `+column+`, COUNT(*) AS n
		FROM classifications c
		JOIN messages m ON m.id = c.message_id
		WHERE m.account_id = ? AND m.folder = ?
		GROUP BY `+column+`
		ORDER BY n DESC`, accountID, folder)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c insightCount
		if err := rows.Scan(&c.Key, &c.Count); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// smartViewClauses map a view name to its SQL predicate. Keeping these
// server-side means counts and the filtered list can never disagree.
var smartViewClauses = map[string]string{
	"unread":       `m.seen = 0`,
	"starred":      `m.flagged = 1`,
	"needs_action": `c.action IS NOT NULL AND c.action NOT IN ('no_action','read_only')`,
	"needs_reply":  `c.requires_response = 1`,
	"important":    `c.priority IN ('critical','high')`,
	"bills":        `c.category = 'bills_payments' OR c.requires_payment = 1`,
	"orders":       `c.category = 'orders_delivery'`,
	"travel":       `c.category = 'travel_booking'`,
	"newsletters":  `c.category = 'newsletters'`,
	"promotions":   `c.category = 'marketing_promotions'`,
	"security":     `c.category = 'security' OR c.is_security_alert = 1`,
	"finance":      `c.category = 'finance'`,
}
