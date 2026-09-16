// Package ingest turns finance mail already sitting in the shared message
// index into transactions, bills, balances and trades, by applying the parser
// rules the user defined (money/parserules).
//
// It does not talk to IMAP. Mail's sync pass is the single ingestion pipeline
// for the whole app; this package reads what that pass indexed, asks the
// provider for the raw bodies of just the messages it cares about, and writes
// the results back with a message_links row joining each message to whatever
// Money made of it.
//
// It never creates accounts either. A message a rule reads but whose account
// nobody has registered is linked as pending_account, with what the message
// said about the account, so the user can add exactly that account — after
// which the held messages are read again.
package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/money/parserules"
)

// batchSizeForTest caps how many messages one pass pulls bodies for. A first
// sync of a long-lived mailbox can match thousands; fetching them all in one go
// would hold a large amount of raw mail (attachments included) in memory at
// once. It is a variable only so a test can shrink it and put a failing batch
// on its own; nothing else may write to it.
var batchSizeForTest = 200

type Result struct {
	Scanned      int `json:"scanned"`
	Transactions int `json:"transactions"`
	Bills        int `json:"bills"`
	Balances     int `json:"balances"`
	// Trades counts broker order confirmations turned into holdings.
	Trades int `json:"trades"`
	// PendingAccount counts messages a rule read whose account nobody has
	// registered yet. They are held, listed for the user, and read again once
	// the account exists.
	PendingAccount int `json:"pendingAccount"`
	Unrecognized   int `json:"unrecognized"`
	Duplicates     int `json:"duplicates"`
	// Failed counts messages this pass could not fetch or record. They stay
	// unlinked and are retried next pass. It is reported rather than returned
	// as an error because the rest of the pass still did useful work.
	Failed int `json:"failed"`
}

// RawFetcher is the slice of mail.Provider that ingest needs. Narrowing it here
// keeps the dependency one-way — Money depends on a capability, not on Mail's
// whole provider surface — and makes the package trivially testable.
type RawFetcher interface {
	FetchRaw(ctx context.Context, folder string, uids []uint32) ([]mail.RawMessage, error)
}

// candidate is one indexed message that looks like finance mail.
type candidate struct {
	messageID    int64
	uid          uint32
	rfcMessageID string
	fromEmail    string
}

// Run processes every not-yet-examined finance message for one mailbox.
//
// A message qualifies if it is from a known issuer domain, from a sender one
// of the user's rules names, or if the AI classifier flagged it as
// transactional. The last two arms are the point of reading the index: they
// surface senders the registry has never heard of, which then either match a
// rule or land as 'unrecognized' — visible, rather than silently skipped.
func Run(ctx context.Context, db *sql.DB, fetcher RawFetcher, accountID int64, folder string) (*Result, error) {
	if folder == "" {
		folder = "INBOX"
	}

	rules, err := loadRules(db)
	if err != nil {
		return nil, fmt.Errorf("loading parser rules: %w", err)
	}
	candidates, err := selectCandidates(db, accountID, folder, rules)
	if err != nil {
		return nil, fmt.Errorf("selecting finance mail: %w", err)
	}

	result := &Result{}
	if len(candidates) == 0 {
		return result, nil
	}

	for start := 0; start < len(candidates); start += batchSizeForTest {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		end := min(start+batchSizeForTest, len(candidates))
		batch := candidates[start:end]

		uids := make([]uint32, 0, len(batch))
		for _, c := range batch {
			uids = append(uids, c.uid)
		}

		raws, err := fetcher.FetchRaw(ctx, folder, uids)
		if err != nil {
			// One unreadable batch must not end the pass.
			//
			// Messages that failed stay unlinked so they can be retried, which
			// means they stay at the front of this UID-ordered queue. Aborting
			// here therefore did not merely skip a batch — it starved every
			// message behind it on every subsequent run, permanently. Mail kept
			// arriving and nothing was ever imported again.
			log.Printf("ingest: fetching %d bodies at uid %d failed, skipping batch: %v",
				len(uids), uids[0], err)
			result.Failed += len(uids)
			continue
		}
		byUID := make(map[uint32][]byte, len(raws))
		for _, r := range raws {
			byUID[r.UID] = r.Raw
		}

		for _, c := range batch {
			raw, ok := byUID[c.uid]
			if !ok {
				// Message vanished between sync and this fetch. The next pass
				// will either find it again or the index will have dropped it.
				continue
			}
			result.Scanned++
			if err := processOne(db, accountID, c, raw, result, rules); err != nil {
				// Same reasoning as a failed fetch: one message that cannot be
				// recorded must not stop the ones behind it.
				log.Printf("ingest: recording message %d failed: %v", c.messageID, err)
				result.Failed++
			}
		}
	}

	return result, nil
}

