package mailapi

import (
	"context"
	"database/sql"
	"testing"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/platform/crypto"
	"wollow/backend/internal/platform/db"
)

// stubProvider is a Provider that talks to nothing. Only the sync path is
// exercised here, so the read methods return empty results and the write
// methods are never called.
type stubProvider struct {
	id     int
	closed bool
}

func (p *stubProvider) ListMessages(context.Context, string, int, int) ([]mail.MessageSummary, error) {
	return nil, nil
}
func (p *stubProvider) GetMessage(context.Context, string, string) (*mail.Message, error) {
	return nil, nil
}
func (p *stubProvider) DeleteMessage(context.Context, string, string) error         { return nil }
func (p *stubProvider) SetFlag(context.Context, string, string, string, bool) error { return nil }
func (p *stubProvider) MoveMessage(context.Context, string, string, string) error   { return nil }
func (p *stubProvider) ListUIDs(context.Context, string) ([]uint32, error)          { return nil, nil }
func (p *stubProvider) FetchForSync(context.Context, string, []uint32) ([]mail.SyncMessage, error) {
	return nil, nil
}
func (p *stubProvider) FetchRaw(context.Context, string, []uint32) ([]mail.RawMessage, error) {
	return nil, nil
}
func (p *stubProvider) FetchHeaders(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (p *stubProvider) DeleteMessages(_ context.Context, _ string, ids []string) ([]string, error) {
	return ids, nil
}
func (p *stubProvider) SetFlags(_ context.Context, _ string, ids []string, _ string, _ bool) ([]string, error) {
	return ids, nil
}
func (p *stubProvider) MoveMessages(_ context.Context, _ string, ids []string, _ string) ([]string, error) {
	return ids, nil
}
func (p *stubProvider) FetchPart(context.Context, string, string, string) (*mail.PartContent, error) {
	return nil, nil
}
func (p *stubProvider) ResolveTrashFolder(context.Context) (string, error) {
	return "Trash", nil
}
func (p *stubProvider) ResolveArchiveFolder(context.Context) (string, error) { return "Archive", nil }
func (p *stubProvider) Close() error                                         { p.closed = true; return nil }

func newTestServer(t *testing.T) (*Server, *sql.DB, int64) {
	t.Helper()

	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	box, err := crypto.New("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("crypto: %v", err)
	}
	encrypted, err := box.Encrypt("app-password")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	res, err := conn.Exec(`
		INSERT INTO mail_accounts (label, imap_host, imap_port, username, encrypted_password)
		VALUES ('Test', 'imap.example.com', 993, 'test@example.com', ?)`, encrypted)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	accountID, _ := res.LastInsertId()

	return NewServer(conn, box), conn, accountID
}

// TestSyncAndAfterSyncShareOneConnection is the load-bearing check for the
// pipeline merge. Money's ingest runs off the AfterSync hook precisely so that
// one mailbox costs one IMAP session; if the hook were handed anything other
// than the connection sync just used — or if it ran outside the per-account
// lock — the app would be back to two clients competing over one mailbox.
func TestSyncAndAfterSyncShareOneConnection(t *testing.T) {
	server, _, accountID := newTestServer(t)

	created := 0
	var lastCreated *stubProvider
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) {
		created++
		lastCreated = &stubProvider{id: created}
		return lastCreated, nil
	}

	hookCalls := 0
	var hookProvider mail.Provider
	server.AfterSync = func(_ context.Context, id int64, p mail.Provider) error {
		hookCalls++
		hookProvider = p
		if id != accountID {
			t.Errorf("hook got account %d, want %d", id, accountID)
		}
		return nil
	}

	if _, err := server.syncAccount(context.Background(), accountID, "INBOX"); err != nil {
		t.Fatalf("syncAccount: %v", err)
	}

	if created != 1 {
		t.Errorf("opened %d connections for one sync pass, want exactly 1", created)
	}
	if hookCalls != 1 {
		t.Fatalf("AfterSync ran %d times, want 1", hookCalls)
	}
	if hookProvider != mail.Provider(lastCreated) {
		t.Error("AfterSync got a different connection than sync used")
	}
	if !lastCreated.closed {
		t.Error("connection was not closed after the pass")
	}
}

// TestAfterSyncFailureDoesNotFailTheSync keeps the two concerns separable: the
// index has already been committed by the time the hook runs, so a Money-side
// parsing problem must not make Mail report a failed sync.
func TestAfterSyncFailureDoesNotFailTheSync(t *testing.T) {
	server, _, accountID := newTestServer(t)

	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) {
		return &stubProvider{}, nil
	}
	server.AfterSync = func(context.Context, int64, mail.Provider) error {
		return context.DeadlineExceeded
	}

	result, err := server.syncAccount(context.Background(), accountID, "INBOX")
	if err != nil {
		t.Errorf("sync reported failure because the after-sync hook failed: %v", err)
	}
	if result == nil {
		t.Error("sync returned no result despite succeeding")
	}
}

// TestSyncWithoutHook covers the plain Mail-only configuration.
func TestSyncWithoutHook(t *testing.T) {
	server, _, accountID := newTestServer(t)
	server.providerFactory = func(mail.AccountCredentials) (mail.Provider, error) {
		return &stubProvider{}, nil
	}

	if _, err := server.syncAccount(context.Background(), accountID, "INBOX"); err != nil {
		t.Fatalf("syncAccount with no AfterSync hook: %v", err)
	}
}
