package moneyapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

// txnSelectCols, txnSelectJoins and scanTxn are a set: every query that builds
// a models.Transaction uses all three, so the column list and the scan can
// never drift apart. The trailing columns are the message_links join that lets
// a transaction point back at the email it came from.
const txnSelectCols = `
	t.id, t.account_id, a.name, t.txn_date, t.value_date, t.narration, t.ref_no,
	t.withdrawal_amt, t.deposit_amt, t.closing_balance, t.type, t.category_id,
	COALESCE(c.name, ''), COALESCE(c.color, ''), t.merchant, t.payment_method, t.notes,
	t.linked_txn_id, t.transfer_kind, t.counterparty, t.created_at,
	l.mail_account_id, l.uid, COALESCE(l.subject, ''), COALESCE(l.sender, ''),
	COALESCE(l.received_at, ''),
	tc.transaction_id, COALESCE(tc.category, ''), COALESCE(tc.subcategory, ''),
	COALESCE(tc.merchant, ''), COALESCE(tc.payment_method, ''), COALESCE(tc.nature, ''),
	COALESCE(tc.transfer_kind, ''), COALESCE(tc.counterparty, ''),
	COALESCE(tc.is_recurring, 0), COALESCE(tc.is_bill, 0), COALESCE(tc.is_refund, 0),
	COALESCE(tc.needs_review, 0), COALESCE(tc.confidence, 0), COALESCE(tc.summary, ''),
	COALESCE(tc.model, ''), COALESCE(tc.classified_at, ''), COALESCE(tc.applied, 0)`

const txnSelectJoins = `
	FROM transactions t
	JOIN finance_accounts a ON a.id = t.account_id
	LEFT JOIN categories c ON c.id = t.category_id
	LEFT JOIN message_links l ON l.transaction_id = t.id
	LEFT JOIN transaction_classifications tc ON tc.transaction_id = t.id`

