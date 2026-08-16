package mailapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"wollow/backend/internal/mail"
)

// bulkStubProvider records every DeleteMessage/SetFlag call it receives and
// fails on demand for specific ids, so a test can assert both what reached
// the "mail server" and that a partial failure doesn't corrupt the rest.
type bulkStubProvider struct {
	failIDs     map[string]bool
	headers     map[string][]byte // id -> raw header block, for FetchHeaders
	deleteCalls []string
	flagCalls   []string // "id:flag:value"
	moveCalls   []string // "id->destFolder"

	// batchSizes records the size of every batched command issued, which is
	// what proves a bulk action costs a constant number of IMAP round trips
	// rather than one per message.
	batchSizes []int
	// beforeBatch, if set, runs before each batch — a gate that lets a test
	// observe a job mid-flight instead of racing one that finishes instantly.
	beforeBatch func()
}

// The batch methods model a real IMAP server: a command over a set either
// succeeds for the whole set or fails for it, with no per-message verdict. The
// provider under test is expected to isolate the bad ones by retrying
// individually, which is what keeps partial failures reportable.
func (p *bulkStubProvider) runBatch(ids []string, apply func(string)) ([]string, error) {
	if p.beforeBatch != nil {
		p.beforeBatch()
	}
	p.batchSizes = append(p.batchSizes, len(ids))
	for _, id := range ids {
		if p.failIDs[id] {
			if len(ids) == 1 {
				return nil, fmt.Errorf("simulated failure for %s", id)
			}
			// Mimic the real fallback: split and retry one at a time.
			var done []string
			for _, single := range ids {
				if got, err := p.runBatch([]string{single}, apply); err == nil {
					done = append(done, got...)
				}
			}
			return done, nil
		}
	}
	for _, id := range ids {
		apply(id)
	}
	return ids, nil
}

func (p *bulkStubProvider) DeleteMessages(_ context.Context, _ string, ids []string) ([]string, error) {
	return p.runBatch(ids, func(id string) { p.deleteCalls = append(p.deleteCalls, id) })
}

func (p *bulkStubProvider) SetFlags(_ context.Context, _ string, ids []string, flag string, value bool) ([]string, error) {
	return p.runBatch(ids, func(id string) {
		p.flagCalls = append(p.flagCalls, fmt.Sprintf("%s:%s:%v", id, flag, value))
	})
}

func (p *bulkStubProvider) MoveMessages(_ context.Context, _ string, ids []string, dest string) ([]string, error) {
	return p.runBatch(ids, func(id string) {
		p.moveCalls = append(p.moveCalls, fmt.Sprintf("%s->%s", id, dest))
	})
}

func (p *bulkStubProvider) ListMessages(context.Context, string, int, int) ([]mail.MessageSummary, error) {
	return nil, nil
}
func (p *bulkStubProvider) GetMessage(context.Context, string, string) (*mail.Message, error) {
	return nil, nil
}
func (p *bulkStubProvider) DeleteMessage(_ context.Context, _ string, id string) error {
	if p.failIDs[id] {
		return fmt.Errorf("simulated failure for %s", id)
	}
	p.deleteCalls = append(p.deleteCalls, id)
	return nil
}
func (p *bulkStubProvider) SetFlag(_ context.Context, _ string, id, flag string, value bool) error {
	if p.failIDs[id] {
		return fmt.Errorf("simulated failure for %s", id)
	}
	p.flagCalls = append(p.flagCalls, fmt.Sprintf("%s:%s:%v", id, flag, value))
	return nil
}
func (p *bulkStubProvider) MoveMessage(_ context.Context, _ string, id, dest string) error {
	if p.failIDs[id] {
		return fmt.Errorf("simulated failure for %s", id)
	}
	p.moveCalls = append(p.moveCalls, fmt.Sprintf("%s->%s", id, dest))
	return nil
}
func (p *bulkStubProvider) ListUIDs(context.Context, string) ([]uint32, error) { return nil, nil }
func (p *bulkStubProvider) FetchForSync(context.Context, string, []uint32) ([]mail.SyncMessage, error) {
	return nil, nil
}
func (p *bulkStubProvider) FetchRaw(context.Context, string, []uint32) ([]mail.RawMessage, error) {
	return nil, nil
}
func (p *bulkStubProvider) FetchHeaders(_ context.Context, _ string, id string) ([]byte, error) {
	return p.headers[id], nil
}
func (p *bulkStubProvider) FetchPart(context.Context, string, string, string) (*mail.PartContent, error) {
	return nil, nil
}
func (p *bulkStubProvider) ResolveTrashFolder(context.Context) (string, error) {
	return "Trash", nil
}
func (p *bulkStubProvider) ResolveArchiveFolder(context.Context) (string, error) {
	return "Archive", nil
}

