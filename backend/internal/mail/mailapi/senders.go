package mailapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/platform/httpx"
)

// sendersFolder is the folder the Senders view and its bulk/unsubscribe
// actions operate on. Mirrors the rest of Mail, which is Inbox-only today.
const sendersFolder = "INBOX"

type senderEmailRequest struct {
	Email string `json:"email"`
}

// handleUnsubscribeSender attempts a real unsubscribe using the List-Unsubscribe
// header (RFC 2369/8058) of the sender's most recent message. An https:// URI
// is requested directly (POSTed one-click style if the message advertises
// List-Unsubscribe-Post). A mailto:-only header can't be acted on here — this
// app has no outbound mail sending — so the URI is handed back for the client
// to open in the user's own mail app instead.
func (s *Server) handleUnsubscribeSender(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	var req senderEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		httpx.WriteError(w, http.StatusBadRequest, "email is required")
		return
	}

	var uid string
	err = s.DB.QueryRow(`
		SELECT uid FROM messages
		WHERE account_id = ? AND folder = ? AND from_email = ?
		ORDER BY date DESC LIMIT 1`, id, sendersFolder, req.Email,
	).Scan(&uid)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "no messages found for this sender")
		return
	}

	var info *mail.UnsubscribeInfo
	err = s.WithProvider(r.Context(), id, func(provider mail.Provider) error {
		raw, err := provider.FetchHeaders(r.Context(), sendersFolder, uid)
		if err != nil {
			return err
		}
		info = mail.ParseUnsubscribeHeaders(raw)
		return nil
	})
	if err != nil {
		httpx.WriteError(w, http.StatusBadGateway, "could not read message headers: "+err.Error())
		return
	}
	if info == nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "this sender has no List-Unsubscribe header")
		return
	}

	if info.HTTPURL != "" {
		if err := requestUnsubscribe(r.Context(), info.HTTPURL, info.OneClick); err != nil {
			httpx.WriteError(w, http.StatusBadGateway, "unsubscribe request failed: "+err.Error())
			return
		}
		s.setSenderStatus(id, req.Email, "http")
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed", "method": "http"})
		return
	}

	// Only a mailto: option was offered. We can't send it ourselves, so this
	// is not marked unsubscribed yet — the client opens the mailto: link and
	// the user confirms via mark-unsubscribed once they've actually sent it.
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "manual", "method": "mailto", "mailto": info.Mailto})
}

func (s *Server) handleMarkSenderUnsubscribed(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	var req senderEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		httpx.WriteError(w, http.StatusBadRequest, "email is required")
		return
	}
	s.setSenderStatus(id, req.Email, "manual")
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed", "method": "manual"})
}

