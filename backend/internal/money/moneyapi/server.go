// Package moneyapi wires the REST handlers for the Money product: finance
// accounts, transactions, categories, bills, statement import, and transfer
// matching. Everything it mounts lives under /api/money and sits behind the
// platform session middleware — none of it is public.
package moneyapi

import (
	"database/sql"
	"net/http"

	"wollow/backend/internal/platform/crypto"
)

type Server struct {
	DB *sql.DB
	// Box decrypts the mailbox password when reading finance mail, and
	// encrypts issuer PDF passwords at rest.
	Box *crypto.Box
}

func NewServer(database *sql.DB, box *crypto.Box) *Server {
	return &Server{DB: database, Box: box}
}

// Register mounts every Money route on the given mux.
//
// Routing note: chi's Route("/accounts") + Get("/") used to match a *trailing
// slash* path. Stdlib ServeMux treats "/accounts" and "/accounts/" as distinct
// patterns, so these are all registered without one. Literal segments like
// "bulk-delete" outrank "{id}" in stdlib's precedence rules, so the two coexist.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/money/dashboard/summary", s.handleDashboardSummary)

	mux.HandleFunc("GET /api/money/accounts", s.handleListAccounts)
	mux.HandleFunc("POST /api/money/accounts", s.handleCreateAccount)
	mux.HandleFunc("POST /api/money/accounts/bulk-delete", s.handleBulkDeleteAccounts)
	mux.HandleFunc("GET /api/money/accounts/{id}", s.handleGetAccount)
	mux.HandleFunc("PUT /api/money/accounts/{id}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /api/money/accounts/{id}", s.handleDeleteAccount)

	mux.HandleFunc("GET /api/money/categories", s.handleListCategories)
	mux.HandleFunc("POST /api/money/categories", s.handleCreateCategory)

	mux.HandleFunc("GET /api/money/transactions", s.handleListTransactions)
	mux.HandleFunc("POST /api/money/transactions", s.handleCreateTransaction)
	mux.HandleFunc("POST /api/money/transactions/bulk-delete", s.handleBulkDeleteTransactions)
	mux.HandleFunc("POST /api/money/transactions/bulk-categorize", s.handleBulkCategorizeTransactions)
	mux.HandleFunc("POST /api/money/transactions/link-transfer", s.handleLinkTransfer)
	mux.HandleFunc("POST /api/money/transactions/{id}/unlink-transfer", s.handleUnlinkTransfer)
	mux.HandleFunc("GET /api/money/transactions/{id}", s.handleGetTransaction)
	mux.HandleFunc("PUT /api/money/transactions/{id}", s.handleUpdateTransaction)
	mux.HandleFunc("DELETE /api/money/transactions/{id}", s.handleDeleteTransaction)

	mux.HandleFunc("GET /api/money/transfer-suggestions", s.handleListTransferSuggestions)
	mux.HandleFunc("POST /api/money/transfer-suggestions/scan", s.handleScanTransferSuggestions)
	mux.HandleFunc("POST /api/money/transfer-suggestions/{id}/confirm", s.handleConfirmTransferSuggestion)
	mux.HandleFunc("POST /api/money/transfer-suggestions/{id}/dismiss", s.handleDismissTransferSuggestion)

	mux.HandleFunc("POST /api/money/import/hdfc/preview", s.handleImportHDFCPreview)
	mux.HandleFunc("POST /api/money/import/hdfc/commit", s.handleImportHDFCCommit)
	mux.HandleFunc("GET /api/money/import/batches", s.handleListImportBatches)

	// Mailboxes are connected and removed on the Mail side (/api/mail/accounts)
	// — there is one credential store now. Money only reads the list and asks
	// for a finance ingest pass over a given mailbox.
	mux.HandleFunc("GET /api/money/email-accounts", s.handleListEmailAccounts)
	mux.HandleFunc("POST /api/money/email-accounts/{id}/sync", s.handleSyncEmailAccount)

	mux.HandleFunc("GET /api/money/bills", s.handleListBills)

	mux.HandleFunc("GET /api/money/pdf-passwords", s.handleListPDFPasswords)
	mux.HandleFunc("POST /api/money/pdf-passwords", s.handleSetPDFPassword)
	mux.HandleFunc("POST /api/money/pdf-attachments/parse-pending", s.handleParsePendingBillPDFs)
}
