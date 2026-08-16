package mailapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wollow/backend/internal/mail"
)

// waitForBulkJob polls the bulk-status endpoint until the mailbox's job stops
// running, and returns its final state.
//
// Sender-level bulk actions are detached, so a test that doesn't wait races the
// job against its own database teardown.
func waitForBulkJob(t *testing.T, mux *http.ServeMux, accountID int64) JobState {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/mail/accounts/%d/senders/bulk-status", accountID), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("bulk-status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var state JobState
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatalf("decode bulk status: %v", err)
		}
		if !state.Running {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("bulk job did not finish: %+v", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startBulkAction posts one of the sender bulk endpoints and asserts it was
// accepted rather than executed inline.
func startBulkAction(t *testing.T, mux *http.ServeMux, accountID int64, action string, body any) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, jsonRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/%s", accountID, action), encoded))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("%s status = %d, body = %s — the action should be accepted, not run inline",
			action, rec.Code, rec.Body.String())
	}
}

// The whole point of detaching: the request returns immediately instead of
// holding a socket open while thousands of messages are processed. That socket
// is what the dev proxy reported as "socket hang up".
func TestBulkSenderActionReturnsImmediately(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	for uid := 1; uid <= 500; uid++ {
		seedMessageFrom(t, server, accountID, uid, "noisy@example.com")
	}

	mux := http.NewServeMux()
	server.Register(mux)

	start := time.Now()
	startBulkAction(t, mux, accountID, "bulk-delete", map[string]any{"emails": []string{"noisy@example.com"}})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the request took %s — it is still doing the work inline", elapsed)
	}

	state := waitForBulkJob(t, mux, accountID)
	if state.Error != "" {
		t.Fatalf("job failed: %s", state.Error)
	}

	var remaining int
	server.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE account_id = ?`, accountID).Scan(&remaining)
	if remaining != 0 {
		t.Errorf("%d messages left in the index after the job finished", remaining)
	}
}

// A bulk action has to report how far along it is, or a long delete is an
// indefinite spinner and the user cannot tell it from a hang.
func TestBulkSenderJobReportsProgress(t *testing.T) {
	server, _, accountID := newTestServer(t)
	// Gate the provider so progress can be observed mid-flight rather than
	// raced against a job that finishes instantly.
	release := make(chan struct{})
	provider := &bulkStubProvider{beforeBatch: func() { <-release }}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	const messages = 600 // three chunks at bulkActionChunk = 200
	for uid := 1; uid <= messages; uid++ {
		seedMessageFrom(t, server, accountID, uid, "noisy@example.com")
	}

	mux := http.NewServeMux()
	server.Register(mux)
	startBulkAction(t, mux, accountID, "bulk-delete", map[string]any{"emails": []string{"noisy@example.com"}})

	// Let the first chunk through and look for partial progress.
	release <- struct{}{}
	var sawPartial bool
	for attempt := 0; attempt < 200 && !sawPartial; attempt++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/mail/accounts/%d/senders/bulk-status", accountID), nil))
		var state JobState
		json.Unmarshal(rec.Body.Bytes(), &state)
		if state.Progress != nil {
			if state.Progress.Total != messages {
				t.Fatalf("progress total = %d, want %d", state.Progress.Total, messages)
			}
			if state.Progress.Label != "messages" {
				t.Errorf("progress label = %q, want %q", state.Progress.Label, "messages")
			}
			if state.Progress.Done > 0 && state.Progress.Done < messages {
				sawPartial = true
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !sawPartial {
		t.Error("never observed partial progress — the client has nothing to render")
	}

	close(release) // let the remaining chunks run
	state := waitForBulkJob(t, mux, accountID)

	if state.Progress == nil || state.Progress.Done != messages {
		t.Errorf("final progress = %+v, want %d done", state.Progress, messages)
	}

	// The finished job has to say what it achieved.
	detail, _ := json.Marshal(state.Detail)
	var result bulkJobResult
	json.Unmarshal(detail, &result)
	if result.Action != "Deleted" {
		t.Errorf("action = %q, want %q", result.Action, "Deleted")
	}
	if result.Updated != messages || result.Total != messages {
		t.Errorf("result = %+v, want %d of %d", result, messages, messages)
	}
}

// Selecting senders with no indexed messages must not leave the client polling
// a job that will never exist.
func TestBulkSenderActionWithNoMessagesCompletesImmediately(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	mux := http.NewServeMux()
	server.Register(mux)

	encoded, _ := json.Marshal(map[string]any{"emails": []string{"nobody@example.com"}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, jsonRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/bulk-delete", accountID), encoded))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Started bool     `json:"started"`
		Status  JobState `json:"status"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Started {
		t.Error("started a job for zero messages")
	}
	if resp.Status.Running {
		t.Error("reported a running job for zero messages, so the client would poll forever")
	}
}

// A second action while one is in flight must not start a competing job — both
// would fight over the same IMAP session.
func TestBulkSenderJobRefusesToDoubleStart(t *testing.T) {
	server, _, accountID := newTestServer(t)
	release := make(chan struct{})
	provider := &bulkStubProvider{beforeBatch: func() { <-release }}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	for uid := 1; uid <= 10; uid++ {
		seedMessageFrom(t, server, accountID, uid, "noisy@example.com")
	}
	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]any{"emails": []string{"noisy@example.com"}})
	first := httptest.NewRecorder()
	mux.ServeHTTP(first, jsonRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/bulk-delete", accountID), body))

	second := httptest.NewRecorder()
	mux.ServeHTTP(second, jsonRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/bulk-delete", accountID), body))

	var resp struct {
		Started bool `json:"started"`
	}
	json.Unmarshal(second.Body.Bytes(), &resp)
	if resp.Started {
		t.Error("a second bulk action started while one was already running")
	}

	close(release)
	waitForBulkJob(t, mux, accountID)
}