// handleResubscribeSender just clears our local unsubscribed flag — there is
// no remote mailing list state to restore, since unsubscribing never involved
// a Wollow account or login on the sender's end.
func (s *Server) handleResubscribeSender(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	var req senderEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		httpx.WriteError(w, http.StatusBadRequest, "email is required")
		return
	}
	if _, err := s.DB.Exec(`DELETE FROM sender_status WHERE account_id = ? AND from_email = ?`, id, req.Email); err != nil {
		log.Printf("sender status: failed to clear %s for account %d: %v", req.Email, id, err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setSenderStatus(accountID int64, email, method string) {
	if _, err := s.DB.Exec(`
		INSERT INTO sender_status (account_id, from_email, unsubscribed_at, unsubscribe_method)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(account_id, from_email) DO UPDATE SET
			unsubscribed_at = excluded.unsubscribed_at,
			unsubscribe_method = excluded.unsubscribe_method`,
		accountID, email, time.Now().UTC().Format(time.RFC3339), method,
	); err != nil {
		log.Printf("sender status: failed to record %s for account %d: %v", email, accountID, err)
	}
}

var unsubscribeHTTPClient = &http.Client{Timeout: 10 * time.Second}

// requestUnsubscribe hits a List-Unsubscribe https:// URI: a plain GET for
// the common case, or a one-click POST (RFC 8058) when the message
// advertised List-Unsubscribe-Post.
func requestUnsubscribe(ctx context.Context, url string, oneClick bool) error {
	method := http.MethodGet
	var body io.Reader
	if oneClick {
		method = http.MethodPost
		body = strings.NewReader("List-Unsubscribe=One-Click")
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if oneClick {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := unsubscribeHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return nil
}

type bulkSenderRequest struct {
	Emails []string `json:"emails"`
}

// Sender-level bulk actions run detached.
//
// Unlike the inbox's own bulk actions — bounded by what fits on a page — these
// address *every* message from a sender, which in a real mailbox is thousands.
// Even batched onto a handful of IMAP commands that is too long to hold an HTTP
// request open for: it has to wait its turn for the mailbox lock behind a
// background sync, and the socket dies before the work starts. So the request
// starts a job and returns; the client polls for progress.

// bulkJobKey is one job per mailbox. All three actions share it because they
// all contend for the same IMAP session anyway, so two at once is not a thing
// worth supporting.
func bulkJobKey(accountID int64) string {
	return fmt.Sprintf("senders-bulk:%d", accountID)
}

// bulkActionChunk is how many messages are handed to one provider call. The
// provider batches internally; this smaller number exists so progress advances
// visibly and so a job killed midway leaves the index agreeing with the server.
const bulkActionChunk = 200

// bulkJobResult is what a finished job reports back through JobState.Detail.
type bulkJobResult struct {
	Action  string   `json:"action"`
	Total   int      `json:"total"`
	Updated int      `json:"updated"`
	Failed  []string `json:"failed,omitempty"`
}

// startBulkSenderJob resolves the senders' messages and runs action over them
// in the background, reporting progress as it goes.
func (s *Server) startBulkSenderJob(
	w http.ResponseWriter,
	accountID int64,
	emails []string,
	label string,
	action func(ctx context.Context, provider mail.Provider, chunk []string) ([]string, error),
	afterChunk func(done []string),
) {
	uids, err := s.senderMessageUIDs(accountID, sendersFolder, emails)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to look up sender messages")
		return
	}

	key := bulkJobKey(accountID)
	if len(uids) == 0 {
		// Nothing to do, but the client still expects a job to poll.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"started": false,
			"status": JobState{
				FinishedAt: time.Now().UTC().Format(time.RFC3339),
				Detail:     bulkJobResult{Action: label},
			},
		})
		return
	}

	started := s.jobs.Start(key, func(ctx context.Context) (any, error) {
		result := bulkJobResult{Action: label, Total: len(uids)}
		s.jobs.Report(key, 0, len(uids), "messages")

		// One connection for the whole job. The context is the job's own, not
		// the request's, so a client that navigates away doesn't abort it.
		err := s.WithProvider(ctx, accountID, func(provider mail.Provider) error {
			for start := 0; start < len(uids); start += bulkActionChunk {
				chunk := uids[start:min(start+bulkActionChunk, len(uids))]
				done, err := action(ctx, provider, chunk)
				if len(done) > 0 {
					afterChunk(done)
					result.Updated += len(done)
				}
				result.Failed = append(result.Failed, missing(chunk, done)...)
				s.jobs.Report(key, start+len(chunk), len(uids), "messages")
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			// Whatever finished before the failure is already applied and
			// counted; the error explains the remainder.
			return result, err
		}
		if len(result.Failed) > 0 {
			log.Printf("%s: account %d left %d of %d messages unprocessed",
				label, accountID, len(result.Failed), len(uids))
		}
		return result, nil
	})

	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"started": started,
		"total":   len(uids),
		"status":  s.jobs.State(key),
	})
}

// handleBulkSenderStatus reports the running bulk action for a mailbox.
func (s *Server) handleBulkSenderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s.jobs.State(bulkJobKey(id)))
}

// senderMessageUIDs looks up every local uid for the given senders — the
// basis for every sender-level bulk action, so "archive this sender" reaches
// every message from them, not just the ones currently loaded in the UI.
func (s *Server) senderMessageUIDs(accountID int64, folder string, emails []string) ([]string, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	placeholdersList := make([]string, len(emails))
	args := make([]any, 0, len(emails)+2)
	args = append(args, accountID, folder)
	for i, e := range emails {
		placeholdersList[i] = "?"
		args = append(args, e)
	}
	rows, err := s.DB.Query(
		`SELECT uid FROM messages WHERE account_id = ? AND folder = ? AND from_email IN (`+strings.Join(placeholdersList, ",")+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var uids []string
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err == nil {
			uids = append(uids, strconv.FormatInt(uid, 10))
		}
	}
	return uids, nil
}

