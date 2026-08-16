package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIMAPServer speaks just enough IMAP to accept a login, a SELECT, and the
// commands the bulk operations issue — and, crucially, records every command
// line it receives.
//
// The bug these tests exist for is invisible to a stubbed provider: the code
// "worked", it just issued two commands per message. Only counting what
// actually goes on the wire proves the batching is real.
type fakeIMAPServer struct {
	listener net.Listener

	mu       sync.Mutex
	commands []string
	// failStore makes STORE fail, to exercise the per-message fallback.
	failStore bool
	// noUIDPlus makes UID EXPUNGE fail the way a server without UIDPLUS does.
	noUIDPlus bool
	// mailboxes is what LIST returns. Each entry is a raw LIST response line
	// after "* LIST ", e.g. `(\Trash) "/" "[Gmail]/Trash"`. Empty means the
	// server lists nothing at all.
	mailboxes []string
}

func newFakeIMAPServer(t *testing.T) *fakeIMAPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeIMAPServer{listener: listener}
	go server.serve()
	t.Cleanup(func() { listener.Close() })
	return server
}

func (s *fakeIMAPServer) addr() (host string, port int) {
	tcp := s.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcp.Port
}

func (s *fakeIMAPServer) record(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, line)
}

// commandsOfType returns every recorded command whose verb matches, with the
// tag stripped: "UID STORE 1:900 +FLAGS.SILENT (\Deleted)".
func (s *fakeIMAPServer) commandsOfType(verb string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, line := range s.commands {
		_, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(rest), strings.ToUpper(verb)) {
			out = append(out, rest)
		}
	}
	return out
}

func (s *fakeIMAPServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeIMAPServer) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 UIDPLUS MOVE] fake ready\r\n")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		s.record(line)

		tag, rest, _ := strings.Cut(line, " ")
		verb := strings.ToUpper(rest)
		switch {
		case strings.HasPrefix(verb, "LOGIN"):
			fmt.Fprintf(conn, "%s OK LOGIN completed\r\n", tag)
		case strings.HasPrefix(verb, "CAPABILITY"):
			fmt.Fprint(conn, "* CAPABILITY IMAP4rev1 UIDPLUS MOVE\r\n")
			fmt.Fprintf(conn, "%s OK CAPABILITY completed\r\n", tag)
		case strings.HasPrefix(verb, "SELECT"):
			fmt.Fprint(conn, "* 1000 EXISTS\r\n* OK [UIDVALIDITY 1] ok\r\n* FLAGS (\\Seen \\Deleted)\r\n")
			fmt.Fprintf(conn, "%s OK [READ-WRITE] SELECT completed\r\n", tag)
		case strings.HasPrefix(verb, "UID STORE"), strings.HasPrefix(verb, "STORE"):
			if s.failStore {
				fmt.Fprintf(conn, "%s NO STORE refused\r\n", tag)
				continue
			}
			fmt.Fprintf(conn, "%s OK STORE completed\r\n", tag)
		case strings.HasPrefix(verb, "UID EXPUNGE"):
			if s.noUIDPlus {
				fmt.Fprintf(conn, "%s BAD UID EXPUNGE not supported\r\n", tag)
				continue
			}
			fmt.Fprintf(conn, "%s OK EXPUNGE completed\r\n", tag)
		case strings.HasPrefix(verb, "EXPUNGE"):
			fmt.Fprintf(conn, "%s OK EXPUNGE completed\r\n", tag)
		case strings.HasPrefix(verb, "UID MOVE"), strings.HasPrefix(verb, "MOVE"):
			fmt.Fprintf(conn, "%s OK MOVE completed\r\n", tag)
		case strings.HasPrefix(verb, "LIST"):
			for _, mb := range s.mailboxes {
				fmt.Fprintf(conn, "* LIST %s\r\n", mb)
			}
			fmt.Fprintf(conn, "%s OK LIST completed\r\n", tag)
		case strings.HasPrefix(verb, "CREATE"):
			fmt.Fprintf(conn, "%s OK CREATE completed\r\n", tag)
		case strings.HasPrefix(verb, "STARTTLS"):
			// A genuinely plaintext server refuses, which is what makes the
			// client fall back to an insecure connection. Answering OK without
			// negotiating would hang the handshake — the exact stall
			// connectTimeout exists to bound.
			fmt.Fprintf(conn, "%s BAD STARTTLS not supported\r\n", tag)
		case strings.HasPrefix(verb, "LOGOUT"):
			fmt.Fprint(conn, "* BYE\r\n")
			fmt.Fprintf(conn, "%s OK LOGOUT completed\r\n", tag)
			return
		default:
			fmt.Fprintf(conn, "%s OK completed\r\n", tag)
		}
	}
}

