package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
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

// NewIMAPProvider dials and logs in to the IMAP server described by creds.
// If creds.UseTLS is true, implicit TLS (port 993 style) is used; otherwise
// the connection is upgraded via STARTTLS when the server advertises it, and
// falls back to a plaintext connection otherwise.
func NewIMAPProvider(creds AccountCredentials) (*IMAPProvider, error) {
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
	bodySection := &imap.FetchItemBodySection{}
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

	raw := m.FindBodySection(bodySection)
	if raw != nil {
		if err := populateBody(msg, raw); err != nil {
			return nil, fmt.Errorf("imap: parse message %s: %w", id, err)
		}
	}

	return msg, nil
}

// populateBody parses a raw RFC 5322 message and fills in BodyText/BodyHTML,
// preferring text/plain for BodyText and text/html for BodyHTML when the
// message is multipart. For a non-multipart message, the single body is used
// to fill whichever field matches its content type (defaulting to text).
func populateBody(msg *Message, raw []byte) error {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Stop on malformed parts but keep whatever we've already parsed.
			break
		}

		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			b, readErr := io.ReadAll(part.Body)
			if readErr != nil {
				continue
			}
			switch contentType {
			case "text/html":
				if msg.BodyHTML == "" {
					msg.BodyHTML = string(b)
				}
			default:
				if msg.BodyText == "" {
					msg.BodyText = string(b)
				}
			}
		case *mail.AttachmentHeader:
			// Attachments are not surfaced through Message yet; skip.
		}
	}

	return nil
}

// DeleteMessage permanently deletes a message: it marks the UID \Deleted and
// expunges it immediately. We delete outright rather than moving to Trash
// because there is no reliable, provider-agnostic "Trash" folder name to
// target across arbitrary IMAP servers.
func (p *IMAPProvider) DeleteMessage(ctx context.Context, folder, id string) error {
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
	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}
	if err := p.client.Store(uidSet, &storeFlags, nil).Close(); err != nil {
		return fmt.Errorf("imap: mark deleted %s: %w", id, err)
	}

	if err := p.client.UIDExpunge(uidSet).Close(); err != nil {
		// Some servers don't support UID EXPUNGE (requires UIDPLUS); fall
		// back to a plain EXPUNGE of the whole mailbox.
		if err2 := p.client.Expunge().Close(); err2 != nil {
			return fmt.Errorf("imap: expunge %s: %w", id, err2)
		}
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
