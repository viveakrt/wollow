package mailapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"wollow/backend/internal/mail"
)

func seedMessageFrom(t *testing.T, server *Server, accountID int64, uid int, fromEmail string) {
	t.Helper()
	_, err := server.DB.Exec(`
		INSERT INTO messages (account_id, folder, uid, subject, from_email)
		VALUES (?, 'INBOX', ?, ?, ?)`,
		accountID, uid, fmt.Sprintf("msg %d", uid), fromEmail)
	if err != nil {
		t.Fatalf("seed message %d: %v", uid, err)
	}
}

func senderStatus(t *testing.T, server *Server, accountID int64, email string) (exists bool, method string) {
	t.Helper()
	err := server.DB.QueryRow(
		`SELECT unsubscribe_method FROM sender_status WHERE account_id = ? AND from_email = ?`,
		accountID, email,
	).Scan(&method)
	if err != nil {
		return false, ""
	}
	return true, method
}

// TestUnsubscribeSenderHTTP checks the one-click path: a List-Unsubscribe
// header with an https URI and List-Unsubscribe-Post gets POSTed to, and the
// sender is recorded as unsubscribed via "http" only once that succeeds.
func TestUnsubscribeSenderHTTP(t *testing.T) {
	var gotMethod, gotBody string
	unsubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer unsubServer.Close()

	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{
		headers: map[string][]byte{
			"1": []byte("Subject: promo\r\nList-Unsubscribe: <" + unsubServer.URL + ">\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"),
		},
	}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }
	seedMessageFrom(t, server, accountID, 1, "promo@example.com")

	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]string{"email": "promo@example.com"})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/unsubscribe", accountID), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "unsubscribed" || resp["method"] != "http" {
		t.Fatalf("response = %+v, want status=unsubscribed method=http", resp)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("unsubscribe server got method %q, want POST (one-click)", gotMethod)
	}
	if gotBody != "List-Unsubscribe=One-Click" {
		t.Errorf("unsubscribe server got body %q", gotBody)
	}

	exists, method := senderStatus(t, server, accountID, "promo@example.com")
	if !exists || method != "http" {
		t.Errorf("sender_status = (exists=%v, method=%q), want (true, http)", exists, method)
	}
}

// TestUnsubscribeSenderMailtoOnly checks that a mailto:-only header is handed
// back to the client rather than silently marked done, since this app can't
// actually send the email itself.
func TestUnsubscribeSenderMailtoOnly(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{
		headers: map[string][]byte{
			"1": []byte("Subject: promo\r\nList-Unsubscribe: <mailto:unsub@example.com>\r\n"),
		},
	}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }
	seedMessageFrom(t, server, accountID, 1, "promo@example.com")

	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]string{"email": "promo@example.com"})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/unsubscribe", accountID), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "manual" || resp["method"] != "mailto" || resp["mailto"] != "mailto:unsub@example.com" {
		t.Fatalf("response = %+v", resp)
	}

	if exists, _ := senderStatus(t, server, accountID, "promo@example.com"); exists {
		t.Error("sender should not be marked unsubscribed until the mailto is confirmed")
	}
}

// TestMarkUnsubscribedAndResubscribe checks the manual override round-trips.
func TestMarkUnsubscribedAndResubscribe(t *testing.T) {
	server, _, accountID := newTestServer(t)
	mux := http.NewServeMux()
	server.Register(mux)

	body, _ := json.Marshal(map[string]string{"email": "promo@example.com"})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/mark-unsubscribed", accountID), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark-unsubscribed status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if exists, method := senderStatus(t, server, accountID, "promo@example.com"); !exists || method != "manual" {
		t.Fatalf("sender_status = (exists=%v, method=%q), want (true, manual)", exists, method)
	}

	req = httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/mail/accounts/%d/senders/resubscribe", accountID), bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resubscribe status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if exists, _ := senderStatus(t, server, accountID, "promo@example.com"); exists {
		t.Error("resubscribe should have cleared the sender_status row")
	}
}

// jsonRequest builds a POST with a JSON body.
func jsonRequest(method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestBulkArchiveSenders checks that archiving targets every message from the
// selected senders (found via the local index, not just what the client sent)
// and leaves messages from other senders untouched.
func TestBulkArchiveSenders(t *testing.T) {
	server, _, accountID := newTestServer(t)
	provider := &bulkStubProvider{}
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	seedMessageFrom(t, server, accountID, 1, "promo@example.com")
	seedMessageFrom(t, server, accountID, 2, "promo@example.com")
	seedMessageFrom(t, server, accountID, 3, "friend@example.com")

	mux := http.NewServeMux()
	server.Register(mux)

	startBulkAction(t, mux, accountID, "bulk-archive", map[string]any{"emails": []string{"promo@example.com"}})
	state := waitForBulkJob(t, mux, accountID)
	if state.Error != "" {
		t.Fatalf("job failed: %s", state.Error)
	}
	detail, _ := json.Marshal(state.Detail)
	var result bulkJobResult
	json.Unmarshal(detail, &result)
	if result.Updated != 2 {
		t.Fatalf("result = %+v, want Updated=2", result)
	}
	if len(provider.moveCalls) != 2 {
		t.Fatalf("moveCalls = %v, want 2 calls", provider.moveCalls)
	}
	for _, call := range provider.moveCalls {
		if call != "1->Archive" && call != "2->Archive" {
			t.Errorf("unexpected move call %q", call)
		}
	}

	if exists, _, _ := messageSeenFlagged(t, server, accountID, 1); exists {
		t.Error("message 1 should have been removed from the local Inbox index after archiving")
	}
	if exists, _, _ := messageSeenFlagged(t, server, accountID, 2); exists {
		t.Error("message 2 should have been removed from the local Inbox index after archiving")
	}
	if exists, _, _ := messageSeenFlagged(t, server, accountID, 3); !exists {
		t.Error("message 3 is from a different sender and should not have been touched")
	}
}
