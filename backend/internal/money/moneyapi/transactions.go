package moneyapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"wollow/backend/internal/platform/httpx"

	"github.com/google/uuid"
	"wollow/backend/internal/money/models"
)

const txnSelectCols = `
	t.id, t.account_id, a.name, t.txn_date, t.value_date, t.narration, t.ref_no,
	t.withdrawal_amt, t.deposit_amt, t.closing_balance, t.type, t.category_id,
	COALESCE(c.name, ''), COALESCE(c.color, ''), t.merchant, t.payment_method, t.notes,
	t.linked_txn_id, t.created_at`

func scanTxn(row interface{ Scan(...interface{}) error }) (models.Transaction, error) {
	var t models.Transaction
	var closing sql.NullFloat64
	var categoryID sql.NullInt64
	var linkedTxnID sql.NullInt64
	err := row.Scan(&t.ID, &t.AccountID, &t.AccountName, &t.TxnDate, &t.ValueDate, &t.Narration,
		&t.RefNo, &t.WithdrawalAmt, &t.DepositAmt, &closing, &t.Type, &categoryID,
		&t.CategoryName, &t.CategoryColor, &t.Merchant, &t.PaymentMethod, &t.Notes,
		&linkedTxnID, &t.CreatedAt)
	if closing.Valid {
		t.ClosingBalance = &closing.Float64
	}
	if categoryID.Valid {
		t.CategoryID = &categoryID.Int64
	}
	if linkedTxnID.Valid {
		t.LinkedTxnID = &linkedTxnID.Int64
	}
	return t, err
}

func (s *Server) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []interface{}{}

	if accID := q.Get("accountId"); accID != "" {
		where = append(where, "t.account_id = ?")
		args = append(args, accID)
	}
	if from := q.Get("from"); from != "" {
		where = append(where, "t.txn_date >= ?")
		args = append(args, from)
	}
	if to := q.Get("to"); to != "" {
		where = append(where, "t.txn_date <= ?")
		args = append(args, to)
	}
	if catID := q.Get("categoryId"); catID != "" {
		where = append(where, "t.category_id = ?")
		args = append(args, catID)
	}
	if typ := q.Get("type"); typ != "" {
		where = append(where, "t.type = ?")
		args = append(args, typ)
	}
	if search := q.Get("search"); search != "" {
		where = append(where, "(t.narration LIKE ? OR t.merchant LIKE ?)")
		like := "%" + search + "%"
		args = append(args, like, like)
	}

	limit := 100
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 1000 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(q.Get("offset")); err == nil && o >= 0 {
		offset = o
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM transactions t
		JOIN finance_accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE %s
		ORDER BY t.txn_date DESC, t.id DESC
		LIMIT ? OFFSET ?`, txnSelectCols, strings.Join(where, " AND "))
	args = append(args, limit, offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	txns := []models.Transaction{}
	for rows.Next() {
		t, err := scanTxn(rows)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		txns = append(txns, t)
	}
	httpx.WriteJSON(w, 200, txns)
}

func (s *Server) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	query := fmt.Sprintf(`
		SELECT %s FROM transactions t
		JOIN finance_accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.id = ?`, txnSelectCols)
	t, err := scanTxn(s.DB.QueryRow(query, id))
	if err != nil {
		httpx.WriteError(w, 404, "transaction not found")
		return
	}
	httpx.WriteJSON(w, 200, t)
}

func (s *Server) handleCreateTransaction(w http.ResponseWriter, r *http.Request) {
	var t models.Transaction
	if err := httpx.DecodeJSON(r, &t); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if t.AccountID == 0 || t.TxnDate == "" {
		httpx.WriteError(w, 400, "accountId and txnDate are required")
		return
	}
	if t.Type == "" {
		if t.DepositAmt > 0 {
			t.Type = "income"
		} else {
			t.Type = "expense"
		}
	}
	if t.ValueDate == "" {
		t.ValueDate = t.TxnDate
	}

	// Manually-created transactions have no natural dedupe key (unlike
	// statement-import rows, where identical hash = identical source row =
	// true duplicate) — the account_id+dedupe_hash unique index would
	// otherwise collide on the second manual entry with matching fields.
	// A random hash makes every manual entry its own row, as intended.
	dedupeHash := "manual-" + uuid.NewString()

	res, err := s.DB.Exec(`
		INSERT INTO transactions (account_id, txn_date, value_date, narration, ref_no,
			withdrawal_amt, deposit_amt, type, category_id, merchant, payment_method, notes, dedupe_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.AccountID, t.TxnDate, t.ValueDate, t.Narration, t.RefNo,
		t.WithdrawalAmt, t.DepositAmt, t.Type, t.CategoryID, t.Merchant, t.PaymentMethod, t.Notes, dedupeHash)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	if err := recomputeAccountBalance(s.DB, t.AccountID); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	t.ID = id
	httpx.WriteJSON(w, 201, t)
}