// loadRules prepares every enabled rule. A rule that no longer compiles is
// skipped and logged rather than failing the pass: one broken rule must not
// stop every other sender's mail.
func loadRules(db *sql.DB) ([]*parserules.Compiled, error) {
	rules, err := parserules.LoadEnabled(db)
	if err != nil {
		return nil, err
	}
	compiled, errs := parserules.CompileAll(rules)
	for _, e := range errs {
		log.Printf("ingest: skipping parser rule: %v", e)
	}
	return compiled, nil
}

func processOne(db *sql.DB, accountID int64, c candidate, raw []byte, result *Result, rules []*parserules.Compiled) error {
	parsed, err := emailparse.ParseEML(raw)
	if err != nil {
		return nil // unparseable MIME; leave it unlinked so a later fix can retry
	}

	// Prefer the header the index recorded; fall back to the body's own.
	rfcID := c.rfcMessageID
	if rfcID == "" {
		rfcID = parsed.MessageID
	}

	outcome := Persist(db, rules, parsed)
	var investmentID *int64
	if outcome.InvestmentID != 0 {
		investmentID = &outcome.InvestmentID
	}
	pending := PendingHint{}
	if outcome.Pending != nil {
		pending = *outcome.Pending
	}

	switch outcome.ParsedAs {
	case "transaction":
		result.Transactions++
	case "bill":
		result.Bills++
	case "balance":
		result.Balances++
	case "trade":
		result.Trades++
	case "pending_account":
		result.PendingAccount++
	default:
		result.Unrecognized++
	}

	// The unique index on (mail_account_id, rfc_message_id) is the real guard
	// against double-import; a conflict here means another pass got there first.
	res, err := db.Exec(`
		INSERT INTO message_links
			(mail_account_id, message_id, rfc_message_id, uid, sender, subject,
			 received_at, parsed_as, transaction_id, bill_id, investment_id, rule_id,
			 pending_issuer, pending_name, pending_last4, pending_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mail_account_id, rfc_message_id) DO NOTHING`,
		accountID, c.messageID, rfcID, c.uid, parsed.From, parsed.Subject,
		parsed.Date, outcome.ParsedAs, outcome.TransactionID, outcome.BillID, investmentID,
		ledger.NullIfZeroID(outcome.RuleID),
		pending.Issuer, pending.Name, pending.Last4, pending.Kind)
	if err != nil {
		return fmt.Errorf("linking message %d: %w", c.messageID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		result.Duplicates++
		return nil
	}
	if outcome.RuleID != 0 {
		parserules.RecordMatch(db, outcome.RuleID)
	}
	return nil
}

// selectCandidates returns indexed messages for this mailbox that Money has not
// linked yet and that look like finance mail.
func selectCandidates(db *sql.DB, accountID int64, folder string, rules []*parserules.Compiled) ([]candidate, error) {
	args := []interface{}{accountID, folder}

	// Each domain is matched exactly *and* as a parent of the sender's domain,
	// because banks send from alerts.<bank>.com as readily as from <bank>.com
	// and an exact-only IN clause missed every one of those.
	seen := map[string]bool{}
	var clauses []string
	addDomain := func(domain string) {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || seen[domain] {
			return
		}
		seen[domain] = true
		clauses = append(clauses, "(m.from_domain = ? OR m.from_domain LIKE ?)")
		args = append(args, domain, "%."+domain)
	}
	for _, domain := range emailparse.AllowedSenderDomains {
		addDomain(domain)
	}
	plain := make([]*parserules.Rule, 0, len(rules))
	for _, c := range rules {
		plain = append(plain, c.Rule)
	}
	domains, emails := parserules.Senders(plain)
	for _, domain := range domains {
		addDomain(domain)
	}
	for _, email := range emails {
		clauses = append(clauses, "LOWER(m.from_email) = ?")
		args = append(args, email)
	}

	// Two exclusions, deliberately both present: message_id catches anything
	// this path already linked, and rfc_message_id catches rows written by the
	// pre-merge importer, which never knew the index's row ids.
	query := fmt.Sprintf(`
		SELECT m.id, m.uid, m.rfc_message_id, m.from_email
		FROM messages m
		LEFT JOIN classifications c ON c.message_id = m.id
		WHERE m.account_id = ?
		  AND m.folder = ?
		  AND ((%s) OR c.is_transactional = 1)
		  AND NOT EXISTS (
		        SELECT 1 FROM message_links l WHERE l.message_id = m.id
		  )
		  AND NOT EXISTS (
		        SELECT 1 FROM message_links l
		        WHERE l.mail_account_id = m.account_id
		          AND m.rfc_message_id != ''
		          AND l.rfc_message_id = m.rfc_message_id
		  )
		ORDER BY m.uid`, strings.Join(clauses, " OR "))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.messageID, &c.uid, &c.rfcMessageID, &c.fromEmail); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RescanStuck clears message_links rows that deserve another pass through the
// parsers, and returns how many it cleared.
//
// Once a message is linked, selectCandidates never looks at it again. That is
// right for a message that became a real row, and wrong for three kinds of
// row: a link whose transaction/bill/holding was deleted from under it
// (recreating the account should bring the mail back), a message held for an
// account that has since been added, and a message nobody had a rule for when
// it arrived. Only those are cleared — never a link still pointing at a real
// row, so a rescan cannot relabel or duplicate anything already correct.
func RescanStuck(db *sql.DB, mailAccountID int64) (int64, error) {
	res, err := db.Exec(`
		DELETE FROM message_links
		WHERE mail_account_id = ?
		  AND ((parsed_as = 'transaction' AND transaction_id IS NULL)
		    OR (parsed_as = 'bill' AND bill_id IS NULL)
		    OR (parsed_as = 'trade' AND investment_id IS NULL)
		    OR parsed_as = 'pending_account'
		    OR parsed_as = 'unrecognized')`,
		mailAccountID)
	if err != nil {
		return 0, fmt.Errorf("clearing stuck links: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ClearPendingFor releases the messages held for an account that now exists,
// so the next sync reads them. Digits identify an account when the mail
// stated them; a digit-less hint (a wallet) is matched on the institution,
// with the same tolerance ledger.MatchAccount has for how the user typed it.
func ClearPendingFor(db *sql.DB, bank, last4 string) (int64, error) {
	var total int64
	if last4 != "" {
		res, err := db.Exec(`
			DELETE FROM message_links WHERE parsed_as = 'pending_account' AND pending_last4 = ?`, last4)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}

	bank = strings.ToLower(strings.TrimSpace(bank))
	if bank == "" {
		return total, nil
	}
	rows, err := db.Query(`
		SELECT DISTINCT pending_issuer FROM message_links
		WHERE parsed_as = 'pending_account' AND pending_last4 = ''`)
	if err != nil {
		return total, err
	}
	var issuers []string
	for rows.Next() {
		var issuer string
		if err := rows.Scan(&issuer); err != nil {
			rows.Close()
			return total, err
		}
		issuers = append(issuers, issuer)
	}
	rows.Close()

	for _, issuer := range issuers {
		if issuer == "" {
			continue
		}
		if strings.ToLower(issuer) != bank &&
			strings.ToLower(emailparse.DisplayNameForIssuer(issuer)) != bank {
			continue
		}
		res, err := db.Exec(`
			DELETE FROM message_links
			WHERE parsed_as = 'pending_account' AND pending_last4 = '' AND pending_issuer = ?`, issuer)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// ClearForRule releases the messages from a rule's sender that nothing could
// read before the rule existed, so a rescan applies the new rule to them.
func ClearForRule(db *sql.DB, mailAccountID int64, rule *parserules.Rule) (int64, error) {
	domain := strings.ToLower(strings.TrimSpace(rule.SenderDomain))
	email := strings.ToLower(strings.TrimSpace(rule.SenderEmail))
	if domain == "" && email == "" {
		return 0, nil
	}
	res, err := db.Exec(`
		DELETE FROM message_links
		WHERE mail_account_id = ?
		  AND parsed_as IN ('unrecognized', 'pending_account')
		  AND ((? != '' AND LOWER(sender) = ?)
		    OR (? != '' AND (LOWER(sender) LIKE '%@' || ? OR LOWER(sender) LIKE '%.' || ?)))`,
		mailAccountID, email, email, domain, domain, domain)
	if err != nil {
		return 0, fmt.Errorf("clearing links for rule %d: %w", rule.ID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PendingHint is what a message said about an account nobody has registered.
type PendingHint struct {
	Issuer string
	Name   string
	Last4  string
	Kind   string
}

// Outcome is what Money made of a single message.
type Outcome struct {
	// ParsedAs is transaction | bill | balance | trade | pending_account |
	// unrecognized, and lands in message_links so the inbox can show it.
	ParsedAs      string
	TransactionID *int64
	BillID        *int64
	AccountID     int64
	// InvestmentID is set when the message was a broker order and became a
	// holding rather than a ledger entry.
	InvestmentID int64
	// RuleID is the parser rule that read the message, if one did.
	RuleID int64
	// Pending is set with ParsedAs = pending_account.
	Pending *PendingHint
}

func unrecognized() Outcome { return Outcome{ParsedAs: "unrecognized"} }

// Persist reads one already-decoded email through the given rules and writes
// whatever the first rule that reads it describes. Rules are tried in the
// order given (priority first); a rule whose sender matches but whose values
// can't be read from this particular message falls through to the next.
func Persist(db *sql.DB, rules []*parserules.Compiled, e *emailparse.Email) Outcome {
	text := parserules.SampleText(e.Subject, e.TextBody)
	emailDate := emailparse.ParseAlertDate(e.Date)
	if emailDate == "" {
		emailDate = normalizeRFCDate(e.Date)
	}
	fromDomain := domainOf(e.From)

	for _, c := range rules {
		if !c.Matches(e.From, fromDomain, e.Subject, text) {
			continue
		}
		ext, err := c.Apply(text)
		if err != nil {
			continue
		}
		out := persistExtraction(db, c.Rule, ext, e, text, emailDate)
		out.RuleID = c.Rule.ID
		return out
	}
	return unrecognized()
}

func persistExtraction(db *sql.DB, rule *parserules.Rule, ext parserules.Extraction, e *emailparse.Email, text, emailDate string) Outcome {
	name := institutionName(rule)
	facts := parserules.ToFacts(ext)

	switch rule.Kind {
	case parserules.KindTransaction:
		txn := parserules.ToTransaction(rule, ext, text, emailDate)
		hint := ledger.AccountHint{
			Issuer: rule.Issuer, Name: name,
			Last4: firstNonEmpty(txn.AccountLast4, facts.Last4), Kind: rule.AccountType,
		}
		accountID, out, ok := resolveOrPend(db, hint, rule.AccountID)
		if !ok {
			return out
		}
		return persistTransaction(db, accountID, txn, facts, emailDate)

	case parserules.KindBill:
		bill := parserules.ToBill(rule, ext)
		// A statement is about a card whatever the rule's default says, unless
		// the rule was explicitly made for a loan.
		kind := rule.AccountType
		if kind != "loan" {
			kind = "credit_card"
		}
		hint := ledger.AccountHint{Issuer: rule.Issuer, Name: name, Last4: bill.CardLast4, Kind: kind}
		accountID, out, ok := resolveOrPend(db, hint, rule.AccountID)
		if !ok {
			return out
		}
		return persistBill(db, accountID, bill, e, facts, emailDate)

	case parserules.KindBalance:
		if !facts.BalanceKnown {
			return unrecognized()
		}
		hint := ledger.AccountHint{Issuer: rule.Issuer, Name: name, Last4: facts.Last4, Kind: rule.AccountType}
		accountID, out, ok := resolveOrPend(db, hint, rule.AccountID)
		if !ok {
			return out
		}
		recordFacts(db, accountID, facts, emailDate)
		return Outcome{ParsedAs: "balance", AccountID: accountID}

	case parserules.KindTrade:
		return persistTrade(db, parserules.ToTrade(rule, ext, emailDate), e, emailDate)
	}
	return unrecognized()
}

// institutionName is what to call the account a rule's mail concerns, before
// the user has named it.
func institutionName(rule *parserules.Rule) string {
	if rule.Issuer != "" {
		return emailparse.DisplayNameForIssuer(rule.Issuer)
	}
	return rule.SenderDomain
}

// resolveOrPend finds the account a message belongs to — the rule's bound
// account, or the one its digits and institution identify — and otherwise
// describes the account that would have to exist, as a pending outcome.
func resolveOrPend(db *sql.DB, hint ledger.AccountHint, boundID int64) (int64, Outcome, bool) {
	if boundID != 0 {
		var archived string
		if err := db.QueryRow(`SELECT archived_at FROM finance_accounts WHERE id = ?`, boundID).Scan(&archived); err == nil && archived == "" {
			return boundID, Outcome{}, true
		}
	}
	if id := ledger.MatchAccount(db, hint); id != 0 {
		return id, Outcome{}, true
	}
	kind := hint.Kind
	if kind == "" {
		kind = "bank"
	}
	return 0, Outcome{
		ParsedAs: "pending_account",
		Pending:  &PendingHint{Issuer: hint.Issuer, Name: hint.Name, Last4: hint.Last4, Kind: kind},
	}, false
}

func persistTransaction(db *sql.DB, accountID int64, txn *models.ParsedEmailTransaction,
	facts parserules.AccountFacts, emailDate string) Outcome {

	if txn.TxnDate == "" {
		// A transaction with no date sorts nowhere and drops out of every
		// month view; the message's own date is the best available stand-in.
		txn.TxnDate = emailDate
	}

	withdrawal, deposit := 0.0, 0.0
	if txn.Type == "income" {
		deposit = txn.Amount
	} else {
		withdrawal = txn.Amount
	}

	dedupe := ledger.EmailDedupeHash(accountID, txn)
	res, err := db.Exec(`
		INSERT OR IGNORE INTO transactions
			(account_id, txn_date, value_date, narration, ref_no, withdrawal_amt, deposit_amt,
			 closing_balance, type, merchant, payment_method, dedupe_hash, category_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			-- A narration this account has already had categorised keeps that
			-- category. The same payee arriving next month is the same kind of
			-- spending, so deciding once is the whole point; without this the
			-- user re-categorises the same recurring payment forever.
			(SELECT category_id FROM transactions
			   WHERE LOWER(TRIM(narration)) = LOWER(TRIM(?))
			     AND TRIM(narration) != '' AND category_id IS NOT NULL
			   ORDER BY id DESC LIMIT 1))`,
		accountID, txn.TxnDate, txn.TxnDate, txn.Narration, txn.RefNo, withdrawal, deposit,
		ledger.NullIfZero(txn.ClosingBalance), txn.Type, txn.Merchant, txn.PaymentMethod, dedupe,
		txn.Narration)
	if err != nil {
		return unrecognized()
	}

	recordFacts(db, accountID, facts, emailDate)

	n, _ := res.RowsAffected()
	if n == 0 {
		// Already have this transaction (the bank sent the alert twice, say).
		// Link the message to the row it describes, so the inbox shows it as
		// recognized and jumps to the right place.
		var existing int64
		if err := db.QueryRow(`SELECT id FROM transactions WHERE account_id = ? AND dedupe_hash = ?`,
			accountID, dedupe).Scan(&existing); err == nil {
			return Outcome{ParsedAs: "transaction", TransactionID: &existing, AccountID: accountID}
		}
		return Outcome{ParsedAs: "unrecognized", AccountID: accountID}
	}
	id, _ := res.LastInsertId()
	ledger.RecomputeAccountBalance(db, accountID)

	// Money moving to or from a broker isn't spending, even though it debits
	// the account exactly like spending does. See DetectInvestmentBroker for
	// why a bank alert rarely spells the broker's name in an obvious way.
	if broker, ok := ledger.DetectInvestmentBroker(txn.Merchant, txn.Narration); ok {
		db.Exec(`UPDATE transactions SET type='transfer', transfer_kind='investment', counterparty=? WHERE id=?`,
			broker, id)
	}

	return Outcome{ParsedAs: "transaction", TransactionID: &id, AccountID: accountID}
}

func persistBill(db *sql.DB, accountID int64, bill models.ParsedBillEmail, e *emailparse.Email,
	facts parserules.AccountFacts, emailDate string) Outcome {

	// Issuers resend statements (reminders, duplicates on request). The unique
	// index on (issuer, card, period) turns the resend into an update of the
	// bill already on the dashboard rather than a second copy of it.
	// RETURNING, not LastInsertId: an upsert that updates leaves last_insert_rowid at another table's row.
	var id int64
	if err := db.QueryRow(`
		INSERT INTO bills (account_id, issuer, card_last4, statement_period, total_due, minimum_due, due_date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(issuer, card_last4, statement_period) WHERE statement_period != ''
		DO UPDATE SET
			account_id  = COALESCE(excluded.account_id, bills.account_id),
			total_due   = COALESCE(excluded.total_due, bills.total_due),
			minimum_due = COALESCE(excluded.minimum_due, bills.minimum_due),
			due_date    = CASE WHEN excluded.due_date != '' THEN excluded.due_date ELSE bills.due_date END
		RETURNING id`,
		ledger.NullIfZeroID(accountID), bill.Issuer, bill.CardLast4,
		bill.StatementPeriod, bill.TotalDue, bill.MinimumDue, bill.DueDate).Scan(&id); err != nil {
		return unrecognized()
	}

	for _, att := range e.PDFAttachments {
		// Re-parsing the same statement mail must not re-store its PDF.
		var already int
		db.QueryRow(`SELECT COUNT(*) FROM pdf_attachments WHERE bill_id = ? AND file_name = ?`,
			id, att.FileName).Scan(&already)
		if already == 0 {
			db.Exec(`INSERT INTO pdf_attachments (bill_id, file_name, content) VALUES (?, ?, ?)`,
				id, att.FileName, att.Content)
		}
	}

	recordFacts(db, accountID, facts, emailDate)
	return Outcome{ParsedAs: "bill", BillID: &id, AccountID: accountID}
}

// persistTrade turns a broker order confirmation into a holding.
//
// Unlike a bank alert this needs no registered account: the position IS the
// record, so there is nothing to hold pending. The message's own ID is the
// dedupe key, which is what lets the mailbox be re-read without buying the
// stock twice.
func persistTrade(db *sql.DB, trade *models.ParsedTrade, e *emailparse.Email, emailDate string) Outcome {
	if trade.TradeDate == "" {
		trade.TradeDate = emailDate
	}

	investmentID, err := ledger.ResolveHolding(db, trade)
	if err != nil || investmentID == 0 {
		return unrecognized()
	}

	dedupe := e.MessageID
	if dedupe == "" {
		// No Message-ID: fall back to the trade's own facts so a re-read still
		// collapses onto one row.
		dedupe = fmt.Sprintf("%s|%s|%s|%.4f|%.4f",
			trade.Broker, trade.Symbol, trade.TradeDate, trade.Shares, trade.Amount)
	}
	if _, err := ledger.RecordTrade(db, investmentID, trade, dedupe); err != nil {
		return unrecognized()
	}
	return Outcome{ParsedAs: "trade", InvestmentID: investmentID}
}

// recordFacts writes the balance and limit a message reported. Both are
// no-ops when it didn't carry them.
func recordFacts(db *sql.DB, accountID int64, facts parserules.AccountFacts, emailDate string) {
	if accountID == 0 {
		return
	}
	if facts.BalanceKnown {
		asOf := facts.AsOf
		if asOf == "" {
			asOf = emailDate
		}
		ledger.RecordBalanceSnapshot(db, accountID, asOf, facts.Balance, "email")
	}
	ledger.RecordCreditLimit(db, accountID, facts.CreditLimit)
}

func domainOf(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if at := strings.LastIndex(email, "@"); at != -1 {
		return email[at+1:]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// normalizeRFCDate reads the Date: header's RFC 5322 form, which the alert date
// parsers don't cover.
func normalizeRFCDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02")
		}
	}
	// Date headers routinely carry a trailing "(IST)" or similar comment.
	if i := strings.Index(raw, " ("); i != -1 {
		return normalizeRFCDate(raw[:i])
	}
	return ""
}
