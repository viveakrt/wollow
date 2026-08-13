package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"mooliq/backend/internal/emailparser"
	"mooliq/backend/internal/models"
)

type connectEmailRequest struct {
	Email       string `json:"email"`
	AppPassword string `json:"appPassword"`
	IMAPHost    string `json:"imapHost"`
	IMAPPort    int    `json:"imapPort"`
}

func (s *Server) handleConnectEmailAccount(w http.ResponseWriter, r *http.Request) {
	var req connectEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	if req.Email == "" || req.AppPassword == "" {
		writeError(w, 400, "email and appPassword are required")
		return
	}
	if req.IMAPHost == "" {
		req.IMAPHost = "imap.gmail.com"
	}
	if req.IMAPPort == 0 {
		req.IMAPPort = 993
	}

	// Verify the credentials actually work before saving them.
	if _, err := emailparser.FetchNewFromAllowlist(req.IMAPHost, req.IMAPPort, req.Email, req.AppPassword, 1<<31); err != nil {
		writeError(w, 400, "could not connect: "+err.Error())
		return
	}

	res, err := s.DB.Exec(`
		INSERT INTO email_accounts (email, imap_host, imap_port, app_password)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET app_password=excluded.app_password,
			imap_host=excluded.imap_host, imap_port=excluded.imap_port, enabled=1`,
		req.Email, req.IMAPHost, req.IMAPPort, req.AppPassword)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]interface{}{"id": id, "email": req.Email})
}

func (s *Server) handleListEmailAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`SELECT id, email, imap_host, imap_port, last_synced_at, last_uid, enabled, created_at FROM email_accounts ORDER BY id`)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	accounts := []models.EmailAccount{}
	for rows.Next() {
		var a models.EmailAccount
		var enabled int
		if err := rows.Scan(&a.ID, &a.Email, &a.IMAPHost, &a.IMAPPort, &a.LastSyncedAt, &a.LastUID, &enabled, &a.CreatedAt); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.Enabled = enabled == 1
		accounts = append(accounts, a)
	}
	writeJSON(w, 200, accounts)
}

func (s *Server) handleDeleteEmailAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM email_accounts WHERE id=?`, id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

type syncResult struct {
	Scanned      int `json:"scanned"`
	Transactions int `json:"transactions"`
	Bills        int `json:"bills"`
	Unrecognized int `json:"unrecognized"`
	Duplicates   int `json:"duplicates"`
}

func (s *Server) handleSyncEmailAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}

	var host, email, appPassword string
	var port int
	var lastUID uint32
	err = s.DB.QueryRow(`SELECT imap_host, imap_port, email, app_password, last_uid FROM email_accounts WHERE id=?`, id).
		Scan(&host, &port, &email, &appPassword, &lastUID)
	if err != nil {
		writeError(w, 404, "email account not found")
		return
	}

	messages, err := emailparser.FetchNewFromAllowlist(host, port, email, appPassword, lastUID)
	if err != nil {
		writeError(w, 502, "sync failed: "+err.Error())
		return
	}

	result := syncResult{}
	var maxUID uint32 = lastUID

	for _, msg := range messages {
		if msg.UID > maxUID {
			maxUID = msg.UID
		}
		result.Scanned++

		parsed, err := emailparser.ParseEML(msg.Raw)
		if err != nil || parsed.MessageID == "" {
			continue
		}

		// Skip if we've already recorded this exact message (defensive;
		// UID-based sync should already prevent this).
		var exists int
		s.DB.QueryRow(`SELECT COUNT(*) FROM email_messages WHERE email_account_id=? AND message_id=?`, id, parsed.MessageID).Scan(&exists)
		if exists > 0 {
			result.Duplicates++
			continue
		}

		issuer := emailparser.IssuerForSender(parsed.From)
		parsedAs, txnID, billID := processEmail(s.DB, issuer, parsed)
		switch parsedAs {
		case "transaction":
			result.Transactions++
		case "bill":
			result.Bills++
		default:
			result.Unrecognized++
		}

		s.DB.Exec(`
			INSERT INTO email_messages (email_account_id, message_id, uid, sender, subject, received_at, parsed_as, transaction_id, bill_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, parsed.MessageID, msg.UID, parsed.From, parsed.Subject, parsed.Date, parsedAs, txnID, billID)
	}

	s.DB.Exec(`UPDATE email_accounts SET last_uid=?, last_synced_at=datetime('now') WHERE id=?`, maxUID, id)

	writeJSON(w, 200, result)
}

