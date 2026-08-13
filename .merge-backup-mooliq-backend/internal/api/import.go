package api

import (
	"database/sql"
	"io"
	"net/http"
	"os"

	"mooliq/backend/internal/models"
	"mooliq/backend/internal/parser"
)

type importPreviewResponse struct {
	FileName         string               `json:"fileName"`
	Bank             string               `json:"bank"`
	AccountNumber    string               `json:"accountNumber"`
	AccountBranch    string               `json:"accountBranch"`
	IFSC             string               `json:"ifsc"`
	StatementFrom    string               `json:"statementFrom"`
	StatementTo      string               `json:"statementTo"`
	OpeningBalance   float64              `json:"openingBalance"`
	ClosingBalance   float64              `json:"closingBalance"`
	TotalRows        int                  `json:"totalRows"`
	NewRows          int                  `json:"newRows"`
	DuplicateRows    int                  `json:"duplicateRows"`
	SuggestedAccount *matchedAccount      `json:"suggestedAccount,omitempty"`
	Transactions     []previewTransaction `json:"transactions"`
}

type matchedAccount struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type previewTransaction struct {
	models.ParsedTransaction
	SuggestedCategory string `json:"suggestedCategory"`
	IsDuplicate       bool   `json:"isDuplicate"`
	DedupeHash        string `json:"dedupeHash"`
}

func saveUploadToTemp(r *http.Request) (string, string, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return "", "", err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "mooliq-import-*.xls")
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()

	if _, err := io.Copy(tmp, file); err != nil {
		return "", "", err
	}
	return tmp.Name(), header.Filename, nil
}

func (s *Server) handleImportHDFCPreview(w http.ResponseWriter, r *http.Request) {
	tmpPath, fileName, err := saveUploadToTemp(r)
	if err != nil {
		writeError(w, 400, "could not read uploaded file: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)

	statement, err := parser.ParseHDFCStatement(tmpPath)
	if err != nil {
		writeError(w, 422, "could not parse statement: "+err.Error())
		return
	}

	resp := importPreviewResponse{
		FileName:       fileName,
		Bank:           statement.Bank,
		AccountNumber:  statement.AccountNumber,
		AccountBranch:  statement.AccountBranch,
		IFSC:           statement.IFSC,
		StatementFrom:  statement.StatementFrom,
		StatementTo:    statement.StatementTo,
		OpeningBalance: statement.OpeningBalance,
		ClosingBalance: statement.ClosingBalance,
		TotalRows:      len(statement.Transactions),
		Transactions:   make([]previewTransaction, 0, len(statement.Transactions)),
	}

	// Try to find an existing account by masked account number suffix.
	if statement.AccountNumber != "" {
		var acc matchedAccount
		err := s.DB.QueryRow(`SELECT id, name FROM accounts WHERE account_number = ? LIMIT 1`,
			statement.AccountNumber).Scan(&acc.ID, &acc.Name)
		if err == nil {
			resp.SuggestedAccount = &acc
		}
	}

	for _, t := range statement.Transactions {
		isDup := false
		if resp.SuggestedAccount != nil {
			var count int
			s.DB.QueryRow(`SELECT COUNT(*) FROM transactions WHERE account_id=? AND dedupe_hash=?`,
				resp.SuggestedAccount.ID, t.DedupeHash).Scan(&count)
			isDup = count > 0
		}
		if isDup {
			resp.DuplicateRows++
		} else {
			resp.NewRows++
		}
		resp.Transactions = append(resp.Transactions, previewTransaction{
			ParsedTransaction: t,
			SuggestedCategory: parser.SuggestCategory(t.Narration),
			IsDuplicate:       isDup,
			DedupeHash:        t.DedupeHash,
		})
	}

	writeJSON(w, 200, resp)
}

type importCommitRequest struct {
	FileName     string               `json:"fileName"`
	AccountID    int64                `json:"accountId"` // existing account, or 0 to create one
	NewAccount   *models.Account      `json:"newAccount,omitempty"`
	Transactions []previewTransaction `json:"transactions"`
}

func (s *Server) handleImportHDFCCommit(w http.ResponseWriter, r *http.Request) {
	var req importCommitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, "invalid body")
		return
	}

	accountID := req.AccountID
	if accountID == 0 {
		if req.NewAccount == nil || req.NewAccount.Name == "" {
			writeError(w, 400, "accountId or newAccount is required")
			return
		}
		na := req.NewAccount
		if na.Currency == "" {
			na.Currency = "INR"
		}
		na.CurrentBalance = na.OpeningBalance
		res, err := s.DB.Exec(`
			INSERT INTO accounts (name, bank, account_type, account_number, currency,
			                       opening_balance, current_balance, ifsc, branch)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			na.Name, na.Bank, na.AccountType, na.AccountNumber, na.Currency,
			na.OpeningBalance, na.CurrentBalance, na.IFSC, na.Branch)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		accountID, _ = res.LastInsertId()
	}

	tx, err := s.DB.Begin()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer tx.Rollback()

	batchRes, err := tx.Exec(`
		INSERT INTO import_batches (account_id, file_name, bank, total_rows, status)
		VALUES (?, ?, 'HDFC', ?, 'processing')`, accountID, req.FileName, len(req.Transactions))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	batchID, _ := batchRes.LastInsertId()

	imported, duplicates := 0, 0
	for _, t := range req.Transactions {
		categoryID := lookupCategoryID(tx, t.SuggestedCategory)
		res, err := tx.Exec(`
			INSERT OR IGNORE INTO transactions
				(account_id, txn_date, value_date, narration, ref_no, withdrawal_amt,
				 deposit_amt, closing_balance, type, category_id, merchant, payment_method,
				 import_batch_id, dedupe_hash)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			accountID, t.TxnDate, t.ValueDate, t.Narration, t.RefNo, t.WithdrawalAmt,
			t.DepositAmt, t.ClosingBalance, t.Type, categoryID, t.Merchant, t.PaymentMethod,
			batchID, t.DedupeHash)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			imported++
		} else {
			duplicates++
		}
	}

	if _, err := tx.Exec(`
		UPDATE import_batches SET imported_rows=?, duplicate_rows=?, status='done' WHERE id=?`,
		imported, duplicates, batchID); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	if err := recomputeAccountBalance(tx, accountID); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"batchId":       batchID,
		"accountId":     accountID,
		"importedRows":  imported,
		"duplicateRows": duplicates,
	})
}

func lookupCategoryID(tx *sql.Tx, name string) interface{} {
	if name == "" {
		return nil
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM categories WHERE name = ?`, name).Scan(&id); err != nil {
		return nil
	}
	return id
}

func (s *Server) handleListImportBatches(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`
		SELECT id, account_id, file_name, bank, total_rows, imported_rows, duplicate_rows, status, error, created_at
		FROM import_batches ORDER BY id DESC LIMIT 50`)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	batches := []models.ImportBatch{}
	for rows.Next() {
		var b models.ImportBatch
		var accID sql.NullInt64
		if err := rows.Scan(&b.ID, &accID, &b.FileName, &b.Bank, &b.TotalRows,
			&b.ImportedRows, &b.DuplicateRows, &b.Status, &b.Error, &b.CreatedAt); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if accID.Valid {
			b.AccountID = &accID.Int64
		}
		batches = append(batches, b)
	}
	writeJSON(w, 200, batches)
}
