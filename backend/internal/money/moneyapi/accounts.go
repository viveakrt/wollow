package moneyapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wollow/backend/internal/money/ingest"
	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

const accountColumns = `
	id, name, bank, account_type, account_number, currency,
	opening_balance, current_balance, credit_limit, ifsc, branch,
	source, include_in_networth, archived_at, created_at, updated_at`

type accountScanner interface {
	Scan(dest ...interface{}) error
}

func scanAccount(row accountScanner) (models.Account, error) {
	var a models.Account
	err := row.Scan(&a.ID, &a.Name, &a.Bank, &a.AccountType, &a.AccountNumber,
		&a.Currency, &a.OpeningBalance, &a.CurrentBalance, &a.CreditLimit, &a.IFSC,
		&a.Branch, &a.Source, &a.IncludeInNetWorth, &a.ArchivedAt, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *Server) loadAccount(id int64) (models.Account, error) {
	return scanAccount(s.DB.QueryRow(`SELECT `+accountColumns+` FROM finance_accounts WHERE id = ?`, id))
}

// handleListAccounts lists active accounts; archived ones only on request,
// since retiring an account is precisely a wish to stop seeing it.
func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	where := `WHERE archived_at = ''`
	if r.URL.Query().Get("includeArchived") == "1" {
		where = ``
	}
	rows, err := s.DB.Query(`SELECT ` + accountColumns + ` FROM finance_accounts ` + where + ` ORDER BY id`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	accounts := []models.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		accounts = append(accounts, a)
	}
	httpx.WriteJSON(w, 200, accounts)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	a, err := s.loadAccount(id)
	if err != nil {
		httpx.WriteError(w, 404, "account not found")
		return
	}
	httpx.WriteJSON(w, 200, a)
}

// accountPayload wraps the account fields whose absence must be
// distinguishable from an explicit false/zero. A client that never mentions
// includeInNetworth gets the default (true); only an explicit false excludes
// the account from net worth.
type accountPayload struct {
	models.Account
	IncludeInNetWorthOpt *bool `json:"includeInNetworth"`
}

func (p *accountPayload) resolve() models.Account {
	a := p.Account
	a.IncludeInNetWorth = p.IncludeInNetWorthOpt == nil || *p.IncludeInNetWorthOpt
	return a
}

// lastFour is the tail of an account number as mail states it, whatever
// masking or spacing the user typed.
func lastFour(number string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, number)
	if len(digits) < 4 {
		return ""
	}
	return digits[len(digits)-4:]
}

