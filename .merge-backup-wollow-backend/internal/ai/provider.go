// Package ai defines a pluggable interface for AI-driven inbox features
// (summarization and structured classification). Users bring their own API
// key via Settings; noop.go is used when no provider is configured.
package ai

import "context"

// Classification is the structured result of analysing one message. It is
// stored per message and drives categories, smart views, priorities and
// suggested actions across the app.
//
// There is deliberately no "state" field: read/unread/starred are derived
// from live IMAP flags, so a model can never invent mailbox state.
type Classification struct {
	Category         string  `json:"category"`
	Subcategory      string  `json:"subcategory"`
	SenderGroup      string  `json:"sender_group"`
	Priority         string  `json:"priority"`
	Action           string  `json:"action"`
	RequiresResponse bool    `json:"requires_response"`
	RequiresPayment  bool    `json:"requires_payment"`
	HasDeadline      bool    `json:"has_deadline"`
	Deadline         string  `json:"deadline"`
	IsNewsletter     bool    `json:"is_newsletter"`
	IsPromotional    bool    `json:"is_promotional"`
	IsTransactional  bool    `json:"is_transactional"`
	IsSecurityAlert  bool    `json:"is_security_alert"`
	Confidence       float64 `json:"confidence"`
	Summary          string  `json:"summary"`
}

type Provider interface {
	Summarize(ctx context.Context, subject, bodyPreview string) (string, error)
	Classify(ctx context.Context, subject, from, snippet string) (*Classification, error)
}

const MaxBodyPreviewChars = 4000

// Token budgets are generous enough to cover reasoning models (e.g. local
// LM Studio models like Nemotron), which spend a chunk of the budget on
// hidden reasoning tokens before writing the actual answer. A plain
// non-reasoning model just stops early and costs nothing extra.
const summarizeMaxTokens = 400
const classifyMaxTokens = 800

const summarizePromptTemplate = "Summarize this email in one short sentence (max 20 words), plain text, no preamble.\n\nSubject: %s\n\nBody:\n%s"
