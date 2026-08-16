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

	// syncMu serializes mailbox access per account id, so a manual sync, the
	// background ticker and a bulk action can never open competing IMAP
	// sessions for one account. Values are buffered channels of size 1 rather
	// than Mutexes, so waiters can give up when their request context does.
	syncMu sync.Map

	// jobs tracks detached long-running work (sync, classification).
	jobs *jobRunner

	// AfterSync, if set, runs inside the per-account lock with the live
	// connection immediately after the index is updated. Money's finance
	// ingest hangs off this, which is what keeps one mailbox to one IMAP
	// session: sync indexes the headers, then ingest pulls bodies for the
	// handful of messages it cares about, over the same connection.
	AfterSync func(ctx context.Context, accountID int64, provider mail.Provider) error
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
	mux.HandleFunc("GET /api/mail/accounts/{id}/messages/{msgId}/parts/{partId}", s.handleGetMessagePart)
	mux.HandleFunc("DELETE /api/mail/accounts/{id}/messages/{msgId}", s.handleDeleteMessage)
	mux.HandleFunc("POST /api/mail/accounts/{id}/messages/{msgId}/flag", s.handleSetFlag)
	// Literal segments like "bulk-delete" outrank the "{msgId}" wildcard in
	// stdlib's precedence rules (see Money's Register for the same pattern),
	// so these coexist with the single-message routes above regardless of order.
	mux.HandleFunc("POST /api/mail/accounts/{id}/messages/bulk-delete", s.handleBulkDeleteMessages)
	mux.HandleFunc("POST /api/mail/accounts/{id}/messages/bulk-flag", s.handleBulkSetFlag)
	mux.HandleFunc("POST /api/mail/accounts/{id}/messages/{msgId}/summarize", s.handleSummarize)
	mux.HandleFunc("POST /api/mail/accounts/{id}/sync", s.handleSync)
	mux.HandleFunc("GET /api/mail/accounts/{id}/sync/status", s.handleSyncStatus)
	mux.HandleFunc("GET /api/mail/accounts/{id}/insights", s.handleInsights)
	mux.HandleFunc("GET /api/mail/accounts/{id}/senders", s.handleListSenders)
	mux.HandleFunc("POST /api/mail/accounts/{id}/senders/unsubscribe", s.handleUnsubscribeSender)
	mux.HandleFunc("POST /api/mail/accounts/{id}/senders/resubscribe", s.handleResubscribeSender)
	mux.HandleFunc("POST /api/mail/accounts/{id}/senders/mark-unsubscribed", s.handleMarkSenderUnsubscribed)
	mux.HandleFunc("POST /api/mail/accounts/{id}/senders/bulk-flag", s.handleBulkSenderFlag)
	mux.HandleFunc("POST /api/mail/accounts/{id}/senders/bulk-delete", s.handleBulkDeleteSenders)
	mux.HandleFunc("POST /api/mail/accounts/{id}/senders/bulk-archive", s.handleBulkArchiveSenders)
	// Sender-level bulk actions run detached, so the client polls this for
	// progress rather than holding a request open for thousands of messages.
	mux.HandleFunc("GET /api/mail/accounts/{id}/senders/bulk-status", s.handleBulkSenderStatus)
	mux.HandleFunc("POST /api/mail/accounts/{id}/classify", s.handleClassify)
	mux.HandleFunc("GET /api/mail/accounts/{id}/classify/status", s.handleClassifyStatus)
}

// WithProvider opens one connection to the given mailbox and runs fn against
// it, holding the per-account lock for the duration. Everything that touches a
// mailbox goes through here, so a manual sync, the background ticker, and
// Money's ingest can never open competing IMAP sessions for one account.
//
// The wait for that lock honours ctx. A background sync over a large mailbox
// can hold it for a while, and a user action queued behind one used to block
// with no way out — the request simply hung until something upstream gave up,
// which reads as "the button does nothing". Now it fails with a reason.
func (s *Server) WithProvider(ctx context.Context, accountID int64, fn func(mail.Provider) error) error {
	// A buffered channel rather than a Mutex, because a Mutex cannot be
	// acquired with a timeout or a cancellation.
	lockAny, _ := s.syncMu.LoadOrStore(accountID, make(chan struct{}, 1))
	lock := lockAny.(chan struct{})
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return fmt.Errorf("mailbox %d is busy with another operation: %w", accountID, ctx.Err())
	}

	creds, err := s.loadAccountCredentials(accountID)
	if err != nil {
		return fmt.Errorf("account not found: %w", err)
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		return fmt.Errorf("could not connect to mail server: %w", err)
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	return fn(provider)
}

// syncAccount brings the local index up to date, then hands the same live
// connection to AfterSync so downstream consumers don't need one of their own.
func (s *Server) syncAccount(ctx context.Context, accountID int64, folder string) (*wsync.Result, error) {
	var result *wsync.Result
	err := s.WithProvider(ctx, accountID, func(provider mail.Provider) error {
		var err error
		result, err = wsync.SyncAccount(ctx, s.DB, provider, accountID, folder)
		if err != nil {
			return err
		}
		if s.AfterSync != nil {
			// A downstream failure must not invalidate the sync that just
			// succeeded; the index is already committed either way.
			if err := s.AfterSync(ctx, accountID, provider); err != nil {
				log.Printf("sync: after-sync hook for account %d failed: %v", accountID, err)
			}
		}
		return nil
	})
	return result, err
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
