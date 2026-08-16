package moneyapi

import (
	"fmt"
	"net/http"
	"strconv"
	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/money/models"
)

// handleScanTransferSuggestions looks for expense/income pairs across
// different accounts that plausibly represent the same money movement —
// classically, a bank account debit that funds a credit card payment,
// showing up as an "expense" in the bank and an "income"/payment-received
// row on the card. A pair is a candidate when:
//   - different accounts
//   - one is an outflow (withdrawal) and the other an inflow (deposit)
//   - amounts match exactly
//   - transaction dates are within 3 days of each other
//   - neither transaction is already linked, and this pair hasn't already
//     been suggested (pending or dismissed)
//
// Confidence is 1.0 for same-day matches, decreasing slightly per day of
// gap, so the UI can sort/display the most likely matches first.
func (s *Server) handleScanTransferSuggestions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT a.id, a.account_id, b.id, b.account_id,
		       julianday(a.txn_date) - julianday(b.txn_date) AS day_gap
		FROM transactions a
		JOIN transactions b
		  ON a.withdrawal_amt = b.deposit_amt
		 AND a.withdrawal_amt > 0
		 AND a.account_id != b.account_id
		 AND a.id < b.id
		 AND ABS(julianday(a.txn_date) - julianday(b.txn_date)) <= 3
		WHERE a.linked_txn_id IS NULL AND b.linked_txn_id IS NULL
		  AND a.type != 'transfer' AND b.type != 'transfer'
		  AND NOT EXISTS (
		      SELECT 1 FROM transfer_suggestions ts
		      WHERE min(ts.txn_id_a, ts.txn_id_b) = min(a.id, b.id)
		        AND max(ts.txn_id_a, ts.txn_id_b) = max(a.id, b.id)
		  )`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	type candidate struct {
		aID, bID int64
		dayGap   float64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var accA, accB int64
		if err := rows.Scan(&c.aID, &accA, &c.bID, &accB, &c.dayGap); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		candidates = append(candidates, c)
	}
	rows.Close()

	created := 0
	for _, c := range candidates {
		confidence := 1.0 - (absFloat(c.dayGap) * 0.15)
		if confidence < 0.4 {
			confidence = 0.4
		}
		res, err := s.DB.Exec(`
			INSERT OR IGNORE INTO transfer_suggestions (txn_id_a, txn_id_b, confidence, status)
			VALUES (?, ?, ?, 'pending')`, c.aID, c.bID, confidence)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		created += int(n)
	}

	httpx.WriteJSON(w, 200, map[string]int{"suggestionsCreated": created})
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func (s *Server) handleListTransferSuggestions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT id, txn_id_a, txn_id_b, confidence, status, created_at
		FROM transfer_suggestions
		WHERE status = 'pending'
		ORDER BY confidence DESC, created_at DESC`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	type row struct {
		id                int64
		txnIDA, txnIDB    int64
		confidence        float64
		status, createdAt string
	}
	var results []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.id, &rr.txnIDA, &rr.txnIDB, &rr.confidence, &rr.status, &rr.createdAt); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		results = append(results, rr)
	}
	rows.Close()

	suggestions := make([]models.TransferSuggestion, 0, len(results))
	for _, rr := range results {
		txnA, errA := s.getTransactionByID(rr.txnIDA)
		txnB, errB := s.getTransactionByID(rr.txnIDB)
		if errA != nil || errB != nil {
			continue // one side was deleted since the suggestion was created
		}
		suggestions = append(suggestions, models.TransferSuggestion{
			ID:         rr.id,
			TxnA:       txnA,
			TxnB:       txnB,
			Confidence: rr.confidence,
			Status:     rr.status,
			CreatedAt:  rr.createdAt,
		})
	}

	httpx.WriteJSON(w, 200, suggestions)
}

func (s *Server) getTransactionByID(id int64) (models.Transaction, error) {
	query := fmt.Sprintf(`SELECT %s %s WHERE t.id = ?`, txnSelectCols, txnSelectJoins)
	return scanTxn(s.DB.QueryRow(query, id))
}

