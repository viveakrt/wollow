package moneyapi

import (
	"net/http"
	"strconv"

	"wollow/backend/internal/money/ingest"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

// handleListEmailAccounts lists the mailboxes connected on the Mail side,
// annotated with when Money last read finance mail out of each. Connecting and
// disconnecting a mailbox happens at /api/mail/accounts — there is one
// credential store, and deleting a mailbox from here would discard the user's
// whole message index.
func (s *Server) handleListEmailAccounts(w http.ResponseWriter, r *http.Request) {
	// Money no longer keeps a cursor: its backlog is "indexed messages with no
	// message_links row", so progress is read straight off the link table.
	rows, err := s.DB.Query(`
		SELECT a.id, a.username, a.imap_host, a.imap_port, a.enabled, a.created_at,
		       COALESCE((SELECT MAX(created_at) FROM message_links l
		                 WHERE l.mail_account_id = a.id), ''),
		       COALESCE((SELECT MAX(uid) FROM message_links l
		                 WHERE l.mail_account_id = a.id), 0)
		FROM mail_accounts a
		ORDER BY a.id`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	accounts := []models.EmailAccount{}
	for rows.Next() {
		var a models.EmailAccount
		var enabled int
		if err := rows.Scan(&a.ID, &a.Email, &a.IMAPHost, &a.IMAPPort, &enabled, &a.CreatedAt,
			&a.LastSyncedAt, &a.LastUID); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		a.Enabled = enabled == 1
		accounts = append(accounts, a)
	}
	httpx.WriteJSON(w, 200, accounts)
}

// handleSyncEmailAccount runs a finance ingest pass over one mailbox's already
// indexed mail. It does not fetch new mail — Mail's sync pass owns that — so if
// the index is stale the honest answer is "nothing new", not a second IMAP
// session racing the first.
func (s *Server) handleSyncEmailAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}

	var exists int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM mail_accounts WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
		httpx.WriteError(w, 404, "mailbox not found")
		return
	}

	var result *ingest.Result
	err = s.withMailSession(r.Context(), id, func(fetcher ingest.RawFetcher) error {
		var runErr error
		result, runErr = ingest.Run(r.Context(), s.DB, fetcher, id, "INBOX")
		return runErr
	})
	if err != nil {
		httpx.WriteError(w, 502, "ingest failed: "+err.Error())
		return
	}

	httpx.WriteJSON(w, 200, result)
}

// rescanResult is handleSyncEmailAccount's result with one extra figure: how
// many stale links this pass cleared before rescanning, so the UI can say
// "recovered 39 transactions" instead of just "found 39" as if they were new.
type rescanResult struct {
	Cleared int64 `json:"cleared"`
	*ingest.Result
}

// handleRescanEmailAccount repairs the ways a message_links row goes stale: a
// transaction/bill/trade whose account or holding was later deleted
// (recreating it should bring the old mail back), a message held for an
// account that has since been added, and a message that landed
// 'unrecognized' before a rule for it existed. Once a message is linked, an
// ordinary sync never looks at it again, so this clears exactly those stuck
// links (see ingest.RescanStuck — never a link still pointing at a real row)
// and immediately re-ingests them, recovering everything fixable in one
// action.
func (s *Server) handleRescanEmailAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}

	var exists int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM mail_accounts WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
		httpx.WriteError(w, 404, "mailbox not found")
		return
	}

	cleared, err := ingest.RescanStuck(s.DB, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	var result *ingest.Result
	err = s.withMailSession(r.Context(), id, func(fetcher ingest.RawFetcher) error {
		var runErr error
		result, runErr = ingest.Run(r.Context(), s.DB, fetcher, id, "INBOX")
		return runErr
	})
	if err != nil {
		httpx.WriteError(w, 502, "rescan failed: "+err.Error())
		return
	}

	httpx.WriteJSON(w, 200, rescanResult{Cleared: cleared, Result: result})
}