func seedMessage(t *testing.T, server *Server, accountID int64, uid int, seen, flagged bool) {
	t.Helper()
	_, err := server.DB.Exec(`
		INSERT INTO messages (account_id, folder, uid, subject, seen, flagged)
		VALUES (?, 'INBOX', ?, ?, ?, ?)`,
		accountID, uid, fmt.Sprintf("msg %d", uid), boolToInt(seen), boolToInt(flagged))
	if err != nil {
		t.Fatalf("seed message %d: %v", uid, err)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func messageSeenFlagged(t *testing.T, server *Server, accountID int64, uid int) (exists, seen, flagged bool) {
	t.Helper()
	var seenInt, flaggedInt int
	err := server.DB.QueryRow(
		`SELECT seen, flagged FROM messages WHERE account_id = ? AND folder = 'INBOX' AND uid = ?`,
		accountID, uid,
	).Scan(&seenInt, &flaggedInt)
	if err != nil {
		return false, false, false
	}
	return true, seenInt != 0, flaggedInt != 0
}

// TestBulkDeleteMessages checks that a mixed-outcome bulk delete only removes
// the messages IMAP actually deleted from the local index, and reports the
// rest as failed rather than silently dropping them.
func TestBulkDeleteMessages(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{failIDs: map[string]bool{"2": true}}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	for _, uid := range []int{1, 2, 3} {
		seedMessage(t, server, accountID, uid, false, false)
	}

	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]any{"folder": "INBOX", "ids": []string{"1", "2", "3"}})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/messages/bulk-delete", accountID), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp bulkMessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Updated != 2 || len(resp.Failed) != 1 || resp.Failed[0] != "2" {
		t.Fatalf("response = %+v, want Updated=2 Failed=[2]", resp)
	}

	if exists, _, _ := messageSeenFlagged(t, server, accountID, 1); exists {
		t.Error("message 1 should have been removed from the local index")
	}
	if exists, _, _ := messageSeenFlagged(t, server, accountID, 3); exists {
		t.Error("message 3 should have been removed from the local index")
	}
	if exists, _, _ := messageSeenFlagged(t, server, accountID, 2); !exists {
		t.Error("message 2 failed to delete on IMAP and should still be in the local index")
	}
}

// The bug this guards: "Delete all" on a sender used to issue a STORE and an
// EXPUNGE per message. At 1,348 messages — a real count from a real mailbox —
// that is ~2,700 sequential IMAP round trips inside one HTTP request, which
// outlives every proxy and browser timeout. The action appeared to do nothing.
//
// IMAP commands take a set of UIDs, so the whole thing is a constant number of
// commands. This asserts the count does not scale with the message count.
func TestBulkDeleteIssuesABoundedNumberOfCommands(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	const messages = 1348
	ids := make([]string, 0, messages)
	for uid := 1; uid <= messages; uid++ {
		seedMessage(t, server, accountID, uid, false, false)
		ids = append(ids, strconv.Itoa(uid))
	}

	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]any{"folder": "INBOX", "ids": ids})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/messages/bulk-delete", accountID), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp bulkMessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Updated != messages {
		t.Errorf("deleted %d of %d messages", resp.Updated, messages)
	}

	// A handful of chunked commands, not one per message.
	if len(provider.batchSizes) > 8 {
		t.Errorf("issued %d IMAP commands for %d messages — the bulk path is looping per message",
			len(provider.batchSizes), messages)
	}

	// And the whole lot must leave the local index, which is where a naive
	// single-statement DELETE would blow past SQLite's variable limit.
	var remaining int
	if err := server.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE account_id = ?`, accountID,
	).Scan(&remaining); err != nil {
		t.Fatalf("counting index: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d messages left in the index after deleting all of them", remaining)
	}
}

// The same bound applies to the sender-level action, which is the one the
// Senders page actually calls — and which now runs as a detached job.
func TestBulkDeleteSendersIsBounded(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	const messages = 900
	for uid := 1; uid <= messages; uid++ {
		seedMessageFrom(t, server, accountID, uid, "alerts@hdfcbank.net")
	}

	mux := http.NewServeMux()
	server.Register(mux)
	startBulkAction(t, mux, accountID, "bulk-delete", map[string]any{"emails": []string{"alerts@hdfcbank.net"}})

	state := waitForBulkJob(t, mux, accountID)
	if state.Error != "" {
		t.Fatalf("job failed: %s", state.Error)
	}

	detail, _ := json.Marshal(state.Detail)
	var result bulkJobResult
	json.Unmarshal(detail, &result)
	if result.Updated != messages {
		t.Errorf("deleted %d of %d messages from the sender", result.Updated, messages)
	}

	// The provider batches; the job chunks. Both together must stay far below
	// one command per message.
	if len(provider.batchSizes) > 12 {
		t.Errorf("issued %d IMAP commands for %d messages", len(provider.batchSizes), messages)
	}

	var remaining int
	server.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE account_id = ?`, accountID).Scan(&remaining)
	if remaining != 0 {
		t.Errorf("%d of the sender's messages are still in the index", remaining)
	}
}

// TestBulkSetFlag checks that only successfully-flagged messages get mirrored
// into the local index, keeping the list consistent with what IMAP actually did.
func TestBulkSetFlag(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{failIDs: map[string]bool{"2": true}}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	for _, uid := range []int{1, 2, 3} {
		seedMessage(t, server, accountID, uid, false, false)
	}

	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]any{
		"folder": "INBOX", "ids": []string{"1", "2", "3"}, "flag": `\Seen`, "value": true,
	})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/messages/bulk-flag", accountID), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp bulkMessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Updated != 2 || len(resp.Failed) != 1 || resp.Failed[0] != "2" {
		t.Fatalf("response = %+v, want Updated=2 Failed=[2]", resp)
	}

	if _, seen, _ := messageSeenFlagged(t, server, accountID, 1); !seen {
		t.Error("message 1 should be marked seen locally")
	}
	if _, seen, _ := messageSeenFlagged(t, server, accountID, 3); !seen {
		t.Error("message 3 should be marked seen locally")
	}
	if _, seen, _ := messageSeenFlagged(t, server, accountID, 2); seen {
		t.Error("message 2 failed to flag on IMAP and should not be marked seen locally")
	}
}
