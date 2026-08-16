// Package ledger holds the Money product's persistence primitives — the rules
// for keeping account balances correct and for attaching an incoming
// transaction to the right account. It is deliberately free of HTTP and of
// IMAP: both the statement importer (moneyapi) and the mail ingester (ingest)
// write through it, and they must agree on these rules exactly.
package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"wollow/backend/internal/money/models"
)

// Execer is satisfied by both *sql.DB and *sql.Tx, so callers can run these
// inside a larger transaction (the importer does) or standalone (ingest does).
type Execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// Queryer adds the reads RecomputeAccountBalance needs. Both *sql.DB and
// *sql.Tx satisfy it, which is what lets the importer recompute inside its
// own transaction and see its own uncommitted rows.
type Queryer interface {
	Execer
	QueryRow(query string, args ...interface{}) *sql.Row
}

// balanceAnchor is a point at which the account's balance is known for certain,
// plus enough ordering information to sum only what came after it.
type balanceAnchor struct {
	balance float64
	date    string
	// txnID orders within a single date for transaction-derived anchors. A
	// snapshot anchor leaves it at 0 and counts every transaction dated after
	// its day, because a snapshot carries no time of day to break ties with.
	txnID    int64
	fromTxn  bool
	resolved bool
}

// RecomputeAccountBalance refreshes an account's opening and current balance
// from the strongest evidence available.
//
// current_balance is anchored on whichever is *most recent*: a bank-reported
// balance snapshot, or the closing balance printed against a transaction. Every
// transaction after that anchor is then applied. Anchoring on the latest fact
// rather than summing from the beginning is what makes the figure survive a
// partial history — an account whose statements only go back a year, or one
// known solely from alert emails, still shows a balance the bank agrees with.
//
// opening_balance is derived from the *earliest* known closing balance
// (closing - deposit + withdrawal), which self-corrects when statements are
// imported out of chronological order.
func RecomputeAccountBalance(db Queryer, accountID int64) error {
	anchor := latestAnchor(db, accountID)

	var current float64
	if anchor.resolved {
		current = anchor.balance + deltaAfter(db, accountID, anchor)
	} else {
		// Nothing authoritative to anchor on: fall back to the stored opening
		// balance plus every movement recorded.
		var opening float64
		if err := db.QueryRow(
			`SELECT opening_balance FROM finance_accounts WHERE id = ?`, accountID,
		).Scan(&opening); err != nil {
			return fmt.Errorf("reading opening balance: %w", err)
		}
		current = opening + deltaAfter(db, accountID, balanceAnchor{})
	}

	_, err := db.Exec(`
		UPDATE finance_accounts SET
			opening_balance = COALESCE((
				SELECT closing_balance - deposit_amt + withdrawal_amt
				FROM transactions
				WHERE account_id = ? AND closing_balance IS NOT NULL
				ORDER BY txn_date ASC, id ASC
				LIMIT 1
			), opening_balance),
			current_balance = ?,
			updated_at = datetime('now')
		WHERE id = ?`, accountID, current, accountID)
	if err != nil {
		return fmt.Errorf("updating account %d balance: %w", accountID, err)
	}
	return nil
}

// latestAnchor picks the more recent of the newest balance snapshot and the
// newest transaction carrying a closing balance.
func latestAnchor(db Queryer, accountID int64) balanceAnchor {
	var fromTxn balanceAnchor
	if err := db.QueryRow(`
		SELECT closing_balance, txn_date, id
		FROM transactions
		WHERE account_id = ? AND closing_balance IS NOT NULL
		ORDER BY txn_date DESC, id DESC
		LIMIT 1`, accountID,
	).Scan(&fromTxn.balance, &fromTxn.date, &fromTxn.txnID); err == nil {
		fromTxn.fromTxn = true
		fromTxn.resolved = true
	}

	var fromSnapshot balanceAnchor
	if err := db.QueryRow(`
		SELECT balance, as_of
		FROM balance_snapshots
		WHERE account_id = ?
		ORDER BY as_of DESC, id DESC
		LIMIT 1`, accountID,
	).Scan(&fromSnapshot.balance, &fromSnapshot.date); err == nil {
		fromSnapshot.resolved = true
	}

	switch {
	case !fromSnapshot.resolved:
		return fromTxn
	case !fromTxn.resolved:
		return fromSnapshot
	case fromSnapshot.date >= fromTxn.date:
		// Ties go to the snapshot: a balance the bank stated for a day already
		// includes that day's transactions.
		return fromSnapshot
	default:
		return fromTxn
	}
}

// deltaAfter sums the net movement of transactions recorded after the anchor.
// A zero-value anchor sums the whole account.
func deltaAfter(db Queryer, accountID int64, anchor balanceAnchor) float64 {
	var (
		delta float64
		err   error
	)
	switch {
	case !anchor.resolved:
		err = db.QueryRow(`
			SELECT COALESCE(SUM(deposit_amt - withdrawal_amt), 0)
			FROM transactions WHERE account_id = ?`, accountID).Scan(&delta)
	case anchor.fromTxn:
		err = db.QueryRow(`
			SELECT COALESCE(SUM(deposit_amt - withdrawal_amt), 0)
			FROM transactions
			WHERE account_id = ? AND (txn_date > ? OR (txn_date = ? AND id > ?))`,
			accountID, anchor.date, anchor.date, anchor.txnID).Scan(&delta)
	default:
		err = db.QueryRow(`
			SELECT COALESCE(SUM(deposit_amt - withdrawal_amt), 0)
			FROM transactions WHERE account_id = ? AND txn_date > ?`,
			accountID, anchor.date).Scan(&delta)
	}
	if err != nil {
		return 0
	}
	return delta
}

// MatchAccountByLast4 finds an existing finance account whose stored
// account_number ends with the given last-4 digits. Returns 0 if there is no
// match, or if last4 is empty.
//
// Prefer ResolveAccount for anything coming out of an email: this ignores the
// institution, so four digits shared by two accounts resolve to whichever was
// created first.
func MatchAccountByLast4(db *sql.DB, last4 string) int64 {
	if last4 == "" {
		return 0
	}
	var id int64
	if err := db.QueryRow(
		`SELECT id FROM finance_accounts WHERE account_number LIKE '%' || ? ORDER BY id LIMIT 1`, last4,
	).Scan(&id); err != nil {
		return 0
	}
	return id
}

// EmailDedupeHash is the uniqueness key for a transaction extracted from an
// alert email. It must stay stable: it is stored in transactions.dedupe_hash
// and backed by a unique index, so changing the formula would let previously
// imported alerts reappear as duplicates.
func EmailDedupeHash(accountID int64, t *models.ParsedEmailTransaction) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%.2f|%s|%s", accountID, t.TxnDate, t.Amount, t.RefNo, t.Narration)
	return hex.EncodeToString(h.Sum(nil))
}

// NullIfZero maps a zero amount to SQL NULL, so "no closing balance reported"
// is distinguishable from "closing balance is zero".
func NullIfZero(f float64) interface{} {
	if f == 0 {
		return nil
	}
	return f
}

// NullIfZeroID maps an unset row id to SQL NULL for nullable foreign keys.
func NullIfZeroID(id int64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}