// releasePending lets mail that was waiting for this account be read again on
// the next sync.
func (s *Server) releasePending(a models.Account) {
	ingest.ClearPendingFor(s.DB, a.Bank, lastFour(a.AccountNumber))
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var payload accountPayload
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	a := payload.resolve()
	if a.Name == "" {
		httpx.WriteError(w, 400, "name is required")
		return
	}
	if a.AccountType == "" {
		a.AccountType = "bank"
	}
	if a.Currency == "" {
		a.Currency = "INR"
	}
	a.CurrentBalance = a.OpeningBalance

	res, err := s.DB.Exec(`
		INSERT INTO finance_accounts (name, bank, account_type, account_number, currency,
		                       opening_balance, current_balance, credit_limit, ifsc, branch,
		                       source, include_in_networth)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'manual', ?)`,
		a.Name, a.Bank, a.AccountType, a.AccountNumber, a.Currency,
		a.OpeningBalance, a.CurrentBalance, a.CreditLimit, a.IFSC, a.Branch,
		a.IncludeInNetWorth)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	a.ID = id
	// Mirror what was actually stored: the client caches this response, and a
	// blank source here reads as an account of unknown origin.
	a.Source = "manual"

	// Mail that named this account before it existed was held, not dropped;
	// the next sync now imports it.
	s.releasePending(a)
	httpx.WriteJSON(w, 201, a)
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var payload accountPayload
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	a := payload.resolve()

	before, err := s.loadAccount(id)
	if err != nil {
		httpx.WriteError(w, 404, "account not found")
		return
	}

	_, err = s.DB.Exec(`
		UPDATE finance_accounts SET name=?, bank=?, account_type=?, account_number=?,
		       currency=?, opening_balance=?, credit_limit=?, ifsc=?, branch=?,
		       include_in_networth=?, updated_at=datetime('now')
		WHERE id=?`,
		a.Name, a.Bank, a.AccountType, a.AccountNumber, a.Currency, a.OpeningBalance,
		a.CreditLimit, a.IFSC, a.Branch, a.IncludeInNetWorth, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	// The user corrected the opening balance — everything email or statement
	// data implied downstream of it must be re-derived. Their correction wins:
	// imported data is a helper here, not the source of truth.
	if a.OpeningBalance != before.OpeningBalance {
		if err := ledger.RecomputeAccountBalance(s.DB, id); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
	}
	// A changed identity may be exactly what held mail was waiting for.
	if a.Bank != before.Bank || a.AccountNumber != before.AccountNumber || a.AccountType != before.AccountType {
		s.releasePending(a)
	}

	updated, err := s.loadAccount(id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, updated)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM finance_accounts WHERE id=?`, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// handleBulkDeleteAccounts deletes multiple accounts at once. Transactions
// under each account cascade-delete via the accounts(id) ON DELETE CASCADE
// foreign key, same as single-account delete.
func (s *Server) handleBulkDeleteAccounts(w http.ResponseWriter, r *http.Request) {
	var req bulkDeleteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteError(w, 400, "ids is required and must be non-empty")
		return
	}

	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs))
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i] = id
	}

	res, err := s.DB.Exec(fmt.Sprintf(`DELETE FROM finance_accounts WHERE id IN (%s)`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	deleted, _ := res.RowsAffected()
	httpx.WriteJSON(w, 200, bulkDeleteResponse{Deleted: int(deleted)})
}

// handleArchiveAccount retires an account: it leaves the accounts page and
// the dashboard and stops receiving mail, but its history stays, unlike a
// delete, which cascades the transactions away.
func (s *Server) handleArchiveAccount(w http.ResponseWriter, r *http.Request) {
	s.setArchived(w, r, true)
}

func (s *Server) handleUnarchiveAccount(w http.ResponseWriter, r *http.Request) {
	s.setArchived(w, r, false)
}

func (s *Server) setArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	stamp := ""
	if archived {
		stamp = time.Now().UTC().Format(time.RFC3339)
	}
	res, err := s.DB.Exec(`UPDATE finance_accounts SET archived_at = ?, updated_at = datetime('now') WHERE id = ?`, stamp, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "account not found")
		return
	}
	a, err := s.loadAccount(id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if !archived {
		s.releasePending(a)
	}
	httpx.WriteJSON(w, 200, a)
}

type setBalanceRequest struct {
	// Amount is the figure as the user sees it: what a card has outstanding,
	// what a bank account holds. The sign convention of the ledger (debt is
	// negative) is applied here so the client never has to know it.
	Amount float64 `json:"amount"`
	AsOf   string  `json:"asOf"`
}

// handleSetAccountBalance records a balance the user states, as a manual
// snapshot: the running balance anchors on it and only later transactions
// move it, exactly as a bank-reported balance would.
func (s *Server) handleSetAccountBalance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var req setBalanceRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	a, err := s.loadAccount(id)
	if err != nil {
		httpx.WriteError(w, 404, "account not found")
		return
	}
	asOf := req.AsOf
	if asOf == "" {
		asOf = time.Now().Format("2006-01-02")
	}
	value := req.Amount
	if liabilityTypes[a.AccountType] {
		value = -req.Amount
	}
	if err := ledger.RecordBalanceSnapshot(s.DB, id, asOf, value, "manual"); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	a, err = s.loadAccount(id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, a)
}

// pendingAccountGroup is one account that mail keeps naming but nobody has
// registered, with everything needed to create it in one click.
type pendingAccountGroup struct {
	Issuer        string `json:"issuer"`
	Name          string `json:"name"`
	Last4         string `json:"last4"`
	Kind          string `json:"kind"`
	Count         int    `json:"count"`
	LastSeen      string `json:"lastSeen"`
	LatestSubject string `json:"latestSubject"`
	Sample        struct {
		MailAccountID int64  `json:"mailAccountId"`
		UID           uint32 `json:"uid"`
	} `json:"sample"`
}

// handleListPendingAccounts groups the messages held for a missing account by
// the account they name.
func (s *Server) handleListPendingAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT l.pending_issuer, l.pending_name, l.pending_last4, l.pending_kind,
		       COUNT(*), MAX(l.received_at), MIN(l.mail_account_id), MIN(l.uid),
		       (SELECT subject FROM message_links l2
		         WHERE l2.parsed_as = 'pending_account'
		           AND l2.pending_issuer = l.pending_issuer AND l2.pending_last4 = l.pending_last4
		           AND l2.pending_kind = l.pending_kind
		         ORDER BY l2.received_at DESC, l2.id DESC LIMIT 1)
		FROM message_links l
		WHERE l.parsed_as = 'pending_account'
		GROUP BY l.pending_issuer, l.pending_name, l.pending_last4, l.pending_kind
		ORDER BY COUNT(*) DESC, MAX(l.received_at) DESC`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	groups := []pendingAccountGroup{}
	for rows.Next() {
		var g pendingAccountGroup
		var subject sql.NullString
		if err := rows.Scan(&g.Issuer, &g.Name, &g.Last4, &g.Kind, &g.Count, &g.LastSeen,
			&g.Sample.MailAccountID, &g.Sample.UID, &subject); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		g.LatestSubject = subject.String
		groups = append(groups, g)
	}
	httpx.WriteJSON(w, 200, groups)
}
