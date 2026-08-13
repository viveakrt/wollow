package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Allowed enum values. Anything a model returns outside these sets is coerced
// to the default rather than rejected, so one creative answer never fails a
// whole classification pass.
var (
	validCategories = set(
		"work", "jobs_career", "finance", "bills_payments", "shopping",
		"orders_delivery", "travel_booking", "home_services", "personal",
		"education", "health", "legal_government", "marketing_promotions",
		"newsletters", "notifications", "security", "documents",
		"meetings_calendar", "communities_social", "automated_system",
		"spam", "other",
	)
	validPriorities = set("critical", "high", "medium", "low", "noise")
	validActions    = set(
		"no_action", "read_only", "review", "respond", "pay", "book",
		"download", "verify", "follow_up", "archive", "unsubscribe",
	)
	validSenderGroups = set(
		"banking_finance", "shopping", "recruitment", "newsletters",
		"technology", "services", "social", "government", "health",
		"education", "travel", "other",
	)
)

const (
	defaultCategory    = "other"
	defaultPriority    = "medium"
	defaultAction      = "no_action"
	defaultSenderGroup = "other"
)

const classifyPromptTemplate = `You are an email classifier. Analyse the email and respond with a single JSON object and nothing else. No prose, no markdown fences.

Use exactly these fields:
{
  "category": one of [work, jobs_career, finance, bills_payments, shopping, orders_delivery, travel_booking, home_services, personal, education, health, legal_government, marketing_promotions, newsletters, notifications, security, documents, meetings_calendar, communities_social, automated_system, spam, other],
  "subcategory": short free-form label (1-3 words) or "",
  "sender_group": one of [banking_finance, shopping, recruitment, newsletters, technology, services, social, government, health, education, travel, other],
  "priority": one of [critical, high, medium, low, noise],
  "action": one of [no_action, read_only, review, respond, pay, book, download, verify, follow_up, archive, unsubscribe],
  "requires_response": true/false,
  "requires_payment": true/false,
  "has_deadline": true/false,
  "deadline": ISO 8601 date or "" if none,
  "is_newsletter": true/false,
  "is_promotional": true/false,
  "is_transactional": true/false,
  "is_security_alert": true/false,
  "confidence": number between 0 and 1,
  "summary": one short sentence (max 20 words)
}

Guidance: "critical" is for things with real consequences if ignored (fraud, security breach, legal notice, payment failure, account suspension). "noise" is for repetitive or worthless mail. Only set requires_response when a human genuinely needs to reply.

Email:
From: %s
Subject: %s
Body: %s`

// parseClassification extracts and validates a Classification from raw model
// output. It tolerates markdown fences and surrounding prose, but refuses to
// invent data: if no JSON object can be found it returns an error so the
// message stays unclassified and is retried on a later pass.
func parseClassification(raw string) (*Classification, error) {
	jsonText, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}

	var c Classification
	if err := json.Unmarshal([]byte(jsonText), &c); err != nil {
		return nil, fmt.Errorf("classification: invalid JSON: %w", err)
	}

	c.Category = coerce(c.Category, validCategories, defaultCategory)
	c.Priority = coerce(c.Priority, validPriorities, defaultPriority)
	c.Action = coerce(c.Action, validActions, defaultAction)
	c.SenderGroup = coerce(c.SenderGroup, validSenderGroups, defaultSenderGroup)

	c.Subcategory = strings.TrimSpace(c.Subcategory)
	c.Summary = strings.TrimSpace(c.Summary)
	c.Deadline = strings.TrimSpace(c.Deadline)
	if c.Deadline == "" {
		c.HasDeadline = false
	}

	switch {
	case c.Confidence < 0:
		c.Confidence = 0
	case c.Confidence > 1:
		c.Confidence = 1
	}

	return &c, nil
}

// extractJSONObject pulls the first balanced JSON object out of model output,
// stripping markdown fences and any reasoning text around it.
func extractJSONObject(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("classification: empty model response")
	}

	if fence := strings.Index(text, "```"); fence != -1 {
		rest := text[fence+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimPrefix(rest, "JSON")
		if end := strings.Index(rest, "```"); end != -1 {
			rest = rest[:end]
		}
		text = strings.TrimSpace(rest)
	}

	start := strings.Index(text, "{")
	if start == -1 {
		return "", fmt.Errorf("classification: no JSON object in response: %s", truncate(text, 200))
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// skip structural characters inside strings
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return text[start : i+1], nil
			}
		}
	}

	return "", fmt.Errorf("classification: unterminated JSON object: %s", truncate(text[start:], 200))
}

func set(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}

func coerce(value string, allowed map[string]struct{}, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if _, ok := allowed[normalized]; ok {
		return normalized
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// buildClassifyPrompt assembles the classification prompt, trimming the body
// snippet so a huge message can't blow past the model's context.
func buildClassifyPrompt(subject, from, snippet string) string {
	if len(snippet) > MaxBodyPreviewChars {
		snippet = snippet[:MaxBodyPreviewChars]
	}
	return fmt.Sprintf(classifyPromptTemplate, from, subject, snippet)
}
