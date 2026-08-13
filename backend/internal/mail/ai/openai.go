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

const defaultOpenAIModel = "gpt-4o-mini"

// OpenAIProvider also works for any OpenAI-compatible chat completions API
// (LM Studio, Ollama, proxies) by overriding BaseURL.
type OpenAIProvider struct {
	APIKey  string
	Model   string
	BaseURL string
	client  *http.Client
}

func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = defaultOpenAIModel
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: strings.TrimRight(baseURL, "/"),
		// Local models can be slow, especially reasoning ones.
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			openAIMessage
			// ReasoningContent is populated by some reasoning-model servers
			// (e.g. LM Studio with local reasoning models) that separate
			// "thinking" text from the final answer. Used only as a fallback
			// when Content came back empty.
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// complete runs one chat completion and returns the assistant text. If the
// model produced only reasoning tokens and no answer, the reasoning text is
// returned instead of an empty string.
func (p *OpenAIProvider) complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	reqBody := openAIRequest{
		Model:     p.Model,
		MaxTokens: maxTokens,
		Messages:  []openAIMessage{{Role: "user", Content: prompt}},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed openAIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("openai: unexpected response: %s", string(body))
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("openai error: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(parsed.Choices[0].Message.ReasoningContent)
	}
	return content, nil
}

func (p *OpenAIProvider) Summarize(ctx context.Context, subject, bodyPreview string) (string, error) {
	if len(bodyPreview) > MaxBodyPreviewChars {
		bodyPreview = bodyPreview[:MaxBodyPreviewChars]
	}
	return p.complete(ctx, fmt.Sprintf(summarizePromptTemplate, subject, bodyPreview), summarizeMaxTokens)
}

func (p *OpenAIProvider) Classify(ctx context.Context, subject, from, snippet string) (*Classification, error) {
	raw, err := p.complete(ctx, buildClassifyPrompt(subject, from, snippet), classifyMaxTokens)
	if err != nil {
		return nil, err
	}
	return parseClassification(raw)
}
