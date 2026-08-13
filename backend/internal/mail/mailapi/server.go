// Package mailapi wires the REST handlers for the Mail product: mailboxes,
// messages, sync, and AI classification. Everything it mounts lives under
// /api/mail and sits behind the platform session middleware.
package mailapi

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"sync"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/mail/ai"
	wsync "wollow/backend/internal/mail/sync"
	"wollow/backend/internal/platform/crypto"
)

type Server struct {
	DB  *sql.DB
	Box *crypto.Box

	// providerFactory builds a mail.Provider for an account row; overridable in tests.
	providerFactory func(creds mail.AccountCredentials) (mail.Provider, error)

	// syncMu serializes syncs per account id, so a manual sync and the
	// background ticker can never open competing IMAP sessions for one account.
	syncMu sync.Map

	// jobs tracks detached long-running work (sync, classification).
	jobs *jobRunner
}

func NewServer(database *sql.DB, box *crypto.Box) *Server {
	return &Server{
		DB:  database,
		Box: box,
		providerFactory: func(creds mail.AccountCredentials) (mail.Provider, error) {
			return mail.NewIMAPProvider(creds)
		},
		jobs: newJobRunner(),
	}
}

// Register mounts every Mail route. The caller is responsible for wrapping the
// mux in the session middleware — nothing here is public.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mail/accounts", s.handleListAccounts)
	mux.HandleFunc("POST /api/mail/accounts", s.handleCreateAccount)
	mux.HandleFunc("DELETE /api/mail/accounts/{id}", s.handleDeleteAccount)
	mux.HandleFunc("GET /api/mail/accounts/{id}/messages", s.handleListMessages)
	mux.HandleFunc("GET /api/mail/accounts/{id}/messages/{msgId}", s.handleGetMessage)
	mux.HandleFunc("DELETE /api/mail/accounts/{id}/messages/{msgId}", s.handleDeleteMessage)
	mux.HandleFunc("POST /api/mail/accounts/{id}/messages/{msgId}/flag", s.handleSetFlag)
	mux.HandleFunc("POST /api/mail/accounts/{id}/messages/{msgId}/summarize", s.handleSummarize)
	mux.HandleFunc("POST /api/mail/accounts/{id}/sync", s.handleSync)
	mux.HandleFunc("GET /api/mail/accounts/{id}/sync/status", s.handleSyncStatus)
	mux.HandleFunc("GET /api/mail/accounts/{id}/insights", s.handleInsights)
	mux.HandleFunc("POST /api/mail/accounts/{id}/classify", s.handleClassify)
	mux.HandleFunc("GET /api/mail/accounts/{id}/classify/status", s.handleClassifyStatus)
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

// SyncAllAccounts syncs every enabled mailbox; used by the background ticker.
func (s *Server) SyncAllAccounts(ctx context.Context) {
	rows, err := s.DB.Query(`SELECT id FROM mail_accounts WHERE enabled = 1`)
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

// buildAIProvider constructs the configured AI provider (or Noop if unset).
// The settings row is platform-owned but read here because Mail is currently
// the only product that calls a model.
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
