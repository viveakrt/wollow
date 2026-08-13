// Package api wires REST handlers for accounts, messages, settings, and auth.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"wollow/backend/internal/ai"
	"wollow/backend/internal/auth"
	"wollow/backend/internal/crypto"
	"wollow/backend/internal/mail"
	wsync "wollow/backend/internal/sync"
)

type Server struct {
	DB           *sql.DB
	Box          *crypto.Box
	Auth         *auth.Authenticator
	CookieSecure bool

	// providerFactory builds a mail.Provider for an account row; overridable in tests.
	providerFactory func(creds mail.AccountCredentials) (mail.Provider, error)

	// syncMu serializes syncs per account id, so a manual sync and the
	// background ticker can never open competing IMAP sessions for one account.
	syncMu sync.Map

	// jobs tracks detached long-running work (sync, classification).
	jobs *jobRunner
}

func NewServer(database *sql.DB, box *crypto.Box, authenticator *auth.Authenticator, cookieSecure bool) *Server {
	return &Server{
		DB:           database,
		Box:          box,
		Auth:         authenticator,
		CookieSecure: cookieSecure,
		providerFactory: func(creds mail.AccountCredentials) (mail.Provider, error) {
			return mail.NewIMAPProvider(creds)
		},
		jobs: newJobRunner(),
	}
}

// syncAccount connects to IMAP and brings the local index up to date. Only one
// sync per account runs at a time.
func (s *Server) syncAccount(ctx context.Context, accountID int64, folder string) (*wsync.Result, error) {
	lockAny, _ := s.syncMu.LoadOrStore(accountID, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	creds, err := s.loadAccountCredentials(accountID)
	if err != nil {
		return nil, fmt.Errorf("account not found: %w", err)
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		return nil, fmt.Errorf("could not connect to mail server: %w", err)
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	return wsync.SyncAccount(ctx, s.DB, provider, accountID, folder)
}

// SyncAllAccounts syncs every configured account; used by the background ticker.
func (s *Server) SyncAllAccounts(ctx context.Context) {
	rows, err := s.DB.Query(`SELECT id FROM accounts`)
	if err != nil {
		log.Printf("sync: listing accounts failed: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()

	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		result, err := s.syncAccount(ctx, id, "INBOX")
		if err != nil {
			log.Printf("sync: account %d failed: %v", id, err)
			continue
		}
		if result.Added > 0 || result.Deleted > 0 {
			log.Printf("sync: account %d added=%d updated=%d deleted=%d",
				id, result.Added, result.Updated, result.Deleted)
		}
	}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/accounts", s.handleListAccounts)
	protected.HandleFunc("POST /api/accounts", s.handleCreateAccount)
	protected.HandleFunc("DELETE /api/accounts/{id}", s.handleDeleteAccount)
	protected.HandleFunc("GET /api/accounts/{id}/messages", s.handleListMessages)
	protected.HandleFunc("GET /api/accounts/{id}/messages/{msgId}", s.handleGetMessage)
	protected.HandleFunc("DELETE /api/accounts/{id}/messages/{msgId}", s.handleDeleteMessage)
	protected.HandleFunc("POST /api/accounts/{id}/messages/{msgId}/flag", s.handleSetFlag)
	protected.HandleFunc("POST /api/accounts/{id}/messages/{msgId}/summarize", s.handleSummarize)
	protected.HandleFunc("POST /api/accounts/{id}/sync", s.handleSync)
	protected.HandleFunc("GET /api/accounts/{id}/sync/status", s.handleSyncStatus)
	protected.HandleFunc("GET /api/accounts/{id}/insights", s.handleInsights)
	protected.HandleFunc("POST /api/accounts/{id}/classify", s.handleClassify)
	protected.HandleFunc("GET /api/accounts/{id}/classify/status", s.handleClassifyStatus)
	protected.HandleFunc("GET /api/settings", s.handleGetSettings)
	protected.HandleFunc("PUT /api/settings", s.handlePutSettings)

	mux.Handle("/api/", s.Auth.Middleware(protected))

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// buildAIProvider constructs the configured AI provider (or Noop if unset).
func (s *Server) buildAIProvider() (ai.Provider, error) {
	row := s.DB.QueryRow(`SELECT ai_provider, encrypted_api_key, model_name, base_url FROM settings WHERE id = 1`)
	var providerName, encryptedKey, model, baseURL string
	if err := row.Scan(&providerName, &encryptedKey, &model, &baseURL); err != nil {
		return nil, err
	}
	if providerName == "" || providerName == "none" {
		return ai.NoopProvider{}, nil
	}
	apiKey, err := s.Box.Decrypt(encryptedKey)
	if err != nil {
		return nil, err
	}
	switch providerName {
	case "anthropic":
		return ai.NewAnthropicProvider(apiKey, model), nil
	case "openai":
		return ai.NewOpenAIProvider(apiKey, model, ""), nil
	case "lmstudio":
		// LM Studio's local server is OpenAI-compatible and normally requires
		// no API key; default to its standard local port if unset.
		if baseURL == "" {
			baseURL = "http://localhost:1234/v1"
		}
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	case "custom":
		// Any OpenAI-compatible endpoint (self-hosted, proxy, etc). The user
		// must supply the base URL themselves; there's no sane default.
		if baseURL == "" {
			return nil, fmt.Errorf("custom AI provider requires a base URL")
		}
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	default:
		return ai.NoopProvider{}, nil
	}
}
