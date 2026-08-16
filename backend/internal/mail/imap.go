package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// IMAPProvider implements Provider on top of a single persistent IMAP
// connection. IMAP connections may only process one command at a time, so
// every exported method takes mu before touching the client.
type IMAPProvider struct {
	mu      sync.Mutex
	client  *imapclient.Client
	creds   AccountCredentials
	mailbox string // currently SELECTed mailbox, "" if none
}

// connectTimeout bounds the whole dial → TLS handshake → LOGIN sequence.
//
// The library's dialer times out the TCP connect, but nothing bounds the
// handshake or the login that follow: a server that accepts a connection and
// then stalls — or acknowledges STARTTLS without ever negotiating — hangs the
// request forever. Every mailbox operation starts here, so an unbounded
// connect is an unbounded API call.
const connectTimeout = 30 * time.Second

// NewIMAPProvider dials and logs in to the IMAP server described by creds.
// If creds.UseTLS is true, implicit TLS (port 993 style) is used; otherwise
// the connection is upgraded via STARTTLS when the server advertises it, and
// falls back to a plaintext connection otherwise.
func NewIMAPProvider(creds AccountCredentials) (*IMAPProvider, error) {
	type result struct {
		provider *IMAPProvider
		err      error
	}
	// Buffered so the goroutine can always finish and hand back whatever it
	// built, even after this function has given up waiting for it.
	done := make(chan result, 1)
	go func() {
		provider, err := dialAndLogin(creds)
		done <- result{provider, err}
	}()

	select {
	case r := <-done:
		return r.provider, r.err
	case <-time.After(connectTimeout):
		// Close whatever the abandoned attempt eventually produces, so a slow
		// server costs a delayed cleanup rather than a leaked socket.
		go func() {
			if r := <-done; r.provider != nil {
				r.provider.Close()
			}
		}()
		return nil, fmt.Errorf("imap: connecting to %s timed out after %s", creds.Host, connectTimeout)
	}
}

func dialAndLogin(creds AccountCredentials) (*IMAPProvider, error) {
	addr := net.JoinHostPort(creds.Host, strconv.Itoa(creds.Port))

	var (
		client *imapclient.Client
		err    error
	)
	if creds.UseTLS {
		client, err = imapclient.DialTLS(addr, &imapclient.Options{
			TLSConfig: &tls.Config{ServerName: creds.Host},
		})
		if err != nil {
			return nil, fmt.Errorf("imap: dial TLS %s: %w", addr, err)
		}
	} else {
		client, err = imapclient.DialStartTLS(addr, &imapclient.Options{
			TLSConfig: &tls.Config{ServerName: creds.Host},
		})
		if err != nil {
			// Fall back to a plain, unencrypted connection if the server
			// doesn't support STARTTLS at all.
			client, err = imapclient.DialInsecure(addr, nil)
			if err != nil {
				return nil, fmt.Errorf("imap: dial %s: %w", addr, err)
			}
		}
	}

	if err := client.Login(creds.Username, creds.Password).Wait(); err != nil {
		client.Close()
		return nil, fmt.Errorf("imap: login: %w", err)
	}

	return &IMAPProvider{client: client, creds: creds}, nil
}

// Close logs out and closes the underlying connection.
func (p *IMAPProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client == nil {
		return nil
	}
	// Best-effort LOGOUT; ignore its error but still close the socket.
	_ = p.client.Logout().Wait()
	err := p.client.Close()
	p.client = nil
	if err != nil {
		return fmt.Errorf("imap: close: %w", err)
	}
	return nil
}

// selectMailbox switches the currently selected mailbox if needed. Caller
// must hold p.mu.
func (p *IMAPProvider) selectMailbox(folder string) error {
	if folder == "" {
		folder = "INBOX"
	}
	if p.mailbox == folder {
		return nil
	}
	if _, err := p.client.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("imap: select %q: %w", folder, err)
	}
	p.mailbox = folder
	return nil
}

