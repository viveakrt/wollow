package mailapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"wollow/backend/internal/mail"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/mail/ai"
	"wollow/backend/internal/mail/classifier"
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

	// What the Money product made of this message, if anything. Present only
	// once finance ingest has looked at it.
	MoneyLink *moneyLink `json:"moneyLink,omitempty"`
}

// moneyLink is the Mail-side view of a message_links row: the join that lets
// the inbox say "this became a transaction" and link straight to it.
type moneyLink struct {
	ParsedAs      string   `json:"parsedAs"` // transaction | bill | unrecognized
	TransactionID *int64   `json:"transactionId,omitempty"`
	BillID        *int64   `json:"billId,omitempty"`
	Amount        *float64 `json:"amount,omitempty"`
	DueDate       string   `json:"dueDate,omitempty"`
	Issuer        string   `json:"issuer,omitempty"`
}

// moneyLinkColumns and scanMoneyLink keep the list and detail queries reading
// the join identically; they must stay in step.
const moneyLinkColumns = `
	l.parsed_as, l.transaction_id, l.bill_id,
	CASE WHEN t.id IS NOT NULL THEN (t.withdrawal_amt + t.deposit_amt) ELSE b.total_due END,
	COALESCE(b.due_date, ''), COALESCE(b.issuer, '')`

const moneyLinkJoins = `
	LEFT JOIN message_links l ON l.message_id = m.id
	LEFT JOIN transactions t ON t.id = l.transaction_id
	LEFT JOIN bills b ON b.id = l.bill_id`

type moneyLinkScan struct {
	parsedAs sql.NullString
	txnID    sql.NullInt64
	billID   sql.NullInt64
	amount   sql.NullFloat64
	dueDate  string
	issuer   string
}

// Pointer receiver is load-bearing: a value receiver would hand Scan pointers
// into a copy, and every link would silently come back nil.
func (s *moneyLinkScan) dest() []any {
	return []any{&s.parsedAs, &s.txnID, &s.billID, &s.amount, &s.dueDate, &s.issuer}
}

func (s *moneyLinkScan) build() *moneyLink {
	if !s.parsedAs.Valid {
		return nil
	}
	link := &moneyLink{ParsedAs: s.parsedAs.String, DueDate: s.dueDate, Issuer: s.issuer}
	if s.txnID.Valid {
		link.TransactionID = &s.txnID.Int64
	}
	if s.billID.Valid {
		link.BillID = &s.billID.Int64
	}
	if s.amount.Valid {
		link.Amount = &s.amount.Float64
	}
	return link
}

