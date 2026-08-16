package mailapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wollow/backend/internal/mail"
)

// partStubProvider serves one canned message and its parts, standing in for a
// live IMAP session.
type partStubProvider struct {
	message  *mail.Message
	parts    map[string]*mail.PartContent
	flagged  []string
	partReqs []string
}

func (p *partStubProvider) ListMessages(context.Context, string, int, int) ([]mail.MessageSummary, error) {
	return nil, nil
}
func (p *partStubProvider) GetMessage(context.Context, string, string) (*mail.Message, error) {
	return p.message, nil
}
func (p *partStubProvider) FetchPart(_ context.Context, _, _, partID string) (*mail.PartContent, error) {
	p.partReqs = append(p.partReqs, partID)
	part, ok := p.parts[partID]
	if !ok {
		return nil, http.ErrMissingFile
	}
	return part, nil
}
func (p *partStubProvider) DeleteMessage(context.Context, string, string) error { return nil }
func (p *partStubProvider) SetFlag(_ context.Context, _, id, flag string, _ bool) error {
	p.flagged = append(p.flagged, id+":"+flag)
	return nil
}
func (p *partStubProvider) MoveMessage(context.Context, string, string, string) error { return nil }
func (p *partStubProvider) DeleteMessages(_ context.Context, _ string, ids []string) ([]string, error) {
	return ids, nil
}
func (p *partStubProvider) SetFlags(_ context.Context, _ string, ids []string, _ string, _ bool) ([]string, error) {
	return ids, nil
}
func (p *partStubProvider) MoveMessages(_ context.Context, _ string, ids []string, _ string) ([]string, error) {
	return ids, nil
}
func (p *partStubProvider) ListUIDs(context.Context, string) ([]uint32, error) { return nil, nil }
func (p *partStubProvider) FetchForSync(context.Context, string, []uint32) ([]mail.SyncMessage, error) {
	return nil, nil
}
func (p *partStubProvider) FetchRaw(context.Context, string, []uint32) ([]mail.RawMessage, error) {
	return nil, nil
}
func (p *partStubProvider) FetchHeaders(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (p *partStubProvider) ResolveTrashFolder(context.Context) (string, error) {
	return "Trash", nil
}
func (p *partStubProvider) ResolveArchiveFolder(context.Context) (string, error) {
	return "Archive", nil
}

func newPartServer(t *testing.T, provider *partStubProvider) (*Server, http.Handler, int64) {
	t.Helper()
	server, _, accountID := newTestServer(t)
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) { return provider, nil }

	mux := http.NewServeMux()
	server.Register(mux)
	return server, mux, accountID
}

func pathf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// An image referenced by cid: renders inline; that is the whole point of the
// endpoint, and the reason a `cid:` URL can be handed to it verbatim.
func TestGetMessagePartServesInlineImages(t *testing.T) {
	provider := &partStubProvider{parts: map[string]*mail.PartContent{
		"logo@example": {FileName: "logo.png", ContentType: "image/png", Content: []byte("\x89PNG fake")},
	}}
	_, mux, accountID := newPartServer(t, provider)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET",
		pathf("/api/mail/accounts/%d/messages/7/parts/logo@example?folder=INBOX", accountID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Errorf("Content-Disposition = %q, want an inline disposition", got)
	}
	if w.Body.String() != "\x89PNG fake" {
		t.Error("part body did not round-trip")
	}
}

// The body iframe is sandboxed, but this endpoint is also reachable as a
// top-level URL where no sandbox applies. An HTML or SVG part served inline
// there would execute as a document on the app's own origin.
func TestGetMessagePartForcesRiskyTypesToDownload(t *testing.T) {
	provider := &partStubProvider{parts: map[string]*mail.PartContent{
		"2": {FileName: "payload.html", ContentType: "text/html", Content: []byte("<script>alert(1)</script>")},
		"3": {FileName: "payload.svg", ContentType: "image/svg+xml", Content: []byte("<svg onload='alert(1)'/>")},
	}}
	_, mux, accountID := newPartServer(t, provider)

	for _, partID := range []string{"2", "3"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET",
			pathf("/api/mail/accounts/%d/messages/7/parts/%s", accountID, partID), nil))

		if w.Code != http.StatusOK {
			t.Fatalf("part %s: status = %d", partID, w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("part %s: Content-Type = %q, want application/octet-stream", partID, got)
		}
		if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
			t.Errorf("part %s: Content-Disposition = %q, want an attachment disposition", partID, got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("part %s: missing nosniff, so a browser may sniff it back to HTML", partID)
		}
	}
}

// ?download=1 turns even a displayable type into a save, which is what the
// attachment list's links use.
func TestGetMessagePartHonoursDownloadFlag(t *testing.T) {
	provider := &partStubProvider{parts: map[string]*mail.PartContent{
		"2": {FileName: "statement.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4")},
	}}
	_, mux, accountID := newPartServer(t, provider)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET",
		pathf("/api/mail/accounts/%d/messages/7/parts/2?download=1", accountID), nil))

	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment disposition", got)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "statement.pdf") {
		t.Errorf("Content-Disposition = %q, want it to carry the filename", w.Header().Get("Content-Disposition"))
	}
}

func TestGetMessagePartReportsMissingParts(t *testing.T) {
	provider := &partStubProvider{parts: map[string]*mail.PartContent{}}
	_, mux, accountID := newPartServer(t, provider)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET",
		pathf("/api/mail/accounts/%d/messages/7/parts/nope", accountID), nil))

	if w.Code == http.StatusOK {
		t.Errorf("status = 200 for a part that does not exist")
	}
}

// Opening a message is what marks it read now — the detail request sets \Seen
// on the server and mirrors it into the index, so the list behind it agrees
// without waiting for the next sync.
func TestGetMessageMarksItReadAndUpdatesTheIndex(t *testing.T) {
	provider := &partStubProvider{message: &mail.Message{
		MessageSummary: mail.MessageSummary{ID: "5", Subject: "hello", Seen: false},
		BodyText:       "hi",
	}}
	server, mux, accountID := newPartServer(t, provider)
	seedMessage(t, server, accountID, 5, false, false)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET",
		pathf("/api/mail/accounts/%d/messages/5?folder=INBOX", accountID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if len(provider.flagged) != 1 || provider.flagged[0] != `5:\Seen` {
		t.Errorf("flag calls = %v, want one \\Seen on uid 5", provider.flagged)
	}
	if exists, seen, _ := messageSeenFlagged(t, server, accountID, 5); !exists || !seen {
		t.Error("the local index still says unread after the message was opened")
	}
}

// A message already read must not cost a redundant IMAP round trip.
func TestGetMessageDoesNotReflagAnAlreadyReadMessage(t *testing.T) {
	provider := &partStubProvider{message: &mail.Message{
		MessageSummary: mail.MessageSummary{ID: "6", Seen: true},
	}}
	server, mux, accountID := newPartServer(t, provider)
	seedMessage(t, server, accountID, 6, true, false)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET",
		pathf("/api/mail/accounts/%d/messages/6", accountID), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if len(provider.flagged) != 0 {
		t.Errorf("flag calls = %v, want none", provider.flagged)
	}
}
