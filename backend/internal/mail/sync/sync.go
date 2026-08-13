// Package sync keeps the local message index in step with the IMAP server.
// Only headers and a short snippet are stored; message bodies are always
// fetched live from IMAP when a message is opened.
package sync

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"wollow/backend/internal/mail"
)

// fetchBatch is how many UIDs are requested from IMAP per FETCH command.
const fetchBatch = 500

// refreshWindow is how many of the newest already-synced messages get their
// flags re-read each pass, so read/starred changes made elsewhere show up
// without refetching the whole mailbox.
const refreshWindow = 500

type Result struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Deleted int `json:"deleted"`
	Total   int `json:"total"`
}

// SyncAccount brings the local index for one account+folder up to date.
func SyncAccount(ctx context.Context, database *sql.DB, provider mail.Provider, accountID int64, folder string) (*Result, error) {
	if folder == "" {
		folder = "INBOX"
	}

	serverUIDs, err := provider.ListUIDs(ctx, folder)
	if err != nil {
		return nil, fmt.Errorf("listing uids: %w", err)
	}

	known, err := storedUIDs(database, accountID, folder)
	if err != nil {
		return nil, fmt.Errorf("reading stored uids: %w", err)
	}

	result := &Result{Total: len(serverUIDs)}

	// New messages: on the server but not stored yet.
	var newUIDs []uint32
	for _, uid := range serverUIDs {
		if _, ok := known[uid]; !ok {
			newUIDs = append(newUIDs, uid)
		}
	}

	for start := 0; start < len(newUIDs); start += fetchBatch {
		end := min(start+fetchBatch, len(newUIDs))

		batch, err := provider.FetchForSync(ctx, folder, newUIDs[start:end])
		if err != nil {
			return nil, fmt.Errorf("fetching batch: %w", err)
		}
		if err := upsertMessages(database, accountID, folder, batch); err != nil {
			return nil, fmt.Errorf("storing batch: %w", err)
		}
		result.Added += len(batch)

		if err := ctx.Err(); err != nil {
			return result, err
		}
	}

	// Refresh flags on the newest already-known messages.
	refresh := newestKnown(serverUIDs, known, refreshWindow)
	for start := 0; start < len(refresh); start += fetchBatch {
		end := min(start+fetchBatch, len(refresh))

		batch, err := provider.FetchForSync(ctx, folder, refresh[start:end])
		if err != nil {
			return nil, fmt.Errorf("refreshing flags: %w", err)
		}
		if err := upsertMessages(database, accountID, folder, batch); err != nil {
			return nil, fmt.Errorf("storing refreshed batch: %w", err)
		}
		result.Updated += len(batch)

		if err := ctx.Err(); err != nil {
			return result, err
		}
	}

	// Reconcile deletions: stored but no longer on the server.
	live := make(map[uint32]struct{}, len(serverUIDs))
	for _, uid := range serverUIDs {
		live[uid] = struct{}{}
	}
	var gone []uint32
	for uid := range known {
		if _, ok := live[uid]; !ok {
			gone = append(gone, uid)
		}
	}
	if len(gone) > 0 {
		deleted, err := deleteMessages(database, accountID, folder, gone)
		if err != nil {
			return nil, fmt.Errorf("removing deleted messages: %w", err)
		}
		result.Deleted = deleted
	}

	var highest uint32
	for _, uid := range serverUIDs {
		if uid > highest {
			highest = uid
		}
	}
	if err := saveSyncState(database, accountID, folder, highest); err != nil {
		return nil, fmt.Errorf("saving sync state: %w", err)
	}

	return result, nil
}

func storedUIDs(database *sql.DB, accountID int64, folder string) (map[uint32]struct{}, error) {
	rows, err := database.Query(
		`SELECT uid FROM messages WHERE account_id = ? AND folder = ?`, accountID, folder,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[uint32]struct{})
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out[uid] = struct{}{}
	}
	return out, rows.Err()
}

// newestKnown returns up to limit of the highest already-stored UIDs.
func newestKnown(serverUIDs []uint32, known map[uint32]struct{}, limit int) []uint32 {
	var out []uint32
	for i := len(serverUIDs) - 1; i >= 0 && len(out) < limit; i-- {
		if _, ok := known[serverUIDs[i]]; ok {
			out = append(out, serverUIDs[i])
		}
	}
	return out
}

func upsertMessages(database *sql.DB, accountID int64, folder string, messages []mail.SyncMessage) error {
	if len(messages) == 0 {
		return nil
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO messages (
			account_id, folder, uid, subject, from_name, from_email, from_domain,
			date, seen, flagged, size, snippet, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, folder, uid) DO UPDATE SET
			subject = excluded.subject,
			from_name = excluded.from_name,
			from_email = excluded.from_email,
			from_domain = excluded.from_domain,
			date = excluded.date,
			seen = excluded.seen,
			flagged = excluded.flagged,
			size = excluded.size,
			snippet = CASE WHEN excluded.snippet != '' THEN excluded.snippet ELSE messages.snippet END,
			synced_at = excluded.synced_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, m := range messages {
		if _, err := stmt.Exec(
			accountID, folder, m.UID, m.Subject, m.FromName, m.FromEmail, m.FromDomain,
			m.Date, boolToInt(m.Seen), boolToInt(m.Flagged), m.Size, m.Snippet, now,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func deleteMessages(database *sql.DB, accountID int64, folder string, uids []uint32) (int, error) {
	tx, err := database.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`DELETE FROM messages WHERE account_id = ? AND folder = ? AND uid = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	count := 0
	for _, uid := range uids {
		res, err := stmt.Exec(accountID, folder, uid)
		if err != nil {
			return 0, err
		}
		if n, err := res.RowsAffected(); err == nil {
			count += int(n)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func saveSyncState(database *sql.DB, accountID int64, folder string, lastUID uint32) error {
	_, err := database.Exec(`
		INSERT INTO sync_state (account_id, folder, last_uid, last_synced_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(account_id, folder) DO UPDATE SET
			last_uid = excluded.last_uid,
			last_synced_at = excluded.last_synced_at
	`, accountID, folder, lastUID, time.Now().UTC().Format(time.RFC3339))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