// ListMessages returns a page of message summaries for folder, newest first.
func (p *IMAPProvider) ListMessages(ctx context.Context, folder string, limit, offset int) ([]MessageSummary, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	searchData, err := p.client.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap: search %q: %w", folder, err)
	}

	uids := searchData.AllUIDs()
	// Newest first: IMAP UIDs are monotonically increasing, so sort
	// descending to get newest-first ordering without an extra round trip.
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })

	if offset >= len(uids) {
		return []MessageSummary{}, nil
	}
	end := offset + limit
	if limit <= 0 || end > len(uids) {
		end = len(uids)
	}
	page := uids[offset:end]
	if len(page) == 0 {
		return []MessageSummary{}, nil
	}

	uidSet := imap.UIDSetNum(page...)
	fetchOptions := &imap.FetchOptions{
		UID:      true,
		Envelope: true,
		Flags:    true,
	}
	messages, err := p.client.Fetch(uidSet, fetchOptions).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch summaries %q: %w", folder, err)
	}

	byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(messages))
	for _, m := range messages {
		byUID[m.UID] = m
	}

	summaries := make([]MessageSummary, 0, len(page))
	for _, uid := range page {
		m, ok := byUID[uid]
		if !ok {
			continue
		}
		summaries = append(summaries, summaryFromFetch(uid, m))
	}
	return summaries, nil
}

func summaryFromFetch(uid imap.UID, m *imapclient.FetchMessageBuffer) MessageSummary {
	s := MessageSummary{ID: strconv.FormatUint(uint64(uid), 10)}
	if m.Envelope != nil {
		s.Subject = m.Envelope.Subject
		s.From = formatAddressList(m.Envelope.From)
		if !m.Envelope.Date.IsZero() {
			s.Date = m.Envelope.Date.Format("2006-01-02T15:04:05Z07:00")
		}
	}
	for _, f := range m.Flags {
		switch f {
		case imap.FlagSeen:
			s.Seen = true
		case imap.FlagFlagged:
			s.Flagged = true
		}
	}
	return s
}

func formatAddressList(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	out := ""
	for i, a := range addrs {
		if i > 0 {
			out += ", "
		}
		addr := a.Addr()
		if a.Name != "" && addr != "" {
			out += a.Name + " <" + addr + ">"
		} else if addr != "" {
			out += addr
		} else {
			out += a.Name
		}
	}
	return out
}

// GetMessage fetches the full message (envelope + body) for the given UID.
//
// The body section is peeked, so opening a message never implicitly marks it
// read: the API layer sets \Seen explicitly, which is what lets it mirror the
// change into the local index in the same breath.
func (p *IMAPProvider) GetMessage(ctx context.Context, folder, id string) (*Message, error) {
	uid, err := parseUID(id)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	uidSet := imap.UIDSetNum(uid)
	bodySection := &imap.FetchItemBodySection{Peek: true}
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		Flags:       true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}
	messages, err := p.client.Fetch(uidSet, fetchOptions).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch message %s: %w", id, err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("imap: message %s not found in %q", id, folder)
	}
	m := messages[0]

	msg := &Message{MessageSummary: summaryFromFetch(uid, m)}
	if m.Envelope != nil {
		msg.To = formatAddressList(m.Envelope.To)
		msg.Cc = formatAddressList(m.Envelope.Cc)
		msg.ReplyTo = formatAddressList(m.Envelope.ReplyTo)
	}

	// A message whose MIME tree is malformed still opens: parseMessage never
	// fails, it degrades. Losing the body of every quirky sender was the whole
	// reason this stopped returning an error.
	if raw := m.FindBodySection(bodySection); raw != nil {
		parsed := parseMessage(raw)
		msg.BodyText = parsed.Text
		msg.BodyHTML = parsed.HTML
		msg.Attachments = parsed.Attachments
	}
	if msg.Attachments == nil {
		msg.Attachments = []Attachment{}
	}

	return msg, nil
}

// FetchPart returns one decoded MIME part — an attachment to download, or the
// inline image an HTML body references by cid.
func (p *IMAPProvider) FetchPart(ctx context.Context, folder, id, partID string) (*PartContent, error) {
	uid, err := parseUID(id)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	// The whole message is fetched and walked rather than asking the server for
	// BODY[<part>]: the part numbers a client can derive from BODYSTRUCTURE
	// disagree with the ones go-message walks for nested and message/rfc822
	// parts, and a mismatch there serves the wrong attachment rather than
	// erroring. Walking one raw message is cheap enough at this scale.
	bodySection := &imap.FetchItemBodySection{Peek: true}
	messages, err := p.client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch part %s of %s: %w", partID, id, err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("imap: message %s not found in %q", id, folder)
	}
	raw := messages[0].FindBodySection(bodySection)
	if raw == nil {
		return nil, fmt.Errorf("imap: no body for message %s", id)
	}
	return findPart(raw, partID)
}

