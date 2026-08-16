package ai

import (
	"context"
	"errors"
)

// NoopProvider is used when the user has not configured an AI provider yet.
type NoopProvider struct{}

var ErrNotConfigured = errors.New("no AI provider configured; set one in Settings")

func (NoopProvider) Summarize(ctx context.Context, subject, bodyPreview string) (string, error) {
	return "", ErrNotConfigured
}

func (NoopProvider) Classify(ctx context.Context, subject, from, snippet string) (*Classification, error) {
	return nil, ErrNotConfigured
}

func (NoopProvider) Complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	return "", ErrNotConfigured
}
