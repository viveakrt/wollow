// Package classifier runs structured AI classification over stored messages
// in the background. Classification happens once per message and is persisted,
// so the UI reads labels from the database instead of calling the model on
// every render.
package classifier

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"wollow/backend/internal/mail/ai"
)

// defaultConcurrency caps simultaneous model calls.
//
// Measured against LM Studio: raising this from 3 to 8 made throughput *worse*
// (9.0/min -> 6.4/min), because a local runtime serves one model serially and
// extra parallel requests only add queueing contention. Cloud providers do
// benefit from higher parallelism, hence WOLLOW_CLASSIFY_CONCURRENCY.
const defaultConcurrency = 3

func concurrencyLimit() int {
	if raw := os.Getenv("WOLLOW_CLASSIFY_CONCURRENCY"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return defaultConcurrency
}

type Status struct {
	Total      int `json:"total"`
	Classified int `json:"classified"`
	Pending    int `json:"pending"`
}

type pendingMessage struct {
	id      int64
	subject string
	from    string
	snippet string
}

// Run classifies every message for an account that has no classification yet.
// It returns the number classified. Individual failures are logged and skipped
// so one bad message never aborts the pass — it is simply retried next run.
func Run(ctx context.Context, database *sql.DB, provider ai.Provider, model string, accountID int64) (int, error) {
	pending, err := loadPending(database, accountID)
	if err != nil {
		return 0, fmt.Errorf("loading unclassified messages: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	var (
		mu       sync.Mutex
		cursor   int
		done     int
		workerWG sync.WaitGroup
	)

	worker := func() {
		defer workerWG.Done()
		for {
			if ctx.Err() != nil {
				return
			}

			mu.Lock()
			if cursor >= len(pending) {
				mu.Unlock()
				return
			}
			msg := pending[cursor]
			cursor++
			mu.Unlock()

			result, err := provider.Classify(ctx, msg.subject, msg.from, msg.snippet)
			if err != nil {
				log.Printf("classifier: message %d failed: %v", msg.id, err)
				continue
			}
			if err := store(database, msg.id, result, model); err != nil {
				log.Printf("classifier: storing message %d failed: %v", msg.id, err)
				continue
			}

			mu.Lock()
			done++
			mu.Unlock()
		}
	}

	n := min(concurrencyLimit(), len(pending))
	workerWG.Add(n)
	for range n {
		go worker()
	}
	workerWG.Wait()

	return done, ctx.Err()
}

// CurrentStatus reports classification progress for an account.
func CurrentStatus(database *sql.DB, accountID int64) (*Status, error) {
	var total, classified int
	err := database.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE account_id = ?`, accountID,
	).Scan(&total)
	if err != nil {
		return nil, err
	}
	err = database.QueryRow(`
		SELECT COUNT(*) FROM classifications c
		JOIN messages m ON m.id = c.message_id
		WHERE m.account_id = ?`, accountID,
	).Scan(&classified)
	if err != nil {
		return nil, err
	}

	return &Status{Total: total, Classified: classified, Pending: total - classified}, nil
}

func loadPending(database *sql.DB, accountID int64) ([]pendingMessage, error) {
	rows, err := database.Query(`
		SELECT m.id, m.subject, m.from_name, m.from_email, m.snippet
		FROM messages m
		LEFT JOIN classifications c ON c.message_id = m.id
		WHERE m.account_id = ? AND c.message_id IS NULL
		ORDER BY m.date DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []pendingMessage
	for rows.Next() {
		var m pendingMessage
		var fromName, fromEmail string
		if err := rows.Scan(&m.id, &m.subject, &fromName, &fromEmail, &m.snippet); err != nil {
			return nil, err
		}
		m.from = fromEmail
		if fromName != "" {
			m.from = fromName + " <" + fromEmail + ">"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func store(database *sql.DB, messageID int64, c *ai.Classification, model string) error {
	_, err := database.Exec(`
		INSERT INTO classifications (
			message_id, category, subcategory, sender_group, priority, action,
			requires_response, requires_payment, has_deadline, deadline,
			is_newsletter, is_promotional, is_transactional, is_security_alert,
			confidence, summary, model, classified_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			category = excluded.category,
			subcategory = excluded.subcategory,
			sender_group = excluded.sender_group,
			priority = excluded.priority,
			action = excluded.action,
			requires_response = excluded.requires_response,
			requires_payment = excluded.requires_payment,
			has_deadline = excluded.has_deadline,
			deadline = excluded.deadline,
			is_newsletter = excluded.is_newsletter,
			is_promotional = excluded.is_promotional,
			is_transactional = excluded.is_transactional,
			is_security_alert = excluded.is_security_alert,
			confidence = excluded.confidence,
			summary = excluded.summary,
			model = excluded.model,
			classified_at = excluded.classified_at
	`,
		messageID, c.Category, c.Subcategory, c.SenderGroup, c.Priority, c.Action,
		boolToInt(c.RequiresResponse), boolToInt(c.RequiresPayment),
		boolToInt(c.HasDeadline), c.Deadline,
		boolToInt(c.IsNewsletter), boolToInt(c.IsPromotional),
		boolToInt(c.IsTransactional), boolToInt(c.IsSecurityAlert),
		c.Confidence, c.Summary, model, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