// handleListMessages serves the inbox from the local index rather than going
// to IMAP, so listing is fast and can carry stored classifications.
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
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
			httpx.WriteError(w, http.StatusBadRequest, "unknown view: "+view)
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
		       c.requires_response, c.confidence, c.summary,`+moneyLinkColumns+`
		FROM messages m
		LEFT JOIN classifications c ON c.message_id = m.id`+moneyLinkJoins+`
		WHERE `+where+`
		ORDER BY m.date DESC
		LIMIT ? OFFSET ?
	`, args...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list messages")
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
			link             moneyLinkScan
		)
		dest := []any{
			&uid, &m.Subject, &fromName, &fromEmail, &m.Date, &seen,
			&flagged, &m.Snippet, &m.Size,
			&category, &subcategory, &senderGroup, &priority, &action,
			&requiresResponse, &confidence, &summary,
		}
		if err := rows.Scan(append(dest, link.dest()...)...); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "failed to read message")
			return
		}
		m.MoneyLink = link.build()

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

	httpx.WriteJSON(w, http.StatusOK, messages)
}

// handleSync kicks off a background sync. A full mailbox sync takes far longer
// than any proxy timeout, so it runs detached and the client polls for status.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
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

	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"status":  s.jobs.State(key),
	})
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
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
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	aiProvider, err := s.buildAIProvider()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load AI provider")
		return
	}
	if _, isNoop := aiProvider.(ai.NoopProvider); isNoop {
		httpx.WriteError(w, http.StatusUnprocessableEntity, ai.ErrNotConfigured.Error())
		return
	}

	var model string
	_ = s.DB.QueryRow(`SELECT model_name FROM settings WHERE id = 1`).Scan(&model)

	key := fmt.Sprintf("classify:%d", id)
	started := s.jobs.Start(key, func(ctx context.Context) (any, error) {
		classified, err := classifier.Run(ctx, s.DB, aiProvider, model, id)
		return map[string]int{"classified": classified}, err
	})

	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"status":  s.jobs.State(key),
	})
}

func (s *Server) handleClassifyStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	status, err := classifier.CurrentStatus(s.DB, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to read status")
		return
	}

	state := s.jobs.State(fmt.Sprintf("classify:%d", id))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
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
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")
	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
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
		httpx.WriteError(w, http.StatusBadGateway, "failed to fetch message: "+err.Error())
		return
	}

	// GetMessage peeks, so opening a message is what marks it read — done
	// explicitly here precisely so the local index can be updated in the same
	// breath instead of drifting until the next sync.
	if !msg.Seen {
		if err := provider.SetFlag(r.Context(), folder, msgID, `\Seen`, true); err != nil {
			log.Printf("open: marking account %d uid %s seen failed: %v", id, msgID, err)
		} else {
			msg.Seen = true
			if _, err := s.DB.Exec(
				`UPDATE messages SET seen = 1 WHERE account_id = ? AND folder = ? AND uid = ?`,
				id, folder, msgID,
			); err != nil {
				log.Printf("open: local index seen update failed for account %d uid %s: %v", id, msgID, err)
			}
		}
	}

	// The body comes live from IMAP, but whether Money made anything of this
	// message is local knowledge — join it on before responding.
	httpx.WriteJSON(w, http.StatusOK, struct {
		*mail.Message
		MoneyLink *moneyLink `json:"moneyLink,omitempty"`
	}{msg, s.moneyLinkFor(id, folder, msgID)})
}

// inlineDisplayTypes are the content types a message part may be rendered with
// directly in the browser. Everything else is forced to download: an HTML or
// SVG attachment served inline would execute as a document, and although the
// body iframe is sandboxed, this endpoint is reachable as a top-level URL where
// no sandbox applies.
var inlineDisplayTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/bmp": true, "image/x-icon": true,
	"image/avif": true, "application/pdf": true, "text/plain": true,
}

// handleGetMessagePart streams one MIME part: an attachment download, or the
// inline image an HTML body references with cid:. Addressed by part number or
// by bare Content-ID, so the client can rewrite cid: URLs without a lookup.
func (s *Server) handleGetMessagePart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")
	partID := r.PathValue("partId")
	if partID == "" {
		httpx.WriteError(w, http.StatusBadRequest, "part id is required")
		return
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "INBOX"
	}

	var part *mail.PartContent
	err = s.WithProvider(r.Context(), id, func(provider mail.Provider) error {
		var fetchErr error
		part, fetchErr = provider.FetchPart(r.Context(), folder, msgID, partID)
		return fetchErr
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "failed to fetch attachment: "+err.Error())
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(part.ContentType))
	disposition := "attachment"
	if inlineDisplayTypes[contentType] && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	} else {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(part.Content)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{
		"filename": part.FileName,
	}))
	// A part is immutable for a given (mailbox, uid, part) triple, and it is
	// private mail — cache it hard, but only in the user's own browser.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if _, err := w.Write(part.Content); err != nil {
		log.Printf("part: writing account %d uid %s part %s failed: %v", id, msgID, partID, err)
	}
}

// moneyLinkFor looks up what Money made of one message, keyed the way the mail
// API addresses messages: by mailbox, folder and IMAP UID.
func (s *Server) moneyLinkFor(accountID int64, folder, uid string) *moneyLink {
	var link moneyLinkScan
	row := s.DB.QueryRow(`
		SELECT `+moneyLinkColumns+`
		FROM messages m`+moneyLinkJoins+`
		WHERE m.account_id = ? AND m.folder = ? AND m.uid = ?`,
		accountID, folder, uid)
	if err := row.Scan(link.dest()...); err != nil {
		return nil // not indexed yet, or Money hasn't looked at it
	}
	return link.build()
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")
	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
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
		httpx.WriteError(w, http.StatusBadGateway, "failed to delete message: "+err.Error())
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

	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type setFlagRequest struct {
	Flag  string `json:"flag"`
	Value bool   `json:"value"`
}

func (s *Server) handleSetFlag(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")

	var req setFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Flag == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
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
		httpx.WriteError(w, http.StatusBadGateway, "failed to set flag: "+err.Error())
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

	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type bulkMessagesResponse struct {
	Updated int      `json:"updated"`
	Failed  []string `json:"failed,omitempty"`
}

type bulkDeleteMessagesRequest struct {
	Folder string   `json:"folder"`
	IDs    []string `json:"ids"`
}

// handleBulkDeleteMessages deletes many messages in one request. It borrows a
// single provider connection via WithProvider and loops DeleteMessage over
// it, rather than opening one IMAP session per message.
func (s *Server) handleBulkDeleteMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	var req bulkDeleteMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "ids is required and must be non-empty")
		return
	}
	folder := req.Folder
	if folder == "" {
		folder = "INBOX"
	}

	var deleted []string
	err = s.WithProvider(r.Context(), id, func(provider mail.Provider) error {
		var deleteErr error
		deleted, deleteErr = provider.DeleteMessages(r.Context(), folder, req.IDs)
		return deleteErr
	})
	if len(deleted) > 0 {
		s.forgetMessages(id, folder, deleted)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not delete messages: "+err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, bulkMessagesResponse{
		Updated: len(deleted),
		Failed:  missing(req.IDs, deleted),
	})
}

type bulkSetFlagRequest struct {
	Folder string   `json:"folder"`
	IDs    []string `json:"ids"`
	Flag   string   `json:"flag"`
	Value  bool     `json:"value"`
}

// handleBulkSetFlag adds or removes one IMAP flag across many messages,
// looping DeleteMessage-style over a single borrowed provider connection.
func (s *Server) handleBulkSetFlag(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	var req bulkSetFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 || req.Flag == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	folder := req.Folder
	if folder == "" {
		folder = "INBOX"
	}

	var updated []string
	err = s.WithProvider(r.Context(), id, func(provider mail.Provider) error {
		var flagErr error
		updated, flagErr = provider.SetFlags(r.Context(), folder, req.IDs, req.Flag, req.Value)
		return flagErr
	})
	s.mirrorFlag(id, folder, updated, req.Flag, req.Value)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not update messages: "+err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, bulkMessagesResponse{
		Updated: len(updated),
		Failed:  missing(req.IDs, updated),
	})
}

// mirrorFlag reflects an IMAP flag change into the local index so the list
// stays correct between syncs. Chunked for the same reason forgetMessages is.
func (s *Server) mirrorFlag(accountID int64, folder string, uids []string, flag string, value bool) {
	var column string
	switch flag {
	case `\Seen`:
		column = "seen"
	case `\Flagged`:
		column = "flagged"
	default:
		return // a flag the index doesn't track
	}
	if len(uids) == 0 {
		return
	}

	stored := 0
	if value {
		stored = 1
	}
	const chunk = 400
	for start := 0; start < len(uids); start += chunk {
		batch := uids[start:min(start+chunk, len(uids))]
		query := `UPDATE messages SET ` + column + ` = ? WHERE account_id = ? AND folder = ? AND uid IN (` +
			placeholders(len(batch)) + `)`
		args := append([]any{stored}, inArgs(accountID, folder, batch)...)
		if _, err := s.DB.Exec(query, args...); err != nil {
			log.Printf("flag mirror failed for account %d: %v", accountID, err)
			return
		}
	}
}

// placeholders returns "?,?,...", n copies, for a SQL IN clause.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// inArgs builds the (accountID, folder, uid...) argument list matching an
// "account_id = ? AND folder = ? AND uid IN (...)" clause built with placeholders.
func inArgs(accountID int64, folder string, uids []string) []any {
	args := make([]any, 0, len(uids)+2)
	args = append(args, accountID, folder)
	for _, uid := range uids {
		args = append(args, uid)
	}
	return args
}

func (s *Server) handleSummarize(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	msgID := r.PathValue("msgId")

	creds, err := s.loadAccountCredentials(id)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "account not found")
		return
	}
	provider, err := s.providerFactory(creds)
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not connect to mail server: "+err.Error())
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
		httpx.WriteError(w, http.StatusBadGateway, "failed to fetch message: "+err.Error())
		return
	}

	aiProvider, err := s.buildAIProvider()
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to load AI provider")
		return
	}

	bodyPreview := msg.BodyText
	if bodyPreview == "" {
		bodyPreview = msg.BodyHTML
	}

	summary, err := aiProvider.Summarize(r.Context(), msg.Subject, bodyPreview)
	if err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"summary": summary})
}
