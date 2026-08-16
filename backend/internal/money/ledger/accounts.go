package ledger

import (
	"database/sql"
	"fmt"
	"strings"
)

// Attaching an alert to the right finance account.
//
// The rule that matters: an account is identified by its *last four digits
// together with the institution that sent the mail*. Last-4 alone collides —
// four digits across a dozen accounts is a coin flip, and a mis-attached
// transaction silently corrupts two balances at once.

// AccountHint is what an alert email told us about the account it concerns.
type AccountHint struct {
	// Issuer is the short institution code ("HDFC"), Name its display form
	// ("HDFC Bank"). Issuer is what gets stored in finance_accounts.bank.
	Issuer string
	Name   string
	Last4  string
	// Kind is the finance_accounts.account_type this alert implies: bank,
	// credit_card, wallet, investment or loan.
	Kind     string
	Currency string
}

// placeholderKind is the type an email-created account gets when the alert
// didn't say what kind of account it was. It is the only type ResolveAccount
// will overwrite later, once a clearer alert arrives.
const placeholderKind = "bank"

// MatchAccount finds the finance account an alert belongs to WITHOUT creating
// one, returning 0 when nothing registered matches.
//
// This is what email ingest uses, and the restraint is the point. Inventing an
// account from whatever alert happens to arrive gets two things wrong that are
// expensive to undo: a mailbox full of receipts becomes a list of accounts the
// user never opened, and — worse — one card-shaped alert can define a savings
// account as a credit card, which then reports its balance as debt.
//
// The user adds accounts themselves instead. Mail naming an account that does
// not exist is held, not guessed at, and attaches on a later pass once they
// have added it — see the pending_account outcome in the ingest package.
func MatchAccount(db *sql.DB, hint AccountHint) int64 {
	return matchExisting(db, normalizeHint(hint))
}

// CreateApprovedAccount records an account a person has agreed to, and is the
// only way an account is created from mail evidence.
//
// source is 'manual' because a human chose this account's type in the approval
// dialog. Recording it as 'email' used to be how a savings account got
// relabelled a credit card: the type was then treated as a guess that a later
// card-shaped alert could overwrite.
func CreateApprovedAccount(db *sql.DB, hint AccountHint) int64 {
	return createAccount(db, normalizeHint(hint), "manual")
}

func normalizeHint(hint AccountHint) AccountHint {
	hint.Issuer = strings.TrimSpace(hint.Issuer)
	hint.Last4 = strings.TrimSpace(hint.Last4)
	if hint.Kind == "" {
		hint.Kind = placeholderKind
	}
	if hint.Currency == "" {
		hint.Currency = "INR"
	}
	if hint.Name == "" {
		hint.Name = hint.Issuer
	}
	return hint
}

// bankMatches accepts the several spellings the same institution legitimately
// has on an account row.
//
// Accounts are hand-entered now, so finance_accounts.bank holds whatever the
// user chose: the issuer code the parsers use ("HDFC"), the display name they
// picked from the list ("HDFC Bank"), or nothing at all if they skipped the
// field. Insisting on the issuer code alone would silently strand an account's
// mail forever — it would parse, find no match, and wait for an account that
// already exists.
const bankMatches = `(LOWER(bank) = LOWER(?) OR LOWER(bank) = LOWER(?) OR bank = '')`