// DeleteMessage deletes one message, moving it to the server's trash where
// there is one. See DeleteMessages for why moving beats expunging — on Gmail,
// expunging from INBOX only strips the label and leaves the mail in All Mail.
func (p *IMAPProvider) DeleteMessage(ctx context.Context, folder, id string) error {
	deleted, err := p.DeleteMessages(ctx, folder, []string{id})
	if err != nil {
		return err
	}
	if len(deleted) == 0 {
		return fmt.Errorf("imap: could not delete message %s in %q", id, folder)
	}
	return nil
}

// SetFlag adds or removes an IMAP flag (e.g. "\\Seen", "\\Flagged") on a
// message.
func (p *IMAPProvider) SetFlag(ctx context.Context, folder, id, flag string, value bool) error {
	uid, err := parseUID(id)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return err
	}

	op := imap.StoreFlagsAdd
	if !value {
		op = imap.StoreFlagsDel
	}
	uidSet := imap.UIDSetNum(uid)
	storeFlags := imap.StoreFlags{
		Op:     op,
		Flags:  []imap.Flag{imap.Flag(flag)},
		Silent: true,
	}
	if err := p.client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
		return fmt.Errorf("imap: set flag %s on %s: %w", flag, id, err)
	}
	return nil
}

// FetchHeaders returns the raw RFC 5322 header block for one message, using a
// peeking BODY[HEADER] section so reading headers never marks the message as
// seen.
func (p *IMAPProvider) FetchHeaders(ctx context.Context, folder, id string) ([]byte, error) {
	uid, err := parseUID(id)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	bodySection := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, Peek: true}
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}
	messages, err := p.client.Fetch(imap.UIDSetNum(uid), fetchOptions).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch headers %s: %w", id, err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("imap: message %s not found in %q", id, folder)
	}

	raw := messages[0].FindBodySection(bodySection)
	if raw == nil {
		return nil, fmt.Errorf("imap: no header section for %s", id)
	}
	return raw, nil
}

// MoveMessage relocates a message into destFolder, archiving it. If the
// server doesn't have destFolder yet, it is created and the move retried
// once — there is no standard "Archive" folder name across IMAP servers, so
// callers that always ask for the same name (see ResolveArchiveFolder) will
// converge on creating it once and reusing it after.
func (p *IMAPProvider) MoveMessage(ctx context.Context, folder, id, destFolder string) error {
	uid, err := parseUID(id)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return err
	}

	uidSet := imap.UIDSetNum(uid)
	if _, err := p.client.Move(uidSet, destFolder).Wait(); err != nil {
		if createErr := p.client.Create(destFolder, nil).Wait(); createErr != nil {
			return fmt.Errorf("imap: move %s to %q: %w", id, destFolder, err)
		}
		// MOVE requires the source mailbox re-selected after CREATE on some
		// servers; selectMailbox is a no-op if it never actually changed.
		p.mailbox = ""
		if err := p.selectMailbox(folder); err != nil {
			return err
		}
		if _, err := p.client.Move(uidSet, destFolder).Wait(); err != nil {
			return fmt.Errorf("imap: move %s to %q after creating it: %w", id, destFolder, err)
		}
	}
	return nil
}

// ResolveArchiveFolder finds the mailbox the server itself flags \Archive
// (RFC 6154 SPECIAL-USE), falling back to the literal name "Archive" when the
// server doesn't advertise SPECIAL-USE or has no such mailbox — MoveMessage
// will create it on first use in that case.
func (p *IMAPProvider) ResolveArchiveFolder(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if archive := p.resolveSpecialFolder(imap.MailboxAttrArchive, "", nil); archive != "" {
		return archive, nil
	}
	// Gmail has no \Archive mailbox: archiving there means moving into All
	// Mail, which drops the INBOX label and is exactly what the user means.
	// Without this the fallback below invents a stray "Archive" label instead.
	if all := p.resolveSpecialFolder(imap.MailboxAttrAll, "", nil); all != "" {
		return all, nil
	}
	return "Archive", nil
}

