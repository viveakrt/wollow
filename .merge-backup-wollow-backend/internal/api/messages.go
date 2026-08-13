package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"wollow/backend/internal/ai"
	"wollow/backend/internal/classifier"
)

// storedMessage is a row from the local index plus its classification, if the
// message has been classified yet.
type storedMessage struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	From    string `json:"from"`
	Date    string `json:"date"`
	Seen    bool   `json:"seen"`
	Flagged bool   `json:"flagged"`
	Snippet string `json:"snippet"`
	Size    int64  `json:"size"`

	Category         string  `json:"category,omitempty"`
	Subcategory      string  `json:"subcategory,omitempty"`
	SenderGroup      string  `json:"senderGroup,omitempty"`
	Priority         string  `json:"priority,omitempty"`
	Action           string  `json:"action,omitempty"`
	RequiresResponse bool    `json:"requiresResponse"`
	Confidence       float64 `json:"confidence,omitempty"`
	AISummary        string  `json:"aiSummary,omitempty"`
	Classified       bool    `json:"classified"`
}

// handleListMessages serves the inbox from the local index rather than going
// to IMAP, so listing is fast and can carry stored classifications.
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	// Filters are applied in SQL so a smart view reflects the whole mailbox,
	// not just the pages the client happens to have loaded.
	where := `m.account_id = ? AND m.folder = ?`
	args := []any{id, folder}

	if view := r.URL.Query().Get("view"); view != "" {
		clause, ok := smartViewClauses[view]
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown view: "+view)
			return
		}
		where += ` AND (` + clause + `)`
	}
	if category := r.URL.Query().Get("category"); category != "" {
		where += ` AND c.category = ?`
		args = append(args, category)
	}
	if priority := r.URL.Query().Get("priority"); priority != "" {
		where += ` AND c.priority = ?`
		args = append(args, priority)
	}
	if sender := r.URL.Query().Get("sender"); sender != "" {
		where += ` AND m.from_email = ?`
		args = append(args, sender)
	}
	if search := r.URL.Query().Get("q"); search != "" {
		where += ` AND (m.subject LIKE ? OR m.from_name LIKE ? OR m.from_email LIKE ?)`
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}

	args = append(args, limit, offset)

	rows, err := s.DB.Query(`
		SELECT m.uid, m.subject, m.from_name, m.from_email, m.date, m.seen,
		       m.flagged, m.snippet, m.size,
		       c.category, c.subcategory, c.sender_group, c.priority, c.action,
		       c.requires_response, c.confidence, c.summary
		FROM messages m
		LEFT JOIN classifications c ON c.message_id = m.id
		WHERE `+where+`
		ORDER BY m.date DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	defer rows.Close()

	messages := []storedMessage{}
	for rows.Next() {
		var (
			m                storedMessage
			uid              int64
			fromName         string
			fromEmail        string
			seen, flagged    int
			category         sql.NullString
			subcategory      sql.NullString
			senderGroup      sql.NullString
			priority         sql.NullString
			action           sql.NullString
			requiresResponse sql.NullInt64
			confidence       sql.NullFloat64
			summary          sql.NullString
		)
		if err := rows.Scan(
			&uid, &m.Subject, &fromName, &fromEmail, &m.Date, &seen,
			&flagged, &m.Snippet, &m.Size,
			&category, &subcategory, &senderGroup, &priority, &action,
			&requiresResponse, &confidence, &summary,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read message")
			return
		}

		m.ID = strconv.FormatInt(uid, 10)
		m.From = fromEmail
		if fromName != "" {
			m.From = fromName + " <" + fromEmail + ">"
		}
		m.Seen = seen != 0
		m.Flagged = flagged != 0

		if category.Valid {
			m.Classified = true
			m.Category = category.String
			m.Subcategory = subcategory.String
			m.SenderGroup = senderGroup.String
			m.Priority = priority.String
			m.Action = action.String
			m.RequiresResponse = requiresResponse.Int64 != 0
			m.Confidence = confidence.Float64
			m.AISummary = summary.String
		}

		messages = append(messages, m)
	}

	writeJSON(w, http.StatusOK, messages)
}

// handleSync kicks off a background sync. A full mailbox sync takes far longer
// than any proxy timeout, so it runs detached and the client polls for status.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	key := fmt.Sprintf("sync:%d:%s", id, folder)
	started := s.jobs.Start(key, func(ctx context.Context) (any, error) {
		return s.syncAccount(ctx, id, folder)
	})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"status":  s.jobs.State(key),
	})
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	var stored int
	_ = s.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE account_id = ? AND folder = ?`, id, folder,
	).Scan(&stored)

	state := s.jobs.State(fmt.Sprintf("sync:%d:%s", id, folder))
	writeJSON(w, http.StatusOK, map[string]any{
		"running":    state.Running,
		"startedAt":  state.StartedAt,
		"finishedAt": state.FinishedAt,
		"error":      state.Error,
		"detail":     state.Detail,
		"stored":     stored,
	})
}

