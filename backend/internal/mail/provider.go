// Package mail defines the provider-agnostic interface for reading and
// managing mailboxes. IMAP/app-password is the first implementation;
// OAuth-based Gmail/Outlook providers implement the same interface later
// without any change to the API layer.
package mail

import "context"

type MessageSummary struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	From    string `json:"from"`
	Date    string `json:"date"`
	Seen    bool   `json:"seen"`
	Flagged bool   `json:"flagged"`
}

type Message struct {
	MessageSummary
	To       string `json:"to"`
	Cc       string `json:"cc"`
	ReplyTo  string `json:"replyTo"`
	BodyText string `json:"bodyText"`
	BodyHTML string `json:"bodyHtml"`
	// Attachments lists every non-body part, both real attachments and the
	// inline images an HTML body references with cid:. Each is fetched
	// separately by PartID rather than inlined here, so opening a message with
	// a 20 MB PDF on it stays cheap.
	Attachments []Attachment `json:"attachments"`
}

// Attachment describes one non-body MIME part. Content is deliberately absent:
// see Provider.FetchPart.
type Attachment struct {
	// PartID is the dotted IMAP-style part number ("2", "1.3") used to fetch
	// this part back.
	PartID string `json:"partId"`
	// ContentID is the Content-ID header without its angle brackets, i.e. the
	// value an HTML body's <img src="cid:..."> refers to. Empty for ordinary
	// attachments.
	ContentID   string `json:"contentId,omitempty"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	// Inline marks parts meant to be displayed within the body (referenced by
	// cid:, or dispositioned inline) rather than offered as downloads.
	Inline bool `json:"inline"`
}

// PartContent is one decoded MIME part, ready to be written to an HTTP
// response.
type PartContent struct {
	FileName    string
	ContentType string
	Content     []byte
}

// SyncMessage is a message header record captured during a sync pass. It
// carries everything the local index stores; bodies are never synced.
type SyncMessage struct {
	UID          uint32
	RFCMessageID string // RFC 5322 Message-ID header, stable across folders
	Subject      string
	FromName     string
	FromEmail    string
	FromDomain   string
	Date         string // RFC3339, empty if the envelope had no usable date
	Seen         bool
	Flagged      bool
	Size         int64
	Snippet      string
}

// RawMessage is a complete, undecoded RFC 5322 message. The index deliberately
// stores only headers and a snippet, so anything that needs a full body — the
// Money product's issuer parsers, and their PDF attachments — asks for it by
// UID at the moment it is needed.
type RawMessage struct {
	UID uint32
	Raw []byte
}

type Provider interface {
	ListMessages(ctx context.Context, folder string, limit, offset int) ([]MessageSummary, error)
	GetMessage(ctx context.Context, folder, id string) (*Message, error)
	// FetchPart returns one decoded MIME part of a message, addressed either by
	// the PartID from Message.Attachments or by a bare Content-ID so cid:
	// references in an HTML body resolve directly.
	FetchPart(ctx context.Context, folder, id, partID string) (*PartContent, error)
	DeleteMessage(ctx context.Context, folder, id string) error
	SetFlag(ctx context.Context, folder, id, flag string, value bool) error
	// MoveMessage relocates a message into destFolder (e.g. archiving it).
	// Implementations should create destFolder if the server doesn't have it yet.
	MoveMessage(ctx context.Context, folder, id, destFolder string) error

	// The bulk forms below exist because IMAP commands take a *set* of UIDs,
	// and looping the single-message calls turns one action into two round
	// trips per message. A sender with a thousand messages then needs a couple
	// of thousand sequential commands, which no HTTP timeout survives — that
	// is what made "Delete all" appear to do nothing.
	//
	// Each returns the ids it actually acted on, so a partial failure is
	// reportable rather than fatal.
	DeleteMessages(ctx context.Context, folder string, ids []string) ([]string, error)
	SetFlags(ctx context.Context, folder string, ids []string, flag string, value bool) ([]string, error)
	MoveMessages(ctx context.Context, folder string, ids []string, destFolder string) ([]string, error)
	// ResolveArchiveFolder returns the folder name archived messages should be
	// moved into, discovering the server's own \Archive mailbox where possible.
	ResolveArchiveFolder(ctx context.Context) (string, error)
	// ResolveTrashFolder returns the server's trash mailbox, or "" if it has
	// none. Deleting moves messages there rather than expunging them: on Gmail
	// an expunge from INBOX only removes the label, leaving the message in All
	// Mail — deleted everywhere except where the user can see it.
	ResolveTrashFolder(ctx context.Context) (string, error)

	// ListUIDs returns every UID currently present in the folder, used to
	// find new messages and to reconcile server-side deletions.
	ListUIDs(ctx context.Context, folder string) ([]uint32, error)
	// FetchForSync fetches header-level data plus a short body snippet for
	// the given UIDs, without marking them as read.
	FetchForSync(ctx context.Context, folder string, uids []uint32) ([]SyncMessage, error)
	// FetchRaw returns whole messages for the given UIDs without marking them
	// as read. Callers should batch: these are full bodies, attachments included.
	FetchRaw(ctx context.Context, folder string, uids []uint32) ([]RawMessage, error)
	// FetchHeaders returns just the raw RFC 5322 header block for one message,
	// without marking it as read — used to read List-Unsubscribe without the
	// cost of pulling the whole body.
	FetchHeaders(ctx context.Context, folder, id string) ([]byte, error)
}

// AccountCredentials is the minimal info a Provider needs to connect.
type AccountCredentials struct {
	Host     string
	Port     int
	Username string
	Password string
	UseTLS   bool
}
