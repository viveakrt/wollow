package moneyapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"wollow/backend/internal/money/models"
)

func addBill(t *testing.T, server *Server, accountID int64, card, period string, total, minimum float64, due, status string) int64 {
	t.Helper()
	res, err := server.DB.Exec(`
		INSERT INTO bills (account_id, issuer, card_last4, statement_period, total_due, minimum_due, due_date, status)
		VALUES (?, 'ICICI', ?, ?, ?, ?, ?, ?)`, accountID, card, period, total, minimum, due, status)
	if err != nil {
		t.Fatalf("insert bill: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

var txnSeq int

func addTxn(t *testing.T, server *Server, accountID int64, date string, withdrawal, deposit float64) int64 {
	t.Helper()
	txnSeq++
	kind := "expense"
	if deposit > 0 {
		kind = "income"
	}
	res, err := server.DB.Exec(`
		INSERT INTO transactions (account_id, txn_date, value_date, narration, withdrawal_amt, deposit_amt, type, dedupe_hash)
		VALUES (?, ?, ?, 'test', ?, ?, ?, ?)`,
		accountID, date, date, withdrawal, deposit, kind, fmt.Sprintf("test-%d", txnSeq))
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func upcomingBillCount(t *testing.T, mux *http.ServeMux) int {
	t.Helper()
	summary := decode[map[string]any](t, do(t, mux, "GET", "/api/money/dashboard/summary", ""))
	bills, _ := summary["upcomingBills"].([]any)
	return len(bills)
}

func TestMarkBillPaidAndUnpaid(t *testing.T) {
	server, mux := newTestServer(t)
	card := createAccount(t, mux, `{"name":"ICICI card","bank":"ICICI","accountType":"credit_card","accountNumber":"7001"}`)
	id := addBill(t, server, card.ID, "7001", "Jul 2026", 12345, 1234, "2026-08-05", "unpaid")
	path := fmt.Sprintf("/api/money/bills/%d", id)

	if n := upcomingBillCount(t, mux); n != 1 {
		t.Fatalf("upcoming bills = %d, want 1", n)
	}

	w := do(t, mux, "POST", path+"/paid", `{"paidAt":"2026-08-03"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("mark paid: %d %s", w.Code, w.Body.String())
	}
	bill := decode[models.Bill](t, w)
	if bill.Status != "paid" || bill.PaidAt != "2026-08-03" || bill.AccountName != "ICICI card" {
		t.Errorf("paid bill = %+v", bill)
	}
	if n := upcomingBillCount(t, mux); n != 0 {
		t.Errorf("a paid bill is still upcoming on the dashboard")
	}
	if list := decode[[]models.Bill](t, do(t, mux, "GET", "/api/money/bills", "")); len(list) != 1 || list[0].Status != "paid" {
		t.Errorf("bills list = %+v", list)
	}

	unpaid := decode[models.Bill](t, do(t, mux, "POST", path+"/unpaid", ""))
	if unpaid.Status != "unpaid" || unpaid.PaidAt != "" {
		t.Errorf("unpaid bill = %+v", unpaid)
	}
	if n := upcomingBillCount(t, mux); n != 1 {
		t.Errorf("an unpaid bill is missing from the dashboard")
	}

	// With no date, the bill is paid today.
	if today := decode[models.Bill](t, do(t, mux, "POST", path+"/paid", "")); today.PaidAt != time.Now().Format("2006-01-02") {
		t.Errorf("paidAt = %q, want today", today.PaidAt)
	}
	if w := do(t, mux, "POST", path+"/paid", `{"paidAt":"05/08/2026"}`); w.Code != http.StatusBadRequest {
		t.Errorf("malformed date: %d", w.Code)
	}
	if w := do(t, mux, "POST", "/api/money/bills/999/paid", ""); w.Code != http.StatusNotFound {
		t.Errorf("missing bill: %d", w.Code)
	}
}

func TestLinkingACardPaymentSuggestsItsOpenBills(t *testing.T) {
	server, mux := newTestServer(t)
	bank := createAccount(t, mux, `{"name":"HDFC Savings","bank":"HDFC","accountType":"bank","accountNumber":"4125"}`)
	card := createAccount(t, mux, `{"name":"ICICI card","bank":"ICICI","accountType":"credit_card","accountNumber":"7001"}`)
	other := createAccount(t, mux, `{"name":"Other card","bank":"ICICI","accountType":"credit_card","accountNumber":"5792"}`)

	older := addBill(t, server, card.ID, "7001", "Jun 2026", 7000, 700, "2026-07-05", "unpaid")
	exact := addBill(t, server, card.ID, "7001", "Jul 2026", 5000, 500, "2026-08-05", "unpaid")
	addBill(t, server, card.ID, "7001", "May 2026", 5000, 500, "2026-06-05", "paid")
	addBill(t, server, other.ID, "5792", "Jul 2026", 5000, 500, "2026-08-05", "unpaid")

	out := addTxn(t, server, bank.ID, "2026-08-03", 5000, 0)
	in := addTxn(t, server, card.ID, "2026-08-03", 0, 5000)

	w := do(t, mux, "POST", "/api/money/transactions/link-transfer", fmt.Sprintf(`{"txnIdA":%d,"txnIdB":%d}`, out, in))
	if w.Code != http.StatusOK {
		t.Fatalf("link: %d %s", w.Code, w.Body.String())
	}
	resp := decode[linkTransferResponse](t, w)
	s := resp.BillSuggestion
	if !resp.Linked || s == nil {
		t.Fatalf("response = %+v, want a bill suggestion", resp)
	}
	if s.AccountID != card.ID || s.AccountName != "ICICI card" || s.Amount != 5000 || s.Date != "2026-08-03" {
		t.Errorf("payment = %+v", s)
	}
	if len(s.Bills) != 2 {
		t.Fatalf("suggested %d bills, want this card's 2 unpaid ones: %+v", len(s.Bills), s.Bills)
	}
	if s.Bills[0].ID != exact || s.Bills[0].Coverage != "full" {
		t.Errorf("first suggestion = bill %d (%s), want the exact-total bill %d (full)", s.Bills[0].ID, s.Bills[0].Coverage, exact)
	}
	if s.Bills[1].ID != older || s.Bills[1].Coverage != "minimum" {
		t.Errorf("second suggestion = bill %d (%s), want bill %d (minimum)", s.Bills[1].ID, s.Bills[1].Coverage, older)
	}
}

func TestTransferBetweenBankAccountsSuggestsNoBill(t *testing.T) {
	server, mux := newTestServer(t)
	hdfc := createAccount(t, mux, `{"name":"HDFC","bank":"HDFC","accountType":"bank","accountNumber":"4125"}`)
	sbi := createAccount(t, mux, `{"name":"SBI","bank":"SBI","accountType":"bank","accountNumber":"1111"}`)
	card := createAccount(t, mux, `{"name":"ICICI card","bank":"ICICI","accountType":"credit_card","accountNumber":"7001"}`)
	addBill(t, server, card.ID, "7001", "Jul 2026", 2000, 200, "2026-08-05", "unpaid")

	out := addTxn(t, server, hdfc.ID, "2026-08-03", 2000, 0)
	in := addTxn(t, server, sbi.ID, "2026-08-03", 0, 2000)
	resp := decode[linkTransferResponse](t, do(t, mux, "POST", "/api/money/transactions/link-transfer",
		fmt.Sprintf(`{"txnIdA":%d,"txnIdB":%d}`, out, in)))
	if !resp.Linked || resp.BillSuggestion != nil {
		t.Errorf("response = %+v, want linked with no bill suggestion", resp)
	}
}

func TestConfirmingASuggestedCardPaymentSuggestsTheBill(t *testing.T) {
	server, mux := newTestServer(t)
	bank := createAccount(t, mux, `{"name":"HDFC","bank":"HDFC","accountType":"bank","accountNumber":"4125"}`)
	card := createAccount(t, mux, `{"name":"ICICI card","bank":"ICICI","accountType":"credit_card","accountNumber":"7001"}`)
	bill := addBill(t, server, card.ID, "7001", "Jul 2026", 3000, 300, "2026-08-15", "unpaid")
	addTxn(t, server, bank.ID, "2026-08-10", 3000, 0)
	addTxn(t, server, card.ID, "2026-08-11", 0, 3000)

	if w := do(t, mux, "POST", "/api/money/transfer-suggestions/scan", ""); w.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", w.Code, w.Body.String())
	}
	suggestions := decode[[]models.TransferSuggestion](t, do(t, mux, "GET", "/api/money/transfer-suggestions", ""))
	if len(suggestions) != 1 {
		t.Fatalf("transfer suggestions = %d, want 1", len(suggestions))
	}
	resp := decode[confirmTransferResponse](t, do(t, mux, "POST",
		fmt.Sprintf("/api/money/transfer-suggestions/%d/confirm", suggestions[0].ID), ""))
	if !resp.Confirmed || resp.BillSuggestion == nil || len(resp.BillSuggestion.Bills) != 1 {
		t.Fatalf("response = %+v, want the card's bill suggested", resp)
	}
	if got := resp.BillSuggestion.Bills[0]; got.ID != bill || got.Coverage != "full" {
		t.Errorf("suggested bill %d (%s), want %d (full)", got.ID, got.Coverage, bill)
	}
}