func connectFake(t *testing.T, server *fakeIMAPServer) *IMAPProvider {
	t.Helper()
	host, port := server.addr()
	provider, err := NewIMAPProvider(AccountCredentials{
		Host: host, Port: port, Username: "u", Password: "p", UseTLS: false,
	})
	if err != nil {
		t.Fatalf("connecting to fake server: %v", err)
	}
	t.Cleanup(func() { provider.Close() })
	return provider
}

func idRange(n int) []string {
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		ids = append(ids, strconv.Itoa(i))
	}
	return ids
}

// gmailMailboxes is what Gmail's LIST returns: a \Trash special-use folder
// under the [Gmail] namespace.
var gmailMailboxes = []string{
	`(\HasNoChildren) "/" "INBOX"`,
	`(\All \HasNoChildren) "/" "[Gmail]/All Mail"`,
	`(\Trash \HasNoChildren) "/" "[Gmail]/Trash"`,
	`(\Sent \HasNoChildren) "/" "[Gmail]/Sent Mail"`,
}

// The bug: on Gmail, "\Deleted + EXPUNGE" from INBOX is *archive*. It strips
// the INBOX label and leaves the message in All Mail, so mail the user deleted
// is still in their account. Deleting has to MOVE to [Gmail]/Trash.
func TestDeleteMessagesMovesToTrashRatherThanExpunging(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.mailboxes = gmailMailboxes
	provider := connectFake(t, server)

	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", idRange(100))
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if len(deleted) != 100 {
		t.Errorf("reported %d deleted, want 100", len(deleted))
	}

	moves := server.commandsOfType("UID MOVE")
	if len(moves) == 0 {
		t.Fatal("no MOVE issued — the mail would stay in Gmail's All Mail")
	}
	if !strings.Contains(moves[0], "[Gmail]/Trash") {
		t.Errorf("moved to the wrong folder: %q", moves[0])
	}
	// An expunge alongside the move would be the old, wrong behaviour.
	if expunges := server.commandsOfType("UID EXPUNGE"); len(expunges) > 0 {
		t.Errorf("still expunging as well as moving: %v", expunges)
	}
}

// Servers that genuinely have no trash still have to delete something.
func TestDeleteMessagesExpungesWhenThereIsNoTrash(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.mailboxes = []string{`(\HasNoChildren) "/" "INBOX"`}
	provider := connectFake(t, server)

	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", idRange(10))
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if len(deleted) != 10 {
		t.Errorf("reported %d deleted, want 10", len(deleted))
	}
	if expunges := server.commandsOfType("UID EXPUNGE"); len(expunges) == 0 {
		t.Error("no expunge on a server with no trash folder — nothing was deleted")
	}
	if moves := server.commandsOfType("UID MOVE"); len(moves) > 0 {
		t.Errorf("moved to a folder the server does not have: %v", moves)
	}
}