// handleConfirmTransferSuggestion links the two transactions (sets
// linked_txn_id on both, marks both type='transfer' so they drop out of
// income/expense totals) and marks the suggestion confirmed.
func (s *Server) handleConfirmTransferSuggestion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}

	var txnA, txnB int64
	if err := s.DB.QueryRow(`SELECT txn_id_a, txn_id_b FROM transfer_suggestions WHERE id=? AND status='pending'`, id).
		Scan(&txnA, &txnB); err != nil {
		httpx.WriteError(w, 404, "suggestion not found or already resolved")
		return
	}

	if err := linkTransferPair(s.DB, txnA, txnB); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	s.DB.Exec(`UPDATE transfer_suggestions SET status='confirmed' WHERE id=?`, id)
	httpx.WriteJSON(w, 200, map[string]bool{"confirmed": true})
}

func (s *Server) handleDismissTransferSuggestion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	if _, err := s.DB.Exec(`UPDATE transfer_suggestions SET status='dismissed' WHERE id=?`, id); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"dismissed": true})
}

type linkTransferRequest struct {
	TxnIDA int64 `json:"txnIdA"`
	TxnIDB int64 `json:"txnIdB"`
}

// handleLinkTransfer is the manual counterpart to confirming a suggestion —
// the user picks any two transactions directly instead of waiting for the
// auto-scan to propose them.
func (s *Server) handleLinkTransfer(w http.ResponseWriter, r *http.Request) {
	var req linkTransferRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if req.TxnIDA == 0 || req.TxnIDB == 0 || req.TxnIDA == req.TxnIDB {
		httpx.WriteError(w, 400, "txnIdA and txnIdB are required and must differ")
		return
	}
	if err := linkTransferPair(s.DB, req.TxnIDA, req.TxnIDB); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"linked": true})
}

// handleUnlinkTransfer reverts both legs of a transfer back to independent
// expense/income transactions (best-effort type inference from amounts) and
// recomputes both accounts' balances (type change doesn't affect the sum,
// but keeps behavior consistent with other mutations).
func (s *Server) handleUnlinkTransfer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}

	var linkedID int64
	var accountID int64
	var withdrawal, deposit float64
	if err := s.DB.QueryRow(`SELECT linked_txn_id, account_id, withdrawal_amt, deposit_amt FROM transactions WHERE id=?`, id).
		Scan(&linkedID, &accountID, &withdrawal, &deposit); err != nil {
		httpx.WriteError(w, 404, "transaction not found")
		return
	}
	if linkedID == 0 {
		httpx.WriteError(w, 400, "transaction is not linked to a transfer")
		return
	}

	for _, txnID := range []int64{id, linkedID} {
		var w2, d2 float64
		var accID int64
		s.DB.QueryRow(`SELECT account_id, withdrawal_amt, deposit_amt FROM transactions WHERE id=?`, txnID).Scan(&accID, &w2, &d2)
		newType := "expense"
		if d2 > 0 {
			newType = "income"
		}
		s.DB.Exec(`UPDATE transactions SET linked_txn_id = NULL, type = ?, transfer_kind = '', counterparty = '' WHERE id = ?`, newType, txnID)
		ledger.RecomputeAccountBalance(s.DB, accID)
	}

	httpx.WriteJSON(w, 200, map[string]bool{"unlinked": true})
}

// linkTransferPair sets both transactions' linked_txn_id to point at each
// other and marks both type='transfer', excluding them from income/expense
// dashboard totals (see handleDashboardSummary's `type != 'transfer'`
// filter). Balances are untouched — a transfer still moves real money
// through both accounts, so withdrawal/deposit amounts stay as-is and
// current_balance (opening + sum(deposits) - sum(withdrawals)) is already
// correct without a recompute.
//
// A linked pair is by definition money between two of the user's own tracked
// accounts, so transfer_kind is always 'self' here. Investment and family
// transfers are single-leg (the other side has no transaction stream) and are
// classified via handleBulkMarkTransfer or a transaction edit instead.
func linkTransferPair(db ledger.Execer, txnA, txnB int64) error {
	if _, err := db.Exec(`UPDATE transactions SET linked_txn_id=?, type='transfer', transfer_kind='self' WHERE id=?`, txnB, txnA); err != nil {
		return fmt.Errorf("linking txn %d: %w", txnA, err)
	}
	if _, err := db.Exec(`UPDATE transactions SET linked_txn_id=?, type='transfer', transfer_kind='self' WHERE id=?`, txnA, txnB); err != nil {
		return fmt.Errorf("linking txn %d: %w", txnB, err)
	}
	return nil
}