// processEmail parses a single email's content and, if recognized, persists
// a transaction or bill. Returns ("transaction"|"bill"|"unrecognized", txnID, billID).
func processEmail(db *sql.DB, issuer string, e *emailparser.Email) (string, *int64, *int64) {
	if emailparser.IsBillEmail(e.Subject) {
		bill := emailparser.ParseBillEmail(issuer, e.Subject, e.TextBody)
		accountID := matchAccountByLast4(db, bill.CardLast4)
		res, err := db.Exec(`
			INSERT INTO bills (account_id, issuer, card_last4, statement_period, total_due, minimum_due, due_date)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			nullIfZeroID(accountID), bill.Issuer, bill.CardLast4, bill.StatementPeriod, bill.TotalDue, bill.MinimumDue, bill.DueDate)
		if err != nil {
			return "unrecognized", nil, nil
		}
		id, _ := res.LastInsertId()

		for _, att := range e.PDFAttachments {
			db.Exec(`INSERT INTO pdf_attachments (bill_id, file_name, content) VALUES (?, ?, ?)`,
				id, att.FileName, att.Content)
		}

		return "bill", nil, &id
	}

	if issuer == "" {
		return "unrecognized", nil, nil
	}

	var txn *models.ParsedEmailTransaction
	var ok bool
	switch issuer {
	case "HDFC":
		if emailparser.IsHDFCBalanceUpdate(e.TextBody) {
			return "unrecognized", nil, nil // snapshot only, not actionable
		}
		txn, ok = emailparser.ParseHDFCEmail(e.Subject, e.TextBody)
	case "Axis":
		txn, ok = emailparser.ParseAxisEmail(e.Subject, e.TextBody)
	}
	if !ok || txn == nil || txn.Amount == 0 {
		return "unrecognized", nil, nil
	}

	accountID := matchAccountByLast4(db, txn.AccountLast4)
	if accountID == 0 {
		accountID = getOrCreateUnmatchedAccount(db, issuer, txn.AccountLast4)
	}

	withdrawal, deposit := 0.0, 0.0
	if txn.Type == "income" {
		deposit = txn.Amount
	} else {
		withdrawal = txn.Amount
	}

	dedupe := emailDedupeHash(accountID, txn)
	res, err := db.Exec(`
		INSERT OR IGNORE INTO transactions
			(account_id, txn_date, value_date, narration, ref_no, withdrawal_amt, deposit_amt,
			 closing_balance, type, merchant, payment_method, dedupe_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, txn.TxnDate, txn.TxnDate, txn.Narration, txn.RefNo, withdrawal, deposit,
		nullIfZero(txn.ClosingBalance), txn.Type, txn.Merchant, txn.PaymentMethod, dedupe)
	if err != nil {
		return "unrecognized", nil, nil
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "unrecognized", nil, nil // duplicate, already have this transaction
	}
	id, _ := res.LastInsertId()
	recomputeAccountBalance(db, accountID)
	return "transaction", &id, nil
}

func nullIfZero(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

func nullIfZeroID(id int64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}

func emailDedupeHash(accountID int64, t *models.ParsedEmailTransaction) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%.2f|%s|%s", accountID, t.TxnDate, t.Amount, t.RefNo, t.Narration)
	return hex.EncodeToString(h.Sum(nil))
}

// matchAccountByLast4 finds an existing account whose stored account_number
// ends with the given last-4 digits. Returns 0 if no match or last4 is empty.
func matchAccountByLast4(db *sql.DB, last4 string) int64 {
	if last4 == "" {
		return 0
	}
	var id int64
	err := db.QueryRow(`SELECT id FROM accounts WHERE account_number LIKE '%' || ?`, last4).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}

// getOrCreateUnmatchedAccount finds or creates a placeholder account for
// alerts whose card/account last-4 didn't match any known account, so
// transactions aren't lost — the user can rename/merge it later from the
// Accounts page.
func getOrCreateUnmatchedAccount(db *sql.DB, issuer, last4 string) int64 {
	name := strings.TrimSpace(issuer + " •• " + last4)
	if last4 == "" {
		name = issuer + " (from email)"
	}
	var id int64
	err := db.QueryRow(`SELECT id FROM accounts WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id
	}
	accType := "credit_card"
	res, err := db.Exec(`
		INSERT INTO accounts (name, bank, account_type, account_number, currency)
		VALUES (?, ?, ?, ?, 'INR')`, name, issuer, accType, last4)
	if err != nil {
		return 0
	}
	id, _ = res.LastInsertId()
	return id
}

func (s *Server) handleListBills(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT id, account_id, issuer, card_last4, statement_period, total_due, minimum_due, due_date, status, created_at
		FROM bills ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		writeError(w, 500, err.Error())
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
			writeError(w, 500, err.Error())
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
	writeJSON(w, 200, bills)
}
