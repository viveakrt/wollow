package moneyapi

import (
	"database/sql"
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
		result, runErr = ingest.RunWithPasswords(r.Context(), s.DB, fetcher, id, "INBOX", s.PDFPasswordLookup())
		return runErr
	})
	if err != nil {
		httpx.WriteError(w, 502, "ingest failed: "+err.Error())
		return
	}

	httpx.WriteJSON(w, 200, result)
}

func (s *Server) handleListBills(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT id, account_id, issuer, card_last4, statement_period, total_due, minimum_due, due_date, status, created_at
		FROM bills ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	bills := []models.Bill{}
	for rows.Next() {
		var b models.Bill
		var accountID sql.NullInt64
		var totalDue, minDue sql.NullFloat64
		if err := rows.Scan(&b.ID, &accountID, &b.Issuer, &b.CardLast4, &b.StatementPeriod,
			&totalDue, &minDue, &b.DueDate, &b.Status, &b.CreatedAt); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		if accountID.Valid {
			b.AccountID = &accountID.Int64
		}
		if totalDue.Valid {
			b.TotalDue = &totalDue.Float64
		}
		if minDue.Valid {
			b.MinimumDue = &minDue.Float64
		}
		bills = append(bills, b)
	}
	httpx.WriteJSON(w, 200, bills)
}
