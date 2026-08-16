package moneyapi

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"strings"

	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/money/models"
	"wollow/backend/internal/money/parsers"
)

type importPreviewResponse struct {
	// Kind routes the client to the right confirm step: "statement" for a
	// transaction export, "deposits" for an FD/RD summary. One upload endpoint
	// handles both because the user just has a file, not a taxonomy.
	Kind             string               `json:"kind"`
	FileName         string               `json:"fileName"`
	Bank             string               `json:"bank"`
	AccountType      string               `json:"accountType"`
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
	// Deposits is populated instead of Transactions when Kind is "deposits".
	Deposits []models.ParsedDeposit `json:"deposits,omitempty"`
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
		httpx.WriteError(w, 400, "could not read uploaded file: "+err.Error())
		return
	}
	defer os.Remove(tmpPath)

	// An FD summary and an account statement arrive through the same upload,
	// and the user has no reason to know they are parsed differently.
	if parsers.IsDepositSummary(tmpPath) {
		s.previewDepositSummary(w, tmpPath, fileName)
		return
	}

	statement, err := parsers.ParseHDFCStatement(tmpPath)
	if err != nil {
		httpx.WriteError(w, 422, "could not parse statement: "+err.Error())
		return
	}

	resp := importPreviewResponse{
		Kind:           "statement",
		FileName:       fileName,
		Bank:           statement.Bank,
		AccountType:    statement.AccountType,
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

	resp.SuggestedAccount = s.matchStatementAccount(statement.AccountNumber)

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
			SuggestedCategory: parsers.SuggestCategory(t.Narration),
			IsDuplicate:       isDup,
			DedupeHash:        t.DedupeHash,
		})
	}

	httpx.WriteJSON(w, 200, resp)
}

// matchStatementAccount finds the account a statement belongs to.
//
// An exact number match is tried first, then the last four digits — because the
// account this statement is for very often already exists as a row alert ingest
// created, and that row only ever knew the masked tail ("XXXXXXXX4125"). Without
// the suffix arm the import offers to create a *second* account for the same
// bank account, and the balance ends up split across both.
func (s *Server) matchStatementAccount(accountNumber string) *matchedAccount {
	accountNumber = strings.TrimSpace(accountNumber)
	if accountNumber == "" {
		return nil
	}

	var acc matchedAccount
	if err := s.DB.QueryRow(
		`SELECT id, name FROM finance_accounts WHERE account_number = ? ORDER BY id LIMIT 1`,
		accountNumber,
	).Scan(&acc.ID, &acc.Name); err == nil {
		return &acc
	}

	if len(accountNumber) < 4 {
		return nil
	}
	last4 := accountNumber[len(accountNumber)-4:]
	if err := s.DB.QueryRow(
		`SELECT id, name FROM finance_accounts
		 WHERE account_number LIKE '%' || ? AND account_number != '' ORDER BY id LIMIT 1`,
		last4,
	).Scan(&acc.ID, &acc.Name); err != nil {
		return nil
	}
	return &acc
}

// previewDepositSummary answers the upload with the holdings a deposit summary
// describes, marking the ones already on file so a re-import reads as a refresh
// rather than a duplication.
func (s *Server) previewDepositSummary(w http.ResponseWriter, tmpPath, fileName string) {
	summary, err := parsers.ParseDepositSummary(tmpPath, "HDFC")
	if err != nil {
		httpx.WriteError(w, 422, "could not parse deposit summary: "+err.Error())
		return
	}

	resp := importPreviewResponse{
		Kind:        "deposits",
		FileName:    fileName,
		Bank:        summary.Institution,
		AccountType: "investment",
		TotalRows:   len(summary.Deposits),
		Deposits:    summary.Deposits,
	}
	for i := range resp.Deposits {
		var existing int
		s.DB.QueryRow(`SELECT COUNT(*) FROM investments WHERE dedupe_key = ?`,
			resp.Deposits[i].DedupeKey).Scan(&existing)
		resp.Deposits[i].IsDuplicate = existing > 0
		if existing > 0 {
			resp.DuplicateRows++
		} else {
			resp.NewRows++
		}
	}
	httpx.WriteJSON(w, 200, resp)
}

type importDepositsRequest struct {
	FileName string                 `json:"fileName"`
	Deposits []models.ParsedDeposit `json:"deposits"`
}