// handleClassify starts a background classification pass over unclassified
// messages. Local models take seconds per message, so this is never inline.
func (s *Server) handleClassify(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	aiProvider, err := s.buildAIProvider()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load AI provider")
		return
	}
	if _, isNoop := aiProvider.(ai.NoopProvider); isNoop {
		writeError(w, http.StatusUnprocessableEntity, ai.ErrNotConfigured.Error())
		return
	}

	var model string
	_ = s.DB.QueryRow(`SELECT model_name FROM settings WHERE id = 1`).Scan(&model)

	key := fmt.Sprintf("classify:%d", id)
	started := s.jobs.Start(key, func(ctx context.Context) (any, error) {
		classified, err := classifier.Run(ctx, s.DB, aiProvider, model, id)
		return map[string]int{"classified": classified}, err
	})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"status":  s.jobs.State(key),
	})
}

func (s *Server) handleClassifyStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	status, err := classifier.CurrentStatus(s.DB, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read status")
		return
	}

	state := s.jobs.State(fmt.Sprintf("classify:%d", id))
	writeJSON(w, http.StatusOK, map[string]any{
		"total":      status.Total,
		"classified": status.Classified,
		"pending":    status.Pending,
		"running":    state.Running,
		"error":      state.Error,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")
	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
		return
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	msg, err := provider.GetMessage(r.Context(), folder, msgID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch message: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")
	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
		return
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	if err := provider.DeleteMessage(r.Context(), folder, msgID); err != nil {
		writeError(w, http.StatusBadGateway, "failed to delete message: "+err.Error())
		return
	}

	// Drop it from the local index too, so the list reflects the deletion
	// immediately instead of waiting for the next sync.
	if _, err := s.DB.Exec(
		`DELETE FROM messages WHERE account_id = ? AND folder = ? AND uid = ?`,
		id, folder, msgID,
	); err != nil {
		log.Printf("delete: local index cleanup failed for account %d uid %s: %v", id, msgID, err)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type setFlagRequest struct {
	Flag  string `json:"flag"`
	Value bool   `json:"value"`
}

func (s *Server) handleSetFlag(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")

	var req setFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Flag == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
		return
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	if err := provider.SetFlag(r.Context(), folder, msgID, req.Flag, req.Value); err != nil {
		writeError(w, http.StatusBadGateway, "failed to set flag: "+err.Error())
		return
	}

	// Mirror the flag change into the local index so the list stays correct
	// between syncs.
	var column string
	switch req.Flag {
	case `\Seen`:
		column = "seen"
	case `\Flagged`:
		column = "flagged"
	}
	if column != "" {
		query := `UPDATE messages SET ` + column + ` = ? WHERE account_id = ? AND folder = ? AND uid = ?`
		value := 0
		if req.Value {
			value = 1
		}
		if _, err := s.DB.Exec(query, value, id, folder, msgID); err != nil {
			log.Printf("flag: local index update failed for account %d uid %s: %v", id, msgID, err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSummarize(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")

	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
		return
	}
	if closer, ok := provider.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	msg, err := provider.GetMessage(r.Context(), folder, msgID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch message: "+err.Error())
		return
	}

	aiProvider, err := s.buildAIProvider()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load AI provider")
		return
	}

	bodyPreview := msg.BodyText
	if bodyPreview == "" {
		bodyPreview = msg.BodyHTML
	}

	summary, err := aiProvider.Summarize(r.Context(), msg.Subject, bodyPreview)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"summary": summary})
}
