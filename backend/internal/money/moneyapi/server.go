// Package moneyapi wires the REST handlers for the Money product: finance
// accounts, transactions, categories, bills, statement import, and transfer
// matching. Everything it mounts lives under /api/money and sits behind the
// platform session middleware — none of it is public.
package moneyapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"wollow/backend/internal/money/ingest"
	"wollow/backend/internal/platform/crypto"
	"wollow/backend/internal/platform/jobs"
)

// MailSession runs fn with a connection to the given mailbox, serialized
// against Mail's own sync so only one IMAP session per mailbox exists at a
// time. It is supplied by main.go rather than imported: Money knows it needs
// somewhere to fetch raw bodies from, but nothing about how Mail connects.
type MailSession func(ctx context.Context, accountID int64, fn func(ingest.RawFetcher) error) error

type Server struct {
	DB *sql.DB
	// Box encrypts issuer PDF passwords at rest.
	Box *crypto.Box
	// mailSession borrows a live mailbox connection; nil in tests that never
	// touch ingest.
	mailSession MailSession
	// jobs tracks detached long-running work — today, the AI classification
	// pass, which is one model call per transaction and so cannot run inside
	// a request.
	jobs *jobs.Runner
}

func NewServer(database *sql.DB, box *crypto.Box, mailSession MailSession) *Server {
	return &Server{DB: database, Box: box, mailSession: mailSession, jobs: jobs.NewRunner()}
}

func (s *Server) withMailSession(ctx context.Context, accountID int64, fn func(ingest.RawFetcher) error) error {
	if s.mailSession == nil {
		return fmt.Errorf("mail sessions are not configured on this server")
	}
	return s.mailSession(ctx, accountID, fn)
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
	// Accounts are added by the user, never proposed by scanning the mailbox.
	// The institution list is offered so a hand-entered account records the
	// same issuer code the parsers use, which is what lets its mail attach.
	mux.HandleFunc("GET /api/money/institutions", s.handleListInstitutions)
	mux.HandleFunc("GET /api/money/accounts/{id}", s.handleGetAccount)
	mux.HandleFunc("PUT /api/money/accounts/{id}", s.handleUpdateAccount)
	mux.HandleFunc("DELETE /api/money/accounts/{id}", s.handleDeleteAccount)

	mux.HandleFunc("GET /api/money/categories", s.handleListCategories)
	mux.HandleFunc("POST /api/money/categories", s.handleCreateCategory)

	mux.HandleFunc("GET /api/money/transactions", s.handleListTransactions)
	mux.HandleFunc("POST /api/money/transactions", s.handleCreateTransaction)
	mux.HandleFunc("POST /api/money/transactions/bulk-delete", s.handleBulkDeleteTransactions)
	mux.HandleFunc("POST /api/money/transactions/bulk-categorize", s.handleBulkCategorizeTransactions)
	mux.HandleFunc("POST /api/money/transactions/bulk-mark-transfer", s.handleBulkMarkTransfer)
	// AI classification mirrors Mail's: a detached pass the client polls,
	// storing one structured reading per transaction rather than a category
	// guess. Literal segments outrank "{id}", so these coexist with the
	// per-transaction routes below.
	mux.HandleFunc("POST /api/money/transactions/classify", s.handleClassifyTransactions)
	mux.HandleFunc("GET /api/money/transactions/classify/status", s.handleClassifyStatus)
	mux.HandleFunc("POST /api/money/transactions/{id}/apply-classification", s.handleApplyClassification)
	mux.HandleFunc("POST /api/money/transactions/{id}/dismiss-classification", s.handleDismissClassification)
	mux.HandleFunc("POST /api/money/transactions/link-transfer", s.handleLinkTransfer)
	mux.HandleFunc("POST /api/money/transactions/{id}/unlink-transfer", s.handleUnlinkTransfer)
	mux.HandleFunc("GET /api/money/transactions/{id}", s.handleGetTransaction)
	mux.HandleFunc("PUT /api/money/transactions/{id}", s.handleUpdateTransaction)
	mux.HandleFunc("DELETE /api/money/transactions/{id}", s.handleDeleteTransaction)

	mux.HandleFunc("GET /api/money/transfer-suggestions", s.handleListTransferSuggestions)
	mux.HandleFunc("POST /api/money/transfer-suggestions/scan", s.handleScanTransferSuggestions)
	mux.HandleFunc("POST /api/money/transfer-suggestions/{id}/confirm", s.handleConfirmTransferSuggestion)
	mux.HandleFunc("POST /api/money/transfer-suggestions/{id}/dismiss", s.handleDismissTransferSuggestion)

	mux.HandleFunc("GET /api/money/investments", s.handleListInvestments)
	mux.HandleFunc("GET /api/money/investments/summary", s.handleInvestmentSummary)
	mux.HandleFunc("POST /api/money/investments", s.handleCreateInvestment)
	mux.HandleFunc("PUT /api/money/investments/{id}", s.handleUpdateInvestment)
	mux.HandleFunc("DELETE /api/money/investments/{id}", s.handleDeleteInvestment)
	// The orders that built a position, and the price that values it. Money
	// has no market feed, so the price is entered rather than fetched.
	mux.HandleFunc("GET /api/money/investments/{id}/trades", s.handleListInvestmentTrades)
	mux.HandleFunc("POST /api/money/investments/{id}/price", s.handleSetInvestmentPrice)

	// One upload endpoint parses both an account statement and a deposit
	// summary and says which it found; the commit routes differ because what
	// they write does. The /hdfc/ prefix is kept for compatibility with links
	// and clients that predate the deposit path.
	mux.HandleFunc("POST /api/money/import/hdfc/preview", s.handleImportHDFCPreview)
	mux.HandleFunc("POST /api/money/import/hdfc/commit", s.handleImportHDFCCommit)
	mux.HandleFunc("POST /api/money/import/deposits/commit", s.handleImportDepositsCommit)
	mux.HandleFunc("GET /api/money/import/batches", s.handleListImportBatches)

	// Mailboxes are connected and removed on the Mail side (/api/mail/accounts)
	// — there is one credential store now. Money only reads the list and asks
	// for a finance ingest pass over a given mailbox.
	mux.HandleFunc("GET /api/money/email-accounts", s.handleListEmailAccounts)
	mux.HandleFunc("POST /api/money/email-accounts/{id}/sync", s.handleSyncEmailAccount)

	// Exchange rates that bring foreign holdings into the rupee net worth.
	mux.HandleFunc("GET /api/money/fx-rates", s.handleListFXRates)
	mux.HandleFunc("POST /api/money/fx-rates", s.handleSetFXRate)

	mux.HandleFunc("GET /api/money/bills", s.handleListBills)

	mux.HandleFunc("GET /api/money/pdf-passwords", s.handleListPDFPasswords)
	mux.HandleFunc("POST /api/money/pdf-passwords", s.handleSetPDFPassword)
	mux.HandleFunc("POST /api/money/pdf-attachments/parse-pending", s.handleParsePendingBillPDFs)
}