func (s *Server) handleUpdateTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var t models.Transaction
	if err := httpx.DecodeJSON(r, &t); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if t.AccountID == 0 || t.TxnDate == "" {
		httpx.WriteError(w, 400, "accountId and txnDate are required")
		return
	}
	if t.Type == "" {
		if t.DepositAmt > 0 {
			t.Type = "income"
		} else {
			t.Type = "expense"
		}
	}
	if t.ValueDate == "" {
		t.ValueDate = t.TxnDate
	}

	var oldAccountID int64
	if err := s.DB.QueryRow(`SELECT account_id FROM transactions WHERE id=?`, id).Scan(&oldAccountID); err != nil {
		httpx.WriteError(w, 404, "transaction not found")
		return
	}

	_, err = s.DB.Exec(`
		UPDATE transactions SET account_id=?, txn_date=?, value_date=?, narration=?, ref_no=?,
			withdrawal_amt=?, deposit_amt=?, type=?, category_id=?, merchant=?,
			payment_method=?, notes=?
		WHERE id=?`,
		t.AccountID, t.TxnDate, t.ValueDate, t.Narration, t.RefNo, t.WithdrawalAmt, t.DepositAmt,
		t.Type, t.CategoryID, t.Merchant, t.PaymentMethod, t.Notes, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	recomputeAccountBalance(s.DB, t.AccountID)
	if oldAccountID != t.AccountID {
		recomputeAccountBalance(s.DB, oldAccountID)
	}
	t.ID = id
	httpx.WriteJSON(w, 200, t)
}

func (s *Server) handleDeleteTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var accountID int64
	s.DB.QueryRow(`SELECT account_id FROM transactions WHERE id=?`, id).Scan(&accountID)

	if _, err := s.DB.Exec(`DELETE FROM transactions WHERE id=?`, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if accountID != 0 {
		recomputeAccountBalance(s.DB, accountID)
	}
	w.WriteHeader(204)
}

type bulkDeleteRequest struct {
	IDs []int64 `json:"ids"`
}

type bulkDeleteResponse struct {
	Deleted int `json:"deleted"`
}

// handleBulkDeleteTransactions deletes many transactions in one request and
// recomputes the balance of every account touched (a bulk selection can span
// multiple accounts).
func (s *Server) handleBulkDeleteTransactions(w http.ResponseWriter, r *http.Request) {
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
	inClause := strings.Join(placeholders, ",")

	affectedAccounts := map[int64]bool{}
	rows, err := s.DB.Query(fmt.Sprintf(`SELECT DISTINCT account_id FROM transactions WHERE id IN (%s)`, inClause), args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for rows.Next() {
		var accID int64
		if err := rows.Scan(&accID); err == nil {
			affectedAccounts[accID] = true
		}
	}
	rows.Close()

	res, err := s.DB.Exec(fmt.Sprintf(`DELETE FROM transactions WHERE id IN (%s)`, inClause), args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	deleted, _ := res.RowsAffected()

	for accID := range affectedAccounts {
		recomputeAccountBalance(s.DB, accID)
	}

	httpx.WriteJSON(w, 200, bulkDeleteResponse{Deleted: int(deleted)})
}

type bulkCategorizeRequest struct {
	IDs        []int64 `json:"ids"`
	CategoryID *int64  `json:"categoryId"` // null clears the category
}

type bulkCategorizeResponse struct {
	Updated int `json:"updated"`
}

// handleBulkCategorizeTransactions applies one category to many transactions
// at once — the common "select rows, pick a category" bulk action. Does not
// touch balances since category has no effect on amounts.
func (s *Server) handleBulkCategorizeTransactions(w http.ResponseWriter, r *http.Request) {
	var req bulkCategorizeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteError(w, 400, "ids is required and must be non-empty")
		return
	}

	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, len(req.IDs)+1)
	args[0] = req.CategoryID
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args[i+1] = id
	}
	inClause := strings.Join(placeholders, ",")

	res, err := s.DB.Exec(fmt.Sprintf(`UPDATE transactions SET category_id=? WHERE id IN (%s)`, inClause), args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	updated, _ := res.RowsAffected()
	httpx.WriteJSON(w, 200, bulkCategorizeResponse{Updated: int(updated)})
}

type execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// recomputeAccountBalance sets accounts.current_balance = opening_balance + sum(deposits) - sum(withdrawals).
//
// opening_balance is also refreshed here, derived from the earliest known
// transaction's closing_balance (closing - deposit + withdrawal). This
// self-corrects when statements are imported out of chronological order:
// the account is first created from whichever statement is imported first
// (its opening_balance reflects that statement's period, not the account's
// true origin), and later a statement covering an earlier period gets
// imported. Without this, opening_balance keeps referring to the wrong
// period and current_balance double-counts everything before it.
// Falls back to the existing opening_balance when no transaction carries a
// closing balance yet (e.g. manually entered transactions only).
func recomputeAccountBalance(db execer, accountID int64) error {
	_, err := db.Exec(`
		UPDATE finance_accounts SET
			opening_balance = COALESCE((
				SELECT closing_balance - deposit_amt + withdrawal_amt
				FROM transactions
				WHERE account_id = ? AND closing_balance IS NOT NULL
				ORDER BY txn_date ASC, id ASC
				LIMIT 1
			), opening_balance),
			current_balance = COALESCE((
				SELECT closing_balance - deposit_amt + withdrawal_amt
				FROM transactions
				WHERE account_id = ? AND closing_balance IS NOT NULL
				ORDER BY txn_date ASC, id ASC
				LIMIT 1
			), opening_balance) + (
				SELECT COALESCE(SUM(deposit_amt),0) - COALESCE(SUM(withdrawal_amt),0)
				FROM transactions WHERE account_id = ?
			),
			updated_at = datetime('now')
		WHERE id = ?`, accountID, accountID, accountID, accountID)
	return err
}