type bulkSenderFlagRequest struct {
	Emails []string `json:"emails"`
	Flag   string   `json:"flag"`
	Value  bool     `json:"value"`
}

// handleBulkSenderFlag adds or removes a flag across every message from the
// given senders.
func (s *Server) handleBulkSenderFlag(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	var req bulkSenderFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Emails) == 0 || req.Flag == "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	label := "Updated"
	if req.Flag == `\Seen` {
		label = map[bool]string{true: "Marked read", false: "Marked unread"}[req.Value]
	}
	s.startBulkSenderJob(w, id, req.Emails, label,
		func(ctx context.Context, provider mail.Provider, chunk []string) ([]string, error) {
			return provider.SetFlags(ctx, sendersFolder, chunk, req.Flag, req.Value)
		},
		func(done []string) { s.mirrorFlag(id, sendersFolder, done, req.Flag, req.Value) },
	)
}

// handleBulkDeleteSenders deletes every message from the given senders.
func (s *Server) handleBulkDeleteSenders(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	var req bulkSenderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Emails) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "emails is required and must be non-empty")
		return
	}

	s.startBulkSenderJob(w, id, req.Emails, "Deleted",
		func(ctx context.Context, provider mail.Provider, chunk []string) ([]string, error) {
			return provider.DeleteMessages(ctx, sendersFolder, chunk)
		},
		// Dropped per chunk rather than at the end, so a job interrupted
		// halfway leaves the index agreeing with the server.
		func(done []string) { s.forgetMessages(id, sendersFolder, done) },
	)
}

// handleBulkArchiveSenders moves every message from the given senders into the
// account's Archive folder via real IMAP MOVE (created on first use if the
// server doesn't have one). Archived messages leave the local Inbox index,
// same as a delete, since they genuinely left INBOX on the server.
func (s *Server) handleBulkArchiveSenders(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	var req bulkSenderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Emails) == 0 {
		httpx.WriteError(w, http.StatusBadRequest, "emails is required and must be non-empty")
		return
	}

	// Resolved once per job rather than per chunk: it costs a LIST, and the
	// answer cannot change mid-job.
	var archiveFolder string
	s.startBulkSenderJob(w, id, req.Emails, "Archived",
		func(ctx context.Context, provider mail.Provider, chunk []string) ([]string, error) {
			if archiveFolder == "" {
				resolved, err := provider.ResolveArchiveFolder(ctx)
				if err != nil || resolved == "" {
					resolved = "Archive"
				}
				archiveFolder = resolved
			}
			return provider.MoveMessages(ctx, sendersFolder, chunk, archiveFolder)
		},
		func(done []string) { s.forgetMessages(id, sendersFolder, done) },
	)
}

// forgetMessages drops uids from the local index, in chunks so a delete of
// thousands doesn't build a SQL statement with thousands of placeholders (past
// SQLite's variable limit, the statement fails and the index keeps messages the
// server no longer has).
func (s *Server) forgetMessages(accountID int64, folder string, uids []string) {
	const chunk = 400
	for start := 0; start < len(uids); start += chunk {
		batch := uids[start:min(start+chunk, len(uids))]
		if _, err := s.DB.Exec(
			`DELETE FROM messages WHERE account_id = ? AND folder = ? AND uid IN (`+placeholders(len(batch))+`)`,
			inArgs(accountID, folder, batch)...,
		); err != nil {
			log.Printf("index cleanup failed for account %d: %v", accountID, err)
			return
		}
	}
}

// missing returns the ids in want that aren't in got, i.e. the ones an
// operation didn't manage to act on.
func missing(want, got []string) []string {
	if len(got) == len(want) {
		return nil
	}
	done := make(map[string]struct{}, len(got))
	for _, id := range got {
		done[id] = struct{}{}
	}
	var failed []string
	for _, id := range want {
		if _, ok := done[id]; !ok {
			failed = append(failed, id)
		}
	}
	return failed
}
