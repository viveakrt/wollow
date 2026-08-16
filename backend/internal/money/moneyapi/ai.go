package moneyapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"wollow/backend/internal/mail/ai"
	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/money/txnclassify"
	"wollow/backend/internal/platform/httpx"
)

// classifyJobKey is the single job key for the transaction classification
// pass. Mail keys its pass per mailbox; Money's transactions span every
// account at once, so one pass covers the product.
const classifyJobKey = "money:classify"

// buildAIProvider constructs the user's configured AI provider from the shared
// settings row — the same one Mail classification uses. Money borrows it for
// transaction classification; the user brings one API key for the whole app.
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
		if baseURL == "" {
			baseURL = "http://localhost:1234/v1"
		}
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	case "custom":
		if baseURL == "" {
			return nil, fmt.Errorf("custom AI provider requires a base URL")
		}
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	default:
		return ai.NoopProvider{}, nil
	}
}

type classifyRequest struct {
	// IDs re-classifies exactly those transactions. Empty means every
	// transaction that has never been classified.
	IDs []int64 `json:"ids"`
}

// handleClassifyTransactions starts a detached classification pass, mirroring
// Mail's POST /accounts/{id}/classify. Classifying thousands of transactions
// is one model call each, so it cannot run inside the request: the client
// polls the status endpoint instead.
func (s *Server) handleClassifyTransactions(w http.ResponseWriter, r *http.Request) {
	var req classifyRequest
	// A body is optional here — "classify everything pending" is the common
	// case and sends none.
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid body")
			return
		}
	}

	provider, err := s.buildAIProvider()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load AI provider")
		return
	}
	if _, isNoop := provider.(ai.NoopProvider); isNoop {
		httpx.WriteError(w, http.StatusUnprocessableEntity, ai.ErrNotConfigured.Error())
		return
	}

	var model string
	_ = s.DB.QueryRow(`SELECT model_name FROM settings WHERE id = 1`).Scan(&model)

	ids := req.IDs
	started := s.jobs.Start(classifyJobKey, func(ctx context.Context) (any, error) {
		s.jobs.Report(classifyJobKey, 0, 0, "transactions")
		return txnclassify.Run(ctx, s.DB, provider, model, ids, func(done, total int) {
			s.jobs.Report(classifyJobKey, done, total, "transactions")
		})
	})

	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"status":  s.jobs.State(classifyJobKey),
	})
}

func (s *Server) handleClassifyStatus(w http.ResponseWriter, r *http.Request) {
	status, err := txnclassify.CurrentStatus(s.DB)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to read status")
		return
	}
	state := s.jobs.State(classifyJobKey)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"total":       status.Total,
		"classified":  status.Classified,
		"pending":     status.Pending,
		"needsReview": status.NeedsReview,
		"running":     state.Running,
		"progress":    state.Progress,
		"error":       state.Error,
		"detail":      state.Detail,
	})
}

// handleApplyClassification writes a stored suggestion through to the
// transaction, including the parts the background pass deliberately would not
// touch: the type and transfer kind. Reclassifying a row as a transfer moves
// it out of the income/expense totals, so it happens only when a person asks
// for it.
func (s *Server) handleApplyClassification(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var c models.TransactionClassification
	var category, transferKind, counterparty string
	err = s.DB.QueryRow(`
		SELECT category, nature, transfer_kind, counterparty, merchant, payment_method
		FROM transaction_classifications WHERE transaction_id = ?`, id,
	).Scan(&category, &c.Nature, &transferKind, &counterparty, &c.Merchant, &c.PaymentMethod)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "no classification for this transaction")
		return
	}

	var accountID int64
	if err := s.DB.QueryRow(`SELECT account_id FROM transactions WHERE id = ?`, id).Scan(&accountID); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "transaction not found")
		return
	}

	var categoryID sql.NullInt64
	if category != "" {
		_ = s.DB.QueryRow(`SELECT id FROM categories WHERE name = ?`, category).Scan(&categoryID)
	}

	// Applying a transfer clears the category: a transfer has no spending
	// category, and leaving a stale one would show up in the breakdown.
	if c.Nature == "transfer" {
		if !validTransferKinds[transferKind] {
			transferKind = "self"
		}
		_, err = s.DB.Exec(`
			UPDATE transactions
			SET type = 'transfer', transfer_kind = ?, counterparty = ?, category_id = NULL
			WHERE id = ?`, transferKind, counterparty, id)
	} else {
		_, err = s.DB.Exec(`
			UPDATE transactions
			SET type = ?, transfer_kind = '', counterparty = '',
			    category_id = COALESCE(?, category_id)
			WHERE id = ?`, c.Nature, categoryID, id)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The user has acted on it, so it is resolved on both counts: applied, and
	// no longer awaiting review.
	if _, err := s.DB.Exec(
		`UPDATE transaction_classifications SET applied = 1, needs_review = 0 WHERE transaction_id = ?`, id,
	); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A type change doesn't move money, but keeping the recompute here means
	// every mutation path leaves balances in the same known-good state.
	ledger.RecomputeAccountBalance(s.DB, accountID)
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"applied": true})
}

// handleDismissClassification drops a suggestion without touching the
// transaction, so a wrong reading stops being offered.
func (s *Server) handleDismissClassification(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.DB.Exec(`
		UPDATE transaction_classifications
		SET needs_review = 0, applied = 1
		WHERE transaction_id = ?`, id); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"dismissed": true})
}
