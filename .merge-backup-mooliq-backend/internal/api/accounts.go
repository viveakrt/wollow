package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"mooliq/backend/internal/models"
)

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT id, name, bank, account_type, account_number, currency,
		       opening_balance, current_balance, ifsc, branch, created_at, updated_at
		FROM accounts ORDER BY id`)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	accounts := []models.Account{}
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Bank, &a.AccountType, &a.AccountNumber,
			&a.Currency, &a.OpeningBalance, &a.CurrentBalance, &a.IFSC, &a.Branch,
			&a.CreatedAt, &a.UpdatedAt); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		accounts = append(accounts, a)
	}
	writeJSON(w, 200, accounts)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	var a models.Account
	err = s.DB.QueryRow(`
		SELECT id, name, bank, account_type, account_number, currency,
		       opening_balance, current_balance, ifsc, branch, created_at, updated_at
		FROM accounts WHERE id = ?`, id).Scan(
		&a.ID, &a.Name, &a.Bank, &a.AccountType, &a.AccountNumber,
		&a.Currency, &a.OpeningBalance, &a.CurrentBalance, &a.IFSC, &a.Branch,
		&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		writeError(w, 404, "account not found")
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var a models.Account
	if err := decodeJSON(r, &a); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if a.Name == "" {
		writeError(w, 400, "name is required")
		return
	}
	if a.Currency == "" {
		a.Currency = "INR"
	}
	a.CurrentBalance = a.OpeningBalance

	res, err := s.DB.Exec(`
		INSERT INTO accounts (name, bank, account_type, account_number, currency,
		                       opening_balance, current_balance, ifsc, branch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Bank, a.AccountType, a.AccountNumber, a.Currency,
		a.OpeningBalance, a.CurrentBalance, a.IFSC, a.Branch)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	a.ID = id
	writeJSON(w, 201, a)
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	var a models.Account
	if err := decodeJSON(r, &a); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	_, err = s.DB.Exec(`
		UPDATE accounts SET name=?, bank=?, account_type=?, account_number=?,
		       currency=?, ifsc=?, branch=?, updated_at=datetime('now')
		WHERE id=?`,
		a.Name, a.Bank, a.AccountType, a.AccountNumber, a.Currency, a.IFSC, a.Branch, id)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	a.ID = id
	writeJSON(w, 200, a)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM accounts WHERE id=?`, id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

// handleBulkDeleteAccounts deletes multiple accounts at once. Transactions
// under each account cascade-delete via the accounts(id) ON DELETE CASCADE
// foreign key, same as single-account delete.
func (s *Server) handleBulkDeleteAccounts(w http.ResponseWriter, r *http.Request) {
	var req bulkDeleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, 400, "ids is required and must be non-empty")
		return
	}

	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs))
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i] = id
	}

	res, err := s.DB.Exec(fmt.Sprintf(`DELETE FROM accounts WHERE id IN (%s)`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	deleted, _ := res.RowsAffected()
	writeJSON(w, 200, bulkDeleteResponse{Deleted: int(deleted)})
}