// handleImportDepositsCommit writes a parsed deposit summary into investments.
//
// Rows already on file are updated rather than skipped: a summary export is a
// snapshot of the whole portfolio, so re-importing a newer one is how a
// deposit's principal (which grows with interest) stays current.
func (s *Server) handleImportDepositsCommit(w http.ResponseWriter, r *http.Request) {
	var req importDepositsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if len(req.Deposits) == 0 {
		httpx.WriteError(w, 400, "deposits is required and must be non-empty")
		return
	}

	tx, err := s.DB.Begin()
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer tx.Rollback()

	imported, updated := 0, 0
	for _, d := range req.Deposits {
		// Whether this row is new has to be established before the upsert:
		// last_insert_rowid() is left untouched by the DO UPDATE branch, so
		// reading it back reports the *previous* insert's id and every update
		// counts as an import.
		var existing int64
		tx.QueryRow(`SELECT id FROM investments WHERE dedupe_key = ? AND dedupe_key != ''`,
			d.DedupeKey).Scan(&existing)

		if _, err := tx.Exec(`
			INSERT INTO investments
				(kind, institution, name, identifier, currency, invested_amount, current_value,
				 maturity_amount, interest_rate, start_date, maturity_date, status, source, dedupe_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', 'statement', ?)
			-- The index on dedupe_key is partial (hand-entered holdings have no
			-- key), and SQLite only matches a partial index when the conflict
			-- target repeats its predicate.
			ON CONFLICT(dedupe_key) WHERE dedupe_key != '' DO UPDATE SET
				invested_amount = excluded.invested_amount,
				current_value   = excluded.current_value,
				maturity_amount = excluded.maturity_amount,
				interest_rate   = excluded.interest_rate,
				maturity_date   = excluded.maturity_date,
				updated_at      = datetime('now')`,
			d.Kind, d.Institution, d.Name, d.Identifier, defaultCurrency(d.Currency),
			d.InvestedAmount, d.InvestedAmount, nullIfZeroAmount(d.MaturityAmount),
			nullIfZeroAmount(d.InterestRate), d.StartDate, d.MaturityDate, d.DedupeKey,
		); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		if existing == 0 {
			imported++
		} else {
			updated++
		}
	}

	if _, err := tx.Exec(`
		INSERT INTO import_batches (file_name, bank, total_rows, imported_rows, duplicate_rows, status)
		VALUES (?, 'HDFC', ?, ?, ?, 'done')`,
		req.FileName, len(req.Deposits), imported, updated); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	httpx.WriteJSON(w, 200, map[string]int{"imported": imported, "updated": updated})
}

func defaultCurrency(c string) string {
	if c == "" {
		return "INR"
	}
	return c
}

// nullIfZeroAmount keeps "not reported" distinguishable from "reported as
// zero" for the optional deposit figures.
func nullIfZeroAmount(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}

type importCommitRequest struct {
	FileName  string `json:"fileName"`
	AccountID int64  `json:"accountId"` // existing account, or 0 to create one
	// AccountNumber is the number the statement itself carried. Importing into
	// an account discovered from alert mail is how the full number replaces the
	// masked tail that was all the alerts ever disclosed.
	AccountNumber string               `json:"accountNumber"`
	NewAccount    *models.Account      `json:"newAccount,omitempty"`
	Transactions  []previewTransaction `json:"transactions"`
}

// upgradeAccountNumber fills in the full account number on an account that only
// ever knew its masked tail, and records where the row came from. It refuses to
// overwrite a number that is already longer than the one offered, so importing
// an older statement can't degrade what a newer one established.
func upgradeAccountNumber(tx *sql.Tx, accountID int64, statementNumber string) {
	statementNumber = strings.TrimSpace(statementNumber)
	if accountID == 0 || statementNumber == "" {
		return
	}
	tx.Exec(`
		UPDATE finance_accounts
		SET account_number = ?, source = 'statement', updated_at = datetime('now')
		WHERE id = ? AND LENGTH(account_number) < ?`,
		statementNumber, accountID, len(statementNumber))
}

func (s *Server) handleImportHDFCCommit(w http.ResponseWriter, r *http.Request) {
	var req importCommitRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}

	accountID := req.AccountID
	if accountID == 0 {
		if req.NewAccount == nil || req.NewAccount.Name == "" {
			httpx.WriteError(w, 400, "accountId or newAccount is required")
			return
		}
		na := req.NewAccount
		if na.Currency == "" {
			na.Currency = "INR"
		}
		na.CurrentBalance = na.OpeningBalance
		res, err := s.DB.Exec(`
			INSERT INTO finance_accounts (name, bank, account_type, account_number, currency,
			                       opening_balance, current_balance, ifsc, branch)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			na.Name, na.Bank, na.AccountType, na.AccountNumber, na.Currency,
			na.OpeningBalance, na.CurrentBalance, na.IFSC, na.Branch)
		if err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		accountID, _ = res.LastInsertId()
	}

	tx, err := s.DB.Begin()
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer tx.Rollback()

	batchRes, err := tx.Exec(`
		INSERT INTO import_batches (account_id, file_name, bank, total_rows, status)
		VALUES (?, ?, 'HDFC', ?, 'processing')`, accountID, req.FileName, len(req.Transactions))
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	batchID, _ := batchRes.LastInsertId()

	upgradeAccountNumber(tx, accountID, req.AccountNumber)

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
			httpx.WriteError(w, 500, err.Error())
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
		httpx.WriteError(w, 500, err.Error())
		return
	}

	if err := ledger.RecomputeAccountBalance(tx, accountID); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	httpx.WriteJSON(w, 200, map[string]interface{}{
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
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	batches := []models.ImportBatch{}
	for rows.Next() {
		var b models.ImportBatch
		var accID sql.NullInt64
		if err := rows.Scan(&b.ID, &accID, &b.FileName, &b.Bank, &b.TotalRows,
			&b.ImportedRows, &b.DuplicateRows, &b.Status, &b.Error, &b.CreatedAt); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		if accID.Valid {
			b.AccountID = &accID.Int64
		}
		batches = append(batches, b)
	}
	httpx.WriteJSON(w, 200, batches)
}