// Emptying the trash means destroying the mail, not moving it onto itself.
func TestDeleteMessagesFromTrashExpunges(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.mailboxes = gmailMailboxes
	provider := connectFake(t, server)

	if _, err := provider.DeleteMessages(context.Background(), "[Gmail]/Trash", idRange(5)); err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if moves := server.commandsOfType("UID MOVE"); len(moves) > 0 {
		t.Errorf("moved trash onto itself: %v", moves)
	}
	if expunges := server.commandsOfType("UID EXPUNGE"); len(expunges) == 0 {
		t.Error("deleting from the trash did not permanently remove anything")
	}
}

// Without SPECIAL-USE, a conventional trash name the server actually lists is
// still the right target — but a name it doesn't have must never be invented.
func TestResolveTrashFolderFallsBackToListedNamesOnly(t *testing.T) {
	t.Run("uses a listed conventional name", func(t *testing.T) {
		server := newFakeIMAPServer(t)
		server.mailboxes = []string{
			`(\HasNoChildren) "/" "INBOX"`,
			`(\HasNoChildren) "/" "Deleted Items"`,
		}
		provider := connectFake(t, server)

		trash, err := provider.ResolveTrashFolder(context.Background())
		if err != nil {
			t.Fatalf("ResolveTrashFolder: %v", err)
		}
		if trash != "Deleted Items" {
			t.Errorf("trash = %q, want %q", trash, "Deleted Items")
		}
	})

	t.Run("invents nothing", func(t *testing.T) {
		server := newFakeIMAPServer(t)
		server.mailboxes = []string{`(\HasNoChildren) "/" "INBOX"`}
		provider := connectFake(t, server)

		trash, _ := provider.ResolveTrashFolder(context.Background())
		if trash != "" {
			t.Errorf("trash = %q, want empty — deleted mail would land somewhere nothing looks", trash)
		}
	})
}

// Gmail has no \Archive mailbox. Falling through to the literal name "Archive"
// creates a stray label and files the mail under it, instead of archiving it
// the way Gmail means — moving it into All Mail.
func TestResolveArchiveFolderUsesAllMailOnGmail(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.mailboxes = gmailMailboxes
	provider := connectFake(t, server)

	archive, err := provider.ResolveArchiveFolder(context.Background())
	if err != nil {
		t.Fatalf("ResolveArchiveFolder: %v", err)
	}
	if archive != "[Gmail]/All Mail" {
		t.Errorf("archive = %q, want %q", archive, "[Gmail]/All Mail")
	}
}

// A server that does advertise \Archive must still win over the All Mail path.
func TestResolveArchiveFolderPrefersSpecialUse(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.mailboxes = []string{
		`(\HasNoChildren) "/" "INBOX"`,
		`(\All \HasNoChildren) "/" "Everything"`,
		`(\Archive \HasNoChildren) "/" "Archived Mail"`,
	}
	provider := connectFake(t, server)

	archive, _ := provider.ResolveArchiveFolder(context.Background())
	if archive != "Archived Mail" {
		t.Errorf("archive = %q, want %q", archive, "Archived Mail")
	}
}

// The regression: deleting a sender's 1,348 messages used to send a STORE and
// an EXPUNGE *each* — ~2,700 sequential commands, which no timeout survives.
// It must be a constant number of commands regardless of the message count.
func TestDeleteMessagesBatchesOntoTheWire(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	const messages = 1348
	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", idRange(messages))
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if len(deleted) != messages {
		t.Errorf("reported %d deleted, want %d", len(deleted), messages)
	}

	stores := server.commandsOfType("UID STORE")
	expunges := server.commandsOfType("UID EXPUNGE")
	// 1348 UIDs at a 500 chunk cap is three commands of each.
	if len(stores) > 4 || len(expunges) > 4 {
		t.Errorf("sent %d STOREs and %d EXPUNGEs for %d messages — still looping per message",
			len(stores), len(expunges), messages)
	}
	if len(stores) == 0 {
		t.Fatal("no STORE reached the server")
	}
	// Consecutive UIDs must compress into a range, not a list of 500 numbers.
	if !strings.Contains(stores[0], ":") {
		t.Errorf("first STORE did not use a UID range: %q", stores[0])
	}
	t.Logf("%d messages cost %d STOREs + %d EXPUNGEs; first: %s",
		messages, len(stores), len(expunges), stores[0])
}

