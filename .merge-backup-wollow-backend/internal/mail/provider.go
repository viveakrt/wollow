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
	BodyText string `json:"bodyText"`
	BodyHTML string `json:"bodyHtml"`
}

// SyncMessage is a message header record captured during a sync pass. It
// carries everything the local index stores; bodies are never synced.
type SyncMessage struct {
	UID        uint32
	Subject    string
	FromName   string
	FromEmail  string
	FromDomain string
	Date       string // RFC3339, empty if the envelope had no usable date
	Seen       bool
	Flagged    bool
	Size       int64
	Snippet    string
}

type Provider interface {
	ListMessages(ctx context.Context, folder string, limit, offset int) ([]MessageSummary, error)
	GetMessage(ctx context.Context, folder, id string) (*Message, error)
	DeleteMessage(ctx context.Context, folder, id string) error
	SetFlag(ctx context.Context, folder, id, flag string, value bool) error

	// ListUIDs returns every UID currently present in the folder, used to
	// find new messages and to reconcile server-side deletions.
	ListUIDs(ctx context.Context, folder string) ([]uint32, error)
	// FetchForSync fetches header-level data plus a short body snippet for
	// the given UIDs, without marking them as read.
	FetchForSync(ctx context.Context, folder string, uids []uint32) ([]SyncMessage, error)
}

// AccountCredentials is the minimal info a Provider needs to connect.
type AccountCredentials struct {
	Host     string
	Port     int
	Username string
	Password string
	UseTLS   bool
}
