package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAnthropicModel = "claude-sonnet-5"

type AnthropicProvider struct {
	APIKey string
	Model  string
	client *http.Client
}

func NewAnthropicProvider(apiKey, model string) *AnthropicProvider {
	if model == "" {
		model = defaultAnthropicModel
	}
	return &AnthropicProvider{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// complete runs one message request and returns the assistant text.
func (p *AnthropicProvider) complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	reqBody := anthropicRequest{
		Model:     p.Model,
		MaxTokens: maxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("anthropic: unexpected response: %s", string(body))
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("anthropic error: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(parsed.Content) == 0 {
		return "", fmt.Errorf("anthropic: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	// Extended thinking emits non-text blocks first; take the first text block.
	for _, block := range parsed.Content {
		if block.Text != "" {
			return strings.TrimSpace(block.Text), nil
		}
	}
	return "", fmt.Errorf("anthropic: response contained no text block")
}

func (p *AnthropicProvider) Complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	return p.complete(ctx, prompt, maxTokens)
}

func (p *AnthropicProvider) Summarize(ctx context.Context, subject, bodyPreview string) (string, error) {
	if len(bodyPreview) > MaxBodyPreviewChars {
		bodyPreview = bodyPreview[:MaxBodyPreviewChars]
	}
	return p.complete(ctx, fmt.Sprintf(summarizePromptTemplate, subject, bodyPreview), summarizeMaxTokens)
}

func (p *AnthropicProvider) Classify(ctx context.Context, subject, from, snippet string) (*Classification, error) {
	raw, err := p.complete(ctx, buildClassifyPrompt(subject, from, snippet), classifyMaxTokens)
	if err != nil {
		return nil, err
	}
	return parseClassification(raw)
}