// trashFallbackNames are the folder names servers without SPECIAL-USE use for
// their trash, most specific first. Unlike Archive, an unmatched name is *not*
// created: inventing a "Trash" folder the server doesn't recognize would leave
// deleted mail somewhere the user's other clients never look.
var trashFallbackNames = []string{
	"[Gmail]/Trash", "[Google Mail]/Trash",
	"Trash", "Deleted Items", "Deleted Messages", "INBOX.Trash",
}

// ResolveTrashFolder finds the mailbox the server flags \Trash, or a
// conventional trash folder that actually exists. Returns "" when the server
// has no trash at all, which tells DeleteMessages to fall back to expunging.
func (p *IMAPProvider) ResolveTrashFolder(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resolveSpecialFolder(imap.MailboxAttrTrash, "", trashFallbackNames), nil
}

// resolveSpecialFolder looks for a mailbox carrying the given RFC 6154
// attribute, then for any of the fallback names the server actually lists, and
// finally returns defaultName. Caller must hold p.mu.
func (p *IMAPProvider) resolveSpecialFolder(attr imap.MailboxAttr, defaultName string, fallbacks []string) string {
	mailboxes, err := p.client.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		// Discovery failing shouldn't block the operation.
		return defaultName
	}

	existing := make(map[string]string, len(mailboxes))
	for _, mb := range mailboxes {
		existing[strings.ToLower(mb.Mailbox)] = mb.Mailbox
		for _, a := range mb.Attrs {
			if a == attr {
				return mb.Mailbox
			}
		}
	}
	// No SPECIAL-USE match: accept a conventional name, but only one the
	// server really has.
	for _, name := range fallbacks {
		if actual, ok := existing[strings.ToLower(name)]; ok {
			return actual
		}
	}
	return defaultName
}

// ListUIDs returns every UID in the folder, ascending.
func (p *IMAPProvider) ListUIDs(ctx context.Context, folder string) ([]uint32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	searchData, err := p.client.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap: search %q: %w", folder, err)
	}

	uids := searchData.AllUIDs()
	out := make([]uint32, 0, len(uids))
	for _, uid := range uids {
		out = append(out, uint32(uid))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// snippetBytes is how much of the message text we pull for a preview snippet.
// Generous because multipart mail front-loads MIME headers, CSS and tracking
// markup; we need enough bytes to reach real prose after cleaning.
const snippetBytes = 4096

// FetchForSync fetches envelope, flags, size and a short text snippet for the
// given UIDs. The body section is fetched with Peek so syncing never marks
// messages as read.
func (p *IMAPProvider) FetchForSync(ctx context.Context, folder string, uids []uint32) ([]SyncMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	imapUIDs := make([]imap.UID, 0, len(uids))
	for _, uid := range uids {
		imapUIDs = append(imapUIDs, imap.UID(uid))
	}

	bodySection := &imap.FetchItemBodySection{
		Specifier: imap.PartSpecifierText,
		Partial:   &imap.SectionPartial{Offset: 0, Size: snippetBytes},
		Peek:      true,
	}
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		Flags:       true,
		RFC822Size:  true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}

	messages, err := p.client.Fetch(imap.UIDSetNum(imapUIDs...), fetchOptions).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: fetch for sync %q: %w", folder, err)
	}

	out := make([]SyncMessage, 0, len(messages))
	for _, m := range messages {
		sm := SyncMessage{
			UID:  uint32(m.UID),
			Size: m.RFC822Size,
		}
		if m.Envelope != nil {
			sm.Subject = m.Envelope.Subject
			sm.RFCMessageID = strings.TrimSpace(m.Envelope.MessageID)
			if !m.Envelope.Date.IsZero() {
				sm.Date = m.Envelope.Date.UTC().Format(time.RFC3339)
			}
			if len(m.Envelope.From) > 0 {
				from := m.Envelope.From[0]
				sm.FromName = from.Name
				sm.FromEmail = from.Addr()
				if at := strings.LastIndex(sm.FromEmail, "@"); at != -1 {
					sm.FromDomain = strings.ToLower(sm.FromEmail[at+1:])
				}
			}
		}
		for _, f := range m.Flags {
			switch f {
			case imap.FlagSeen:
				sm.Seen = true
			case imap.FlagFlagged:
				sm.Flagged = true
			}
		}
		if raw := m.FindBodySection(bodySection); raw != nil {
			sm.Snippet = makeSnippet(raw)
		}
		out = append(out, sm)
	}

	return out, nil
}