func TestSetFlagsBatchesOntoTheWire(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	updated, err := provider.SetFlags(context.Background(), "INBOX", idRange(900), `\Seen`, true)
	if err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	if len(updated) != 900 {
		t.Errorf("reported %d updated, want 900", len(updated))
	}
	if stores := server.commandsOfType("UID STORE"); len(stores) > 3 {
		t.Errorf("sent %d STOREs for 900 messages", len(stores))
	}
}

func TestMoveMessagesBatchesOntoTheWire(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	moved, err := provider.MoveMessages(context.Background(), "INBOX", idRange(900), "Archive")
	if err != nil {
		t.Fatalf("MoveMessages: %v", err)
	}
	if len(moved) != 900 {
		t.Errorf("reported %d moved, want 900", len(moved))
	}
	if moves := server.commandsOfType("UID MOVE"); len(moves) > 3 {
		t.Errorf("sent %d MOVEs for 900 messages", len(moves))
	}
}

// A server without UIDPLUS rejects UID EXPUNGE; the plain EXPUNGE fallback has
// to keep the delete working rather than reporting everything as failed.
func TestDeleteMessagesFallsBackWithoutUIDPlus(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.noUIDPlus = true
	provider := connectFake(t, server)

	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", idRange(50))
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if len(deleted) != 50 {
		t.Errorf("reported %d deleted, want 50 — the fallback did not engage", len(deleted))
	}
	if plain := server.commandsOfType("EXPUNGE"); len(plain) == 0 {
		t.Error("no plain EXPUNGE was attempted after UID EXPUNGE was refused")
	}
}

// When the batch fails as a unit there is no per-message verdict, so the code
// retries individually to find out which ones are actually bad. A server that
// refuses everything must therefore report everything failed, not silently
// claim success.
func TestDeleteMessagesReportsAWholesaleFailure(t *testing.T) {
	server := newFakeIMAPServer(t)
	server.failStore = true
	provider := connectFake(t, server)

	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", idRange(5))
	if err != nil {
		t.Fatalf("DeleteMessages returned an error rather than a per-message verdict: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("reported %d deleted against a server refusing every STORE", len(deleted))
	}
}

// A cancelled request must stop issuing commands rather than working through
// every remaining chunk.
func TestDeleteMessagesHonoursContextCancellation(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.DeleteMessages(ctx, "INBOX", idRange(2000)); err == nil {
		t.Error("expected a cancellation error")
	}
	if stores := server.commandsOfType("UID STORE"); len(stores) > 1 {
		t.Errorf("kept issuing %d STOREs after cancellation", len(stores))
	}
}

// Ids that aren't UIDs must not take the good ones down with them.
func TestBulkOperationsSkipUnparseableIDs(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", []string{"1", "not-a-uid", "3"})
	if err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if len(deleted) != 2 {
		t.Errorf("deleted %v, want the two real ids", deleted)
	}
}

func TestDeleteMessagesWithNoIDsIsANoOp(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	deleted, err := provider.DeleteMessages(context.Background(), "INBOX", nil)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("got (%v, %v), want (nil, nil)", deleted, err)
	}
	if stores := server.commandsOfType("UID STORE"); len(stores) != 0 {
		t.Errorf("issued %d commands for an empty id list", len(stores))
	}
}

// Guards the wall-clock claim directly: the batched path has to finish quickly
// even though the fake server answers every command in sequence.
func TestBulkDeleteIsFastEnoughForAnHTTPRequest(t *testing.T) {
	server := newFakeIMAPServer(t)
	provider := connectFake(t, server)

	start := time.Now()
	if _, err := provider.DeleteMessages(context.Background(), "INBOX", idRange(5000)); err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("deleting 5000 messages took %s", elapsed)
	}
}
