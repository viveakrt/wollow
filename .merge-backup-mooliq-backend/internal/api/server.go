package api

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type Server struct {
	DB     *sql.DB
	Router *chi.Mux
}

func NewServer(database *sql.DB) *Server {
	s := &Server{DB: database, Router: chi.NewRouter()}

	s.Router.Use(middleware.Logger)
	s.Router.Use(middleware.Recoverer)
	s.Router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	s.routes()
	return s
}

func (s *Server) routes() {
	r := s.Router
	r.Route("/api", func(r chi.Router) {
		r.Get("/dashboard/summary", s.handleDashboardSummary)

		r.Route("/accounts", func(r chi.Router) {
			r.Get("/", s.handleListAccounts)
			r.Post("/", s.handleCreateAccount)
			r.Post("/bulk-delete", s.handleBulkDeleteAccounts)
			r.Get("/{id}", s.handleGetAccount)
			r.Put("/{id}", s.handleUpdateAccount)
			r.Delete("/{id}", s.handleDeleteAccount)
		})

		r.Route("/categories", func(r chi.Router) {
			r.Get("/", s.handleListCategories)
			r.Post("/", s.handleCreateCategory)
		})

		r.Route("/transactions", func(r chi.Router) {
			r.Get("/", s.handleListTransactions)
			r.Post("/", s.handleCreateTransaction)
			r.Post("/bulk-delete", s.handleBulkDeleteTransactions)
			r.Post("/bulk-categorize", s.handleBulkCategorizeTransactions)
			r.Post("/link-transfer", s.handleLinkTransfer)
			r.Post("/{id}/unlink-transfer", s.handleUnlinkTransfer)
			r.Get("/{id}", s.handleGetTransaction)
			r.Put("/{id}", s.handleUpdateTransaction)
			r.Delete("/{id}", s.handleDeleteTransaction)
		})

		r.Route("/transfer-suggestions", func(r chi.Router) {
			r.Get("/", s.handleListTransferSuggestions)
			r.Post("/scan", s.handleScanTransferSuggestions)
			r.Post("/{id}/confirm", s.handleConfirmTransferSuggestion)
			r.Post("/{id}/dismiss", s.handleDismissTransferSuggestion)
		})

		r.Route("/import", func(r chi.Router) {
			r.Post("/hdfc/preview", s.handleImportHDFCPreview)
			r.Post("/hdfc/commit", s.handleImportHDFCCommit)
			r.Get("/batches", s.handleListImportBatches)
		})

		r.Route("/email-accounts", func(r chi.Router) {
			r.Get("/", s.handleListEmailAccounts)
			r.Post("/", s.handleConnectEmailAccount)
			r.Delete("/{id}", s.handleDeleteEmailAccount)
			r.Post("/{id}/sync", s.handleSyncEmailAccount)
		})

		r.Get("/bills", s.handleListBills)

		r.Route("/pdf-passwords", func(r chi.Router) {
			r.Get("/", s.handleListPDFPasswords)
			r.Post("/", s.handleSetPDFPassword)
		})
		r.Post("/pdf-attachments/parse-pending", s.handleParsePendingBillPDFs)
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
}

func (s *Server) Start(addr string) error {
	log.Printf("mooliq backend listening on %s", addr)
	return http.ListenAndServe(addr, s.Router)
}
