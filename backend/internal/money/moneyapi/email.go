package moneyapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

// handleListEmailAccounts lists the mailboxes connected on the Mail side,
// annotated with Money's own ingest cursor. Connecting and disconnecting a
// mailbox happens at /api/mail/accounts — there is one credential store, and
// deleting a mailbox from here would discard the user's whole message index.
func (s *Server) handleListEmailAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT a.id, a.username, a.imap_host, a.imap_port, a.enabled, a.created_at,
		       COALESCE(m.last_synced_at, ''), COALESCE(m.last_uid, 0)
		FROM mail_accounts a
		LEFT JOIN money_ingest_state m ON m.mail_account_id = a.id
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

type syncResult struct {
	Scanned      int `json:"scanned"`
	Transactions int `json:"transactions"`
	Bills        int `json:"bills"`
	Unrecognized int `json:"unrecognized"`
	Duplicates   int `json:"duplicates"`
}

func (s *Server) handleSyncEmailAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, 400, "invalid id")
		return
	}

	var host, email, encryptedPassword string
	var port int
	var lastUID uint32
	err = s.DB.QueryRow(`
		SELECT a.imap_host, a.imap_port, a.username, a.encrypted_password, COALESCE(m.last_uid, 0)
		FROM mail_accounts a
		LEFT JOIN money_ingest_state m ON m.mail_account_id = a.id
		WHERE a.id = ?`, id).
		Scan(&host, &port, &email, &encryptedPassword, &lastUID)
	if err != nil {
		httpx.WriteError(w, 404, "mailbox not found")
		return
	}

	appPassword, err := s.Box.Decrypt(encryptedPassword)
	if err != nil {
		httpx.WriteError(w, 500, "could not decrypt mailbox password")
		return
	}

	messages, err := emailparse.FetchNewFromAllowlist(host, port, email, appPassword, lastUID)
	if err != nil {
		httpx.WriteError(w, 502, "sync failed: "+err.Error())
		return
	}

	result := syncResult{}
	var maxUID uint32 = lastUID

	for _, msg := range messages {
		if msg.UID > maxUID {
			maxUID = msg.UID
		}
		result.Scanned++

		parsed, err := emailparse.ParseEML(msg.Raw)
		if err != nil || parsed.MessageID == "" {
			continue
		}

		// Skip if we've already recorded this exact message (defensive;
		// UID-based sync should already prevent this).
		var exists int
		s.DB.QueryRow(`SELECT COUNT(*) FROM message_links WHERE mail_account_id=? AND rfc_message_id=?`, id, parsed.MessageID).Scan(&exists)
		if exists > 0 {
			result.Duplicates++
			continue
		}

		issuer := emailparse.IssuerForSender(parsed.From)
		parsedAs, txnID, billID := processEmail(s.DB, issuer, parsed)
		switch parsedAs {
		case "transaction":
			result.Transactions++
		case "bill":
			result.Bills++
		default:
			result.Unrecognized++
		}

		// message_id (the FK into the shared mail index) stays null until Money
		// ingest reads from that index instead of fetching its own mail.
		s.DB.Exec(`
			INSERT INTO message_links (mail_account_id, rfc_message_id, uid, sender, subject, received_at, parsed_as, transaction_id, bill_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, parsed.MessageID, msg.UID, parsed.From, parsed.Subject, parsed.Date, parsedAs, txnID, billID)
	}

	s.DB.Exec(`
		INSERT INTO money_ingest_state (mail_account_id, last_uid, last_synced_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT(mail_account_id) DO UPDATE SET
			last_uid = excluded.last_uid,
			last_synced_at = excluded.last_synced_at`, id, maxUID)

	httpx.WriteJSON(w, 200, result)
}

// processEmail parses a single email's content and, if recognized, persists
// a transaction or bill. Returns ("transaction"|"bill"|"unrecognized", txnID, billID).
func processEmail(db *sql.DB, issuer string, e *emailparse.Email) (string, *int64, *int64) {
	if emailparse.IsBillEmail(e.Subject) {
		bill := emailparse.ParseBillEmail(issuer, e.Subject, e.TextBody)
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
		if emailparse.IsHDFCBalanceUpdate(e.TextBody) {
			return "unrecognized", nil, nil // snapshot only, not actionable
		}
		txn, ok = emailparse.ParseHDFCEmail(e.Subject, e.TextBody)
	case "Axis":
		txn, ok = emailparse.ParseAxisEmail(e.Subject, e.TextBody)
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
	err := db.QueryRow(`SELECT id FROM finance_accounts WHERE account_number LIKE '%' || ?`, last4).Scan(&id)
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
	err := db.QueryRow(`SELECT id FROM finance_accounts WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id
	}
	accType := "credit_card"
	res, err := db.Exec(`
		INSERT INTO finance_accounts (name, bank, account_type, account_number, currency)
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
