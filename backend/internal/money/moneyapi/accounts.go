package moneyapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/models"
)

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT id, name, bank, account_type, account_number, currency,
		       opening_balance, current_balance, credit_limit, ifsc, branch,
		       source, include_in_networth, created_at, updated_at
		FROM finance_accounts ORDER BY id`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	accounts := []models.Account{}
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Bank, &a.AccountType, &a.AccountNumber,
			&a.Currency, &a.OpeningBalance, &a.CurrentBalance, &a.CreditLimit, &a.IFSC,
			&a.Branch, &a.Source, &a.IncludeInNetWorth, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	var a models.Account
	err = s.DB.QueryRow(`
		SELECT id, name, bank, account_type, account_number, currency,
		       opening_balance, current_balance, credit_limit, ifsc, branch,
		       source, include_in_networth, created_at, updated_at
		FROM finance_accounts WHERE id = ?`, id).Scan(
		&a.ID, &a.Name, &a.Bank, &a.AccountType, &a.AccountNumber,
		&a.Currency, &a.OpeningBalance, &a.CurrentBalance, &a.CreditLimit, &a.IFSC,
		&a.Branch, &a.Source, &a.IncludeInNetWorth, &a.CreatedAt, &a.UpdatedAt)
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

	var oldOpening float64
	if err := s.DB.QueryRow(
		`SELECT opening_balance FROM finance_accounts WHERE id=?`, id,
	).Scan(&oldOpening); err != nil {
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
	if a.OpeningBalance != oldOpening {
		if err := ledger.RecomputeAccountBalance(s.DB, id); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
	}
	a.ID = id
	httpx.WriteJSON(w, 200, a)
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