// matchExisting tries progressively weaker identifications, strongest first.
func matchExisting(db *sql.DB, hint AccountHint) int64 {
	if hint.Last4 != "" {
		// Same digits, same bank, same kind — the only unambiguous match.
		if id := queryID(db, `
			SELECT id FROM finance_accounts
			WHERE account_number LIKE '%' || ? AND `+bankMatches+` AND account_type = ?
			ORDER BY id LIMIT 1`, hint.Last4, hint.Issuer, hint.Name, hint.Kind); id != 0 {
			return id
		}
		// Same digits and bank, different recorded kind: still the same
		// account — the user may have typed a type we disagree with, and their
		// choice wins over our guess.
		if id := queryID(db, `
			SELECT id FROM finance_accounts
			WHERE account_number LIKE '%' || ? AND `+bankMatches+`
			ORDER BY id LIMIT 1`, hint.Last4, hint.Issuer, hint.Name); id != 0 {
			return id
		}
		return 0
	}

	// No digits at all: fall back to one account per (institution, kind), which
	// is right for wallets — Amazon Pay has one balance, not a numbered one.
	// An empty issuer would match every unnamed account, so it is refused.
	if hint.Issuer == "" {
		return 0
	}
	return queryID(db, `
		SELECT id FROM finance_accounts
		WHERE (LOWER(bank) = LOWER(?) OR LOWER(bank) = LOWER(?))
		  AND account_type = ? AND account_number = ''
		ORDER BY id LIMIT 1`, hint.Issuer, hint.Name, hint.Kind)
}

// Account types are never rewritten from mail any more.
//
// There used to be a reconcileKind step that "upgraded" an account still
// carrying the placeholder type when a clearer alert arrived. It could not
// work: the placeholder was "bank", which is also a real type, so it could not
// tell "we don't know yet" from "this is a savings account". One card-shaped
// alert naming the same digits was enough to relabel a salary account as a
// credit card — and a credit card's balance reads as debt, so net worth moved
// by the account's whole balance. The type a person chose now stands until
// they change it.

func createAccount(db *sql.DB, hint AccountHint, source string) int64 {
	res, err := db.Exec(`
		INSERT INTO finance_accounts
			(name, bank, account_type, account_number, currency, source)
		VALUES (?, ?, ?, ?, ?, ?)`,
		accountDisplayName(hint), hint.Issuer, hint.Kind, maskedNumber(hint.Last4),
		hint.Currency, source)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// accountDisplayName names a discovered account the way a bank statement would
// — "HDFC Bank •• 4125" — so it is recognizable in a list before the user has
// renamed it.
func accountDisplayName(hint AccountHint) string {
	label := hint.Name
	if label == "" {
		label = "Unknown"
	}
	if hint.Last4 == "" {
		return label
	}
	return label + " •• " + hint.Last4
}

// maskedNumber stores a discovered account's digits in the same masked shape
// statements use, so MatchAccountByLast4's suffix match works against accounts
// from either source.
func maskedNumber(last4 string) string {
	if last4 == "" {
		return ""
	}
	return "XXXXXXXX" + last4
}

func queryID(db *sql.DB, query string, args ...any) int64 {
	var id int64
	if err := db.QueryRow(query, args...).Scan(&id); err != nil {
		return 0
	}
	return id
}

// RecordBalanceSnapshot stores a bank-reported balance for an account.
//
// Snapshots are the strongest balance evidence there is, so RecomputeBalance
// anchors on the most recent one and derives everything after it from
// transactions. Re-reporting the same day replaces the earlier figure rather
// than accumulating rows.
func RecordBalanceSnapshot(db *sql.DB, accountID int64, asOf string, balance float64, source string) error {
	if accountID == 0 || asOf == "" {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO balance_snapshots (account_id, as_of, balance, source)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(account_id, as_of) DO UPDATE SET
			balance = excluded.balance,
			source = excluded.source`,
		accountID, asOf, balance, source)
	if err != nil {
		return fmt.Errorf("recording balance snapshot: %w", err)
	}
	return RecomputeAccountBalance(db, accountID)
}

// RecordCreditLimit stores the sanctioned limit a card spend alert reported.
// Zero is ignored: alerts that omit the limit must not wipe a known one.
func RecordCreditLimit(db Execer, accountID int64, limit float64) {
	if accountID == 0 || limit <= 0 {
		return
	}
	db.Exec(`
		UPDATE finance_accounts SET credit_limit = ?, updated_at = datetime('now')
		WHERE id = ?`, limit, accountID)
}