const maxSnippet = 300

var (
	styleBlockRE  = regexp.MustCompile(`(?is)<(style|script|head)[^>]*>.*?</(style|script|head)>`)
	tagRE         = regexp.MustCompile(`(?s)<[^>]*>`)
	cssRuleRE     = regexp.MustCompile(`(?s)[#.@]?[A-Za-z0-9_.#\-\[\]="' :>,]*\{[^{}]*\}`)
	mimeHeaderRE  = regexp.MustCompile(`(?i)^(content-[a-z-]+|mime-version|charset|boundary|x-[a-z-]+):`)
	qpSoftBreakRE = regexp.MustCompile(`=\r?\n`)
	qpHexRE       = regexp.MustCompile(`=([0-9A-Fa-f]{2})`)
)

// makeSnippet turns raw partial body bytes into a short, human-readable
// preview. Multipart mail front-loads MIME part headers, CSS and tracking
// markup, all of which would otherwise poison both the UI preview and the
// text the AI classifier sees. Best-effort throughout: bad input yields a
// shorter or empty snippet, never an error.
func makeSnippet(raw []byte) string {
	text := string(raw)

	// Drop MIME part headers and boundary markers line by line, keeping only
	// what looks like body content.
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "--"): // MIME boundary
			continue
		case mimeHeaderRE.MatchString(trimmed):
			continue
		case strings.EqualFold(trimmed, "This is a multipart message in MIME format."),
			strings.EqualFold(trimmed, "This is a multi-part message in MIME format."):
			continue
		}
		kept = append(kept, line)
	}
	text = strings.Join(kept, "\n")

	// Undo quoted-printable so "=3D" style escapes don't leak into the preview.
	text = qpSoftBreakRE.ReplaceAllString(text, "")
	text = qpHexRE.ReplaceAllStringFunc(text, func(m string) string {
		var b byte
		if _, err := fmt.Sscanf(m[1:], "%02x", &b); err != nil {
			return ""
		}
		if b == '\n' || b == '\r' || b >= 0x20 {
			return string(rune(b))
		}
		return ""
	})

	text = styleBlockRE.ReplaceAllString(text, " ")
	text = cssRuleRE.ReplaceAllString(text, " ")
	text = tagRE.ReplaceAllString(text, " ")

	replacer := strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&zwnj;", "", "&shy;", "",
	)
	text = replacer.Replace(text)

	// Collapse remaining entity noise (e.g. long &amp;amp; chains) and space.
	text = strings.Join(strings.Fields(text), " ")

	if len(text) > maxSnippet {
		text = strings.TrimSpace(text[:maxSnippet])
	}
	return text
}

func parseUID(id string) (imap.UID, error) {
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("imap: invalid message id %q: %w", id, err)
	}
	return imap.UID(n), nil
}

// rawFetchBatch caps how many whole messages are pulled per FETCH. Bodies with
// attachments are far larger than the header records FetchForSync deals in, so
// this is deliberately much smaller than the sync batch size.
const rawFetchBatch = 50

// FetchRaw returns complete messages for the given UIDs, using a peeking BODY
// section so reading a message for parsing never marks it as seen in the user's
// actual mailbox.
func (p *IMAPProvider) FetchRaw(ctx context.Context, folder string, uids []uint32) ([]RawMessage, error) {
	if len(uids) == 0 {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.selectMailbox(folder); err != nil {
		return nil, err
	}

	bodySection := &imap.FetchItemBodySection{Peek: true}
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	}

	out := make([]RawMessage, 0, len(uids))
	for start := 0; start < len(uids); start += rawFetchBatch {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		end := min(start+rawFetchBatch, len(uids))

		imapUIDs := make([]imap.UID, 0, end-start)
		for _, uid := range uids[start:end] {
			imapUIDs = append(imapUIDs, imap.UID(uid))
		}

		messages, err := p.client.Fetch(imap.UIDSetNum(imapUIDs...), fetchOptions).Collect()
		if err != nil {
			return nil, fmt.Errorf("imap: fetch raw %q: %w", folder, err)
		}
		for _, m := range messages {
			raw := m.FindBodySection(bodySection)
			if raw == nil {
				continue
			}
			out = append(out, RawMessage{UID: uint32(m.UID), Raw: raw})
		}
	}

	return out, nil
}
