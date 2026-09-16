package moneyapi

import (
	"database/sql"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

const billColumns = `
	b.id, b.account_id, COALESCE(fa.name, ''), b.issuer, b.card_last4, b.statement_period,
	b.total_due, b.minimum_due, b.due_date, b.status, b.paid_at, b.created_at`

const billJoins = `
	FROM bills b
	LEFT JOIN finance_accounts fa ON fa.id = b.account_id`

func scanBill(row interface{ Scan(...interface{}) error }) (models.Bill, error) {
	var b models.Bill
	var accountID sql.NullInt64
	var totalDue, minDue sql.NullFloat64
	if err := row.Scan(&b.ID, &accountID, &b.AccountName, &b.Issuer, &b.CardLast4, &b.StatementPeriod,
		&totalDue, &minDue, &b.DueDate, &b.Status, &b.PaidAt, &b.CreatedAt); err != nil {
		return b, err
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
	return b, nil
}

func (s *Server) handleListBills(w http.ResponseWriter, r *http.Request) {
	// Unpaid first, so the limit can only ever cut settled history.
	rows, err := s.DB.Query(`SELECT ` + billColumns + billJoins + `
		ORDER BY CASE WHEN b.status = 'unpaid' THEN 0 ELSE 1 END, b.created_at DESC
		LIMIT 200`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	bills := []models.Bill{}
	for rows.Next() {
		b, err := scanBill(rows)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		bills = append(bills, b)
	}
	httpx.WriteJSON(w, 200, bills)
}

type markBillPaidRequest struct {
	PaidAt string `json:"paidAt"`
}

// handleMarkBillPaid only settles the reminder; the card's balance comes from its transactions.
func (s *Server) handleMarkBillPaid(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	var req markBillPaidRequest
	if err := httpx.DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	paidAt := req.PaidAt
	if paidAt == "" {
		paidAt = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", paidAt); err != nil {
		httpx.WriteError(w, 400, "paidAt must be YYYY-MM-DD")
		return
	}
	s.setBillStatus(w, id, "paid", paidAt)
}

func (s *Server) handleMarkBillUnpaid(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}
	s.setBillStatus(w, id, "unpaid", "")
}

func (s *Server) setBillStatus(w http.ResponseWriter, id int64, status, paidAt string) {
	res, err := s.DB.Exec(`UPDATE bills SET status = ?, paid_at = ? WHERE id = ?`, status, paidAt, id)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.WriteError(w, 404, "bill not found")
		return
	}
	bill, err := scanBill(s.DB.QueryRow(`SELECT `+billColumns+billJoins+` WHERE b.id = ?`, id))
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	httpx.WriteJSON(w, 200, bill)
}

// billPaymentSuggestion is only a suggestion: one payment can cover part of a bill, or two bills.
type billPaymentSuggestion struct {
	AccountID   int64           `json:"accountId"`
	AccountName string          `json:"accountName"`
	Amount      float64         `json:"amount"`
	Date        string          `json:"date"`
	Bills       []suggestedBill `json:"bills"`
}

type suggestedBill struct {
	models.Bill
	// Coverage is full, minimum, partial, or unknown when the statement stated no amounts.
	Coverage string `json:"coverage"`
}

const (
	maxSuggestedBills = 5
	// amountTolerance absorbs the paise a payment rounds away.
	amountTolerance = 1.0
)

// suggestBillPayment returns nil unless one linked leg is money arriving on a card or loan with unpaid bills.
func (s *Server) suggestBillPayment(txnA, txnB int64) *billPaymentSuggestion {
	rows, err := s.DB.Query(`
		SELECT t.account_id, fa.name, fa.account_type, t.deposit_amt, t.txn_date
		FROM transactions t
		JOIN finance_accounts fa ON fa.id = t.account_id
		WHERE t.id IN (?, ?)`, txnA, txnB)
	if err != nil {
		return nil
	}
	var payment *billPaymentSuggestion
	for rows.Next() {
		var p billPaymentSuggestion
		var accountType string
		if err := rows.Scan(&p.AccountID, &p.AccountName, &accountType, &p.Amount, &p.Date); err != nil {
			continue
		}
		if liabilityTypes[accountType] && p.Amount > 0 {
			payment = &p
		}
	}
	// The pool holds one connection, so the cursor must close before the next query.
	rows.Close()
	if payment == nil {
		return nil
	}

	billRows, err := s.DB.Query(`SELECT `+billColumns+billJoins+`
		WHERE b.account_id = ? AND b.status = 'unpaid'
		ORDER BY CASE WHEN b.due_date = '' THEN 1 ELSE 0 END, b.due_date, b.id`, payment.AccountID)
	if err != nil {
		return nil
	}
	for billRows.Next() {
		bill, err := scanBill(billRows)
		if err != nil {
			continue
		}
		payment.Bills = append(payment.Bills, suggestedBill{Bill: bill, Coverage: billCoverage(payment.Amount, bill)})
	}
	billRows.Close()
	if len(payment.Bills) == 0 {
		return nil
	}

	// Paying exactly the total due identifies the bill better than any due date does.
	sort.SliceStable(payment.Bills, func(i, j int) bool {
		return paysExactTotal(payment.Amount, payment.Bills[i].Bill) && !paysExactTotal(payment.Amount, payment.Bills[j].Bill)
	})
	if len(payment.Bills) > maxSuggestedBills {
		payment.Bills = payment.Bills[:maxSuggestedBills]
	}
	return payment
}

func paysExactTotal(amount float64, b models.Bill) bool {
	return b.TotalDue != nil && math.Abs(amount-*b.TotalDue) <= amountTolerance
}

func billCoverage(amount float64, b models.Bill) string {
	switch {
	case b.TotalDue != nil && amount >= *b.TotalDue-amountTolerance:
		return "full"
	case b.MinimumDue != nil && amount >= *b.MinimumDue-amountTolerance:
		return "minimum"
	case b.TotalDue != nil || b.MinimumDue != nil:
		return "partial"
	default:
		return "unknown"
	}
}