func scanTxn(row interface{ Scan(...interface{}) error }) (models.Transaction, error) {
	var t models.Transaction
	var closing sql.NullFloat64
	var categoryID sql.NullInt64
	var linkedTxnID sql.NullInt64
	var mailAccountID sql.NullInt64
	var mailUID sql.NullInt64
	var subject, sender, receivedAt string
	var classifiedTxnID sql.NullInt64
	var ai models.TransactionClassification
	err := row.Scan(&t.ID, &t.AccountID, &t.AccountName, &t.TxnDate, &t.ValueDate, &t.Narration,
		&t.RefNo, &t.WithdrawalAmt, &t.DepositAmt, &closing, &t.Type, &categoryID,
		&t.CategoryName, &t.CategoryColor, &t.Merchant, &t.PaymentMethod, &t.Notes,
		&linkedTxnID, &t.TransferKind, &t.Counterparty, &t.CreatedAt,
		&mailAccountID, &mailUID, &subject, &sender, &receivedAt,
		&classifiedTxnID, &ai.Category, &ai.Subcategory, &ai.Merchant, &ai.PaymentMethod,
		&ai.Nature, &ai.TransferKind, &ai.Counterparty, &ai.IsRecurring, &ai.IsBill,
		&ai.IsRefund, &ai.NeedsReview, &ai.Confidence, &ai.Summary, &ai.Model,
		&ai.ClassifiedAt, &ai.Applied)
	if classifiedTxnID.Valid {
		t.AI = &ai
	}
	if closing.Valid {
		t.ClosingBalance = &closing.Float64
	}
	if categoryID.Valid {
		t.CategoryID = &categoryID.Int64
	}
	if linkedTxnID.Valid {
		t.LinkedTxnID = &linkedTxnID.Int64
	}
	if mailAccountID.Valid {
		t.SourceEmail = &models.SourceEmail{
			MailAccountID: mailAccountID.Int64,
			UID:           uint32(mailUID.Int64),
			Subject:       subject,
			Sender:        sender,
			ReceivedAt:    receivedAt,
		}
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
	if kind := q.Get("transferKind"); kind != "" {
		where = append(where, "t.transfer_kind = ?")
		args = append(args, kind)
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
		SELECT %s %s
		WHERE %s
		ORDER BY t.txn_date DESC, t.id DESC
		LIMIT ? OFFSET ?`, txnSelectCols, txnSelectJoins, strings.Join(where, " AND "))
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
	query := fmt.Sprintf(`SELECT %s %s WHERE t.id = ?`, txnSelectCols, txnSelectJoins)
	t, err := scanTxn(s.DB.QueryRow(query, id))
	if err != nil {
		httpx.WriteError(w, 404, "transaction not found")
		return
	}
	httpx.WriteJSON(w, 200, t)
}

// validTransferKinds are the transfer classifications the API accepts:
// between the user's own accounts, into an investment, or to a family member.
var validTransferKinds = map[string]bool{"self": true, "investment": true, "family": true}

// normalizeTransferFields keeps type and transfer_kind consistent: only
// transfers carry a kind (defaulting to 'self'), and a kind on an
// income/expense row is stripped rather than stored as junk.
func normalizeTransferFields(t *models.Transaction) {
	if t.Type != "transfer" {
		t.TransferKind = ""
		t.Counterparty = ""
		return
	}
	if !validTransferKinds[t.TransferKind] {
		t.TransferKind = "self"
	}
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
	normalizeTransferFields(&t)

	// Manually-created transactions have no natural dedupe key (unlike
	// statement-import rows, where identical hash = identical source row =
	// true duplicate) — the account_id+dedupe_hash unique index would
	// otherwise collide on the second manual entry with matching fields.
	// A random hash makes every manual entry its own row, as intended.
	dedupeHash := "manual-" + uuid.NewString()

	res, err := s.DB.Exec(`
		INSERT INTO transactions (account_id, txn_date, value_date, narration, ref_no,
			withdrawal_amt, deposit_amt, type, category_id, merchant, payment_method, notes,
			transfer_kind, counterparty, dedupe_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.AccountID, t.TxnDate, t.ValueDate, t.Narration, t.RefNo,
		t.WithdrawalAmt, t.DepositAmt, t.Type, t.CategoryID, t.Merchant, t.PaymentMethod, t.Notes,
		t.TransferKind, t.Counterparty, dedupeHash)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	if err := ledger.RecomputeAccountBalance(s.DB, t.AccountID); err != nil {
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
	normalizeTransferFields(&t)

	var oldAccountID int64
	var oldCategoryID sql.NullInt64
	if err := s.DB.QueryRow(`SELECT account_id, category_id FROM transactions WHERE id=?`, id).
		Scan(&oldAccountID, &oldCategoryID); err != nil {
		httpx.WriteError(w, 404, "transaction not found")
		return
	}

	_, err = s.DB.Exec(`
		UPDATE transactions SET account_id=?, txn_date=?, value_date=?, narration=?, ref_no=?,
			withdrawal_amt=?, deposit_amt=?, type=?, category_id=?, merchant=?,
			payment_method=?, notes=?, transfer_kind=?, counterparty=?
		WHERE id=?`,
		t.AccountID, t.TxnDate, t.ValueDate, t.Narration, t.RefNo, t.WithdrawalAmt, t.DepositAmt,
		t.Type, t.CategoryID, t.Merchant, t.PaymentMethod, t.Notes, t.TransferKind, t.Counterparty, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	ledger.RecomputeAccountBalance(s.DB, t.AccountID)
	if oldAccountID != t.AccountID {
		ledger.RecomputeAccountBalance(s.DB, oldAccountID)
	}

	// Correcting one occurrence corrects the narration everywhere. Only when
	// the category actually changed: re-saving an unrelated field must not
	// quietly rewrite every sibling row.
	if categoryChanged(oldCategoryID, t.CategoryID) {
		if _, err := s.spreadCategoryByNarration([]int64{id}, t.CategoryID); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
	}

	t.ID = id
	httpx.WriteJSON(w, 200, t)
}

func categoryChanged(old sql.NullInt64, next *int64) bool {
	if !old.Valid {
		return next != nil
	}
	return next == nil || *next != old.Int64
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
		ledger.RecomputeAccountBalance(s.DB, accountID)
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
		ledger.RecomputeAccountBalance(s.DB, accID)
	}

	httpx.WriteJSON(w, 200, bulkDeleteResponse{Deleted: int(deleted)})
}

type bulkCategorizeRequest struct {
	IDs        []int64 `json:"ids"`
	CategoryID *int64  `json:"categoryId"` // null clears the category
}

type bulkCategorizeResponse struct {
	Updated int `json:"updated"`
	// Matched is how many additional rows were changed because they share a
	// narration with the ones selected.
	Matched int `json:"matched"`
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

	// A narration names the payee and the rail it came in on, so every row
	// carrying it is the same kind of payment. Categorising one and leaving
	// its thirty-two twins uncategorised is busywork the user would only have
	// to repeat.
	spread, err := s.spreadCategoryByNarration(req.IDs, req.CategoryID)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	httpx.WriteJSON(w, 200, bulkCategorizeResponse{Updated: int(updated) + spread, Matched: spread})
}

// spreadCategoryByNarration applies the category chosen for the given
// transactions to every other transaction sharing their narration, and reports
// how many extra rows it changed.
//
// Rows already carrying the chosen category are excluded by the WHERE clause,
// so the count reports real changes rather than no-ops. Clearing a category
// (categoryID nil) spreads too: un-tagging one row means the narration was
// tagged wrongly, and the rest are wrong in the same way.
func (s *Server) spreadCategoryByNarration(ids []int64, categoryID *int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := []any{categoryID}
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")

	// TRIM/LOWER so the same payee written with different spacing or case
	// still counts as one narration, matching how the classifier groups.
	query := fmt.Sprintf(`
		UPDATE transactions SET category_id = ?
		WHERE LOWER(TRIM(narration)) IN (
			SELECT LOWER(TRIM(narration)) FROM transactions
			WHERE id IN (%s) AND TRIM(narration) != ''
		)
		AND id NOT IN (%s)
		AND category_id IS NOT ?`, inClause, inClause)

	spreadArgs := append([]any{}, args...)
	spreadArgs = append(spreadArgs, args[1:]...) // the ids a second time
	spreadArgs = append(spreadArgs, categoryID)

	res, err := s.DB.Exec(query, spreadArgs...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

type bulkMarkTransferRequest struct {
	IDs []int64 `json:"ids"`
	// Kind is 'self', 'investment' or 'family'.
	Kind         string `json:"kind"`
	Counterparty string `json:"counterparty"`
}

// handleBulkMarkTransfer reclassifies transactions as transfers of a given
// kind — money moved to an investment or sent to family is not spending, but
// it is also not a pair of rows to link: the other side has no transaction
// stream here. Marking drops the rows out of income/expense totals and lets
// the dashboard report them as their own cash-flow lines.
func (s *Server) handleBulkMarkTransfer(w http.ResponseWriter, r *http.Request) {
	var req bulkMarkTransferRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if len(req.IDs) == 0 {
		httpx.WriteError(w, 400, "ids is required and must be non-empty")
		return
	}
	if !validTransferKinds[req.Kind] {
		httpx.WriteError(w, 400, "kind must be one of: self, investment, family")
		return
	}

	placeholders := make([]string, len(req.IDs))
	args := make([]interface{}, 0, len(req.IDs)+2)
	args = append(args, req.Kind, req.Counterparty)
	for i, id := range req.IDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	res, err := s.DB.Exec(fmt.Sprintf(
		`UPDATE transactions SET type='transfer', transfer_kind=?, counterparty=? WHERE id IN (%s)`,
		strings.Join(placeholders, ",")), args...)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	updated, _ := res.RowsAffected()
	httpx.WriteJSON(w, 200, map[string]int64{"updated": updated})
}
