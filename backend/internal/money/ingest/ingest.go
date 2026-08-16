// Package ingest turns finance mail already sitting in the shared message
// index into transactions and bills.
//
// It does not talk to IMAP. Mail's sync pass is the single ingestion pipeline
// for the whole app; this package reads what that pass indexed, asks the
// provider for the raw bodies of just the messages it cares about, and writes
// the results back with a message_links row joining each message to whatever
// Money made of it.
//
// Before the merge this was a second IMAP client with its own credentials,
// its own UID cursor and its own dedupe table, scanning a hardcoded list of
// four sender domains. Candidate selection is now a SQL query over the index,
// which is what lets the AI classifier widen the net past that list.
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
	"wollow/backend/internal/money/pdfparse"
)

// PDFPasswordLookup returns the plaintext password configured for a
// statement issuer, and whether one is configured at all.
//
// It is a function rather than ingest reaching into pdf_passwords itself,
// because the passwords are encrypted at rest and ingest has no reason to
// know how — that stays with whoever holds the crypto box (moneyapi.Server
// today). nil means "nothing is ever configured", which degrades safely: a
// password-protected statement is held pending exactly like mail naming an
// account nobody registered, and picks up the moment one is added.
type PDFPasswordLookup func(issuer string) (password string, ok bool)

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
	Unrecognized int `json:"unrecognized"`
	Duplicates   int `json:"duplicates"`
	// Trades counts broker order confirmations turned into holdings.
	Trades int `json:"trades"`
	// PendingPDFPassword counts password-protected statements (a Zerodha
	// contract note, a demat holding statement) waiting on a password for
	// their issuer. Same held-for-retry treatment as PendingAccount.
	PendingPDFPassword int `json:"pendingPdfPassword"`
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
// A message qualifies if it is from a known issuer domain, or if the AI
// classifier flagged it as transactional. The second arm is the point of
// reading the index: it surfaces issuers no parser has ever been taught, which
// then land as 'unrecognized' rather than being silently skipped.
//
// Equivalent to RunWithPasswords with no password source configured —
// password-protected investment statements are simply held pending.
func Run(ctx context.Context, db *sql.DB, fetcher RawFetcher, accountID int64, folder string) (*Result, error) {
	return RunWithPasswords(ctx, db, fetcher, accountID, folder, nil)
}

// RunWithPasswords is Run, with password-protected investment statements
// (a broker's contract note, a demat holding statement) decryptable through
// lookup.
func RunWithPasswords(ctx context.Context, db *sql.DB, fetcher RawFetcher, accountID int64, folder string, lookup PDFPasswordLookup) (*Result, error) {
	if folder == "" {
		folder = "INBOX"
	}

	candidates, err := selectCandidates(db, accountID, folder)
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
			// Messages held for a missing account are deliberately left
			// unlinked so they can be retried, which means they stay at the
			// front of this UID-ordered queue. Aborting here therefore did not
			// merely skip a batch — it starved every message behind it on
			// every subsequent run, permanently. Mail kept arriving and
			// nothing was ever imported again.
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
			if err := processOne(db, accountID, c, raw, result, lookup); err != nil {
				// Same reasoning as a failed fetch: one message that cannot be
				// recorded must not stop the ones behind it.
				log.Printf("ingest: recording message %d failed: %v", c.messageID, err)
				result.Failed++
			}
		}
	}

	return result, nil
}

func processOne(db *sql.DB, accountID int64, c candidate, raw []byte, result *Result, lookup PDFPasswordLookup) error {
	parsed, err := emailparse.ParseEML(raw)
	if err != nil {
		return nil // unparseable MIME; leave it unlinked so a later fix can retry
	}

	// Prefer the header the index recorded; fall back to the body's own.
	rfcID := c.rfcMessageID
	if rfcID == "" {
		rfcID = parsed.MessageID
	}

	outcome := PersistWithPasswords(db, emailparse.InstitutionForSender(parsed.From), parsed, lookup)
	parsedAs, txnID, billID := outcome.ParsedAs, outcome.TransactionID, outcome.BillID
	var investmentID *int64
	if outcome.InvestmentID != 0 {
		investmentID = &outcome.InvestmentID
	}

	switch parsedAs {
	case "transaction":
		result.Transactions++
	case "bill":
		result.Bills++
	case "balance":
		result.Balances++
	case "trade":
		result.Trades++
	case pendingPDFPasswordOutcome:
		// Same reasoning, for a statement this pass can't open yet: adding or
		// fixing the password in Settings and syncing again picks it back up.
		result.PendingPDFPassword++
		return nil
	default:
		result.Unrecognized++
	}

	// The unique index on (mail_account_id, rfc_message_id) is the real guard
	// against double-import; a conflict here means another pass got there first.
	res, err := db.Exec(`
		INSERT INTO message_links
			(mail_account_id, message_id, rfc_message_id, uid, sender, subject,
			 received_at, parsed_as, transaction_id, bill_id, investment_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mail_account_id, rfc_message_id) DO NOTHING`,
		accountID, c.messageID, rfcID, c.uid, parsed.From, parsed.Subject,
		parsed.Date, parsedAs, txnID, billID, investmentID)
	if err != nil {
		return fmt.Errorf("linking message %d: %w", c.messageID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		result.Duplicates++
	}
	return nil
}

// selectCandidates returns indexed messages for this mailbox that Money has not
// linked yet and that look like finance mail.
func selectCandidates(db *sql.DB, accountID int64, folder string) ([]candidate, error) {
	args := []interface{}{accountID, folder}

	// Each registry domain is matched exactly *and* as a parent of the sender's
	// domain, because banks send from alerts.<bank>.com as readily as from
	// <bank>.com and an exact-only IN clause missed every one of those.
	clauses := make([]string, 0, len(emailparse.AllowedSenderDomains))
	for _, domain := range emailparse.AllowedSenderDomains {
		domain = strings.ToLower(domain)
		clauses = append(clauses, "(m.from_domain = ? OR m.from_domain LIKE ?)")
		args = append(args, domain, "%."+domain)
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
// parsers rather than standing forever as their first result. Once a message
// has any message_links row, selectCandidates never looks at it again — so
// without this, nothing below ever gets a second chance.
//
// Two situations qualify:
//
//   - Orphaned: a message correctly read as a transaction, bill, or trade
//     whose target row was later deleted (an account's transactions cascade
//     away with it; bills and trades instead survive with their link column
//     set to NULL). Recreating the account should bring the old mail back,
//     not just catch new mail from here on.
//   - Unrecognized: a message the parsers could not read at the time —
//     because the account it named did not exist yet (accounts now
//     auto-create, but this link predates that), or because the parser has
//     since learned the template. Re-linking costs nothing: anything still
//     genuinely unparseable just lands back as 'unrecognized' unchanged.
//
// Orphaned links are only removed when their target is confirmed gone
// (transaction_id/bill_id/investment_id IS NULL for that parsed_as), never one
// still pointing at a real row — so this can add missing history but never
// relabel or duplicate anything already correct.
func RescanStuck(db *sql.DB, mailAccountID int64) (int64, error) {
	res, err := db.Exec(`
		DELETE FROM message_links
		WHERE mail_account_id = ?
		  AND ((parsed_as = 'transaction' AND transaction_id IS NULL)
		    OR (parsed_as = 'bill' AND bill_id IS NULL)
		    OR (parsed_as = 'trade' AND investment_id IS NULL)
		    OR parsed_as = 'unrecognized')`,
		mailAccountID)
	if err != nil {
		return 0, fmt.Errorf("clearing stuck links: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// pendingPDFPasswordOutcome marks a password-protected investment statement
// this pass could not open. No link is written, so it is retried once a
// password is configured.
const pendingPDFPasswordOutcome = "pending_pdf_password"

// Outcome is what Money made of a single message.
type Outcome struct {
	// ParsedAs is transaction | bill | balance | trade | unrecognized, and
	// lands in message_links so the inbox can show it. The one value that does
	// not is pending_pdf_password.
	ParsedAs      string
	TransactionID *int64
	BillID        *int64
	AccountID     int64
	// InvestmentID is set when the message was a broker order and became a
	// holding rather than a ledger entry.
	InvestmentID int64
}

func unrecognized() Outcome { return Outcome{ParsedAs: "unrecognized"} }

func pendingPDFPassword() Outcome { return Outcome{ParsedAs: pendingPDFPasswordOutcome} }

// Persist reads one already-decoded email and writes whatever it describes: a
// transaction, a bill reminder, or — new, and the reason balances used to go
// stale — a bank-reported balance.
//
// Equivalent to PersistWithPasswords with no password source — password-
// protected investment statements are held pending.
func Persist(db *sql.DB, inst *emailparse.Institution, e *emailparse.Email) Outcome {
	return PersistWithPasswords(db, inst, e, nil)
}

// PersistWithPasswords is Persist, with password-protected investment
// statements decryptable through lookup (see Zerodha's contract notes and
// demat holding statements, handled by persistZerodhaMail).
//
// Every message also gets its account facts recorded, transaction alerts
// included: a spend alert that ends with "available balance: INR 2,08,870.09"
// tells us both things, and only reading the first half is how an account's
// balance drifted from the bank's.
func PersistWithPasswords(db *sql.DB, inst *emailparse.Institution, e *emailparse.Email, lookup PDFPasswordLookup) Outcome {
	issuer := ""
	institutionName := ""
	if inst != nil {
		issuer = inst.Issuer
		institutionName = inst.Name
	}

	facts := emailparse.ParseAccountFacts(e.Subject, e.TextBody)
	kind := string(emailparse.KindForAlert(inst, e.Subject, e.TextBody))
	emailDate := emailparse.ParseAlertDate(e.Date)
	if emailDate == "" {
		emailDate = normalizeRFCDate(e.Date)
	}

	// Trades are matched before anything else. A broker's "BUY order ... for
	// $245.73 is successful" carries an amount and a direction word, so the
	// generic alert reader would book it as a $245.73 bank expense — money
	// that never left a tracked account, and a holding that never appears.
	if trade, ok := emailparse.ParseTradeEmail(inst, e.Subject, e.TextBody); ok {
		return persistTrade(db, trade, e, emailDate)
	}

	// Zerodha's own mail (Coin allotments, contract notes, demat holding
	// statements, and the funding notices that must NOT become bank
	// transactions) is handled on its own path. Anything that isn't one of
	// those falls through to the ordinary pipeline below unchanged.
	if inst != nil && inst.Issuer == "Zerodha" {
		if out, handled := persistZerodhaMail(db, inst, e, emailDate, lookup); handled {
			return out
		}
	}

	if emailparse.IsBillEmail(e.Subject) {
		return persistBill(db, issuer, institutionName, e, facts, emailDate)
	}

	txn := parseTransaction(issuer, e)
	if txn == nil || txn.Amount == 0 {
		// No transaction — but if the bank stated a balance, that is still
		// worth having. This is what a "balance update" email is, and it used
		// to be discarded entirely.
		if facts.BalanceKnown {
			return persistBalanceOnly(db, issuer, institutionName, kind, facts, emailDate)
		}
		return unrecognized()
	}

	last4 := firstNonEmpty(txn.AccountLast4, facts.AccountLast4)
	accountID := ledger.ResolveAccount(db, ledger.AccountHint{
		Issuer: issuer, Name: institutionName, Last4: last4, Kind: kind,
	})
	if accountID == 0 {
		return unrecognized()
	}

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
		// Already have this transaction. The link row still gets written, so
		// the inbox shows the message as recognized rather than as a failure.
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

// parseTransaction runs the issuer's own parser where one exists and falls back
// to the shared alert reader everywhere else — which is what lets an issuer
// nobody has written a parser for still produce transactions.
func parseTransaction(issuer string, e *emailparse.Email) *models.ParsedEmailTransaction {
	switch issuer {
	case "HDFC":
		if emailparse.IsHDFCBalanceUpdate(e.TextBody) {
			return nil // a snapshot, handled as a balance rather than a transaction
		}
		if txn, ok := emailparse.ParseHDFCEmail(e.Subject, e.TextBody); ok {
			return txn
		}
	case "Axis":
		if txn, ok := emailparse.ParseAxisEmail(e.Subject, e.TextBody); ok {
			return txn
		}
	}
	if emailparse.IsBalanceOnlyAlert(e.Subject, e.TextBody) {
		return nil
	}
	if txn, ok := emailparse.ParseGenericAlert(e.Subject, e.TextBody); ok {
		return txn
	}
	return nil
}

func persistBill(db *sql.DB, issuer, institutionName string, e *emailparse.Email,
	facts emailparse.AccountFacts, emailDate string) Outcome {

	bill := emailparse.ParseBillEmail(issuer, e.Subject, e.TextBody)
	if bill.CardLast4 == "" {
		bill.CardLast4 = facts.AccountLast4
	}

	// A statement is always about a card, whatever the sender's default kind.
	// The card product name is preferred over the bank's: an issuer sends
	// statements for several cards, and naming them all after the bank would
	// pile every one of them into a single account.
	accountID := ledger.ResolveAccount(db, ledger.AccountHint{
		Issuer: issuer,
		Name:   firstNonEmpty(bill.CardName, institutionName),
		Last4:  bill.CardLast4,
		Kind:   string(emailparse.KindCreditCard),
	})
	if accountID == 0 {
		// A due date attached to nothing cannot be reconciled — this is a
		// genuine failure to record the card, not a missing approval.
		return unrecognized()
	}

	// Issuers resend statements (reminders, duplicates on request). The unique
	// index on (issuer, card, period) turns the resend into an update of the
	// bill already on the dashboard rather than a second copy of it.
	res, err := db.Exec(`
		INSERT INTO bills (account_id, issuer, card_last4, statement_period, total_due, minimum_due, due_date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(issuer, card_last4, statement_period) WHERE statement_period != ''
		DO UPDATE SET
			account_id  = COALESCE(excluded.account_id, bills.account_id),
			total_due   = COALESCE(excluded.total_due, bills.total_due),
			minimum_due = COALESCE(excluded.minimum_due, bills.minimum_due),
			due_date    = CASE WHEN excluded.due_date != '' THEN excluded.due_date ELSE bills.due_date END`,
		ledger.NullIfZeroID(accountID), bill.Issuer, bill.CardLast4,
		bill.StatementPeriod, bill.TotalDue, bill.MinimumDue, bill.DueDate)
	if err != nil {
		return unrecognized()
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// An ON CONFLICT update reports no new row id; find the one we updated
		// so the message still links to a bill.
		db.QueryRow(`SELECT id FROM bills WHERE issuer = ? AND card_last4 = ? AND statement_period = ?`,
			bill.Issuer, bill.CardLast4, bill.StatementPeriod).Scan(&id)
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
	if id == 0 {
		return Outcome{ParsedAs: "unrecognized", AccountID: accountID}
	}
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

// persistZerodhaMail handles the shapes of mail specific to Zerodha: it
// returns handled=false for anything that isn't one of them, so the caller
// falls through to the ordinary bill/transaction/balance pipeline unchanged.
func persistZerodhaMail(db *sql.DB, inst *emailparse.Institution, e *emailparse.Email, emailDate string, lookup PDFPasswordLookup) (out Outcome, handled bool) {
	// Zerodha's own notice that money moved into or out of the trading
	// account balance. The real movement is already a bank-side transaction
	// (an HDFC UPI debit, say) that the transfer-reclassification below picks
	// up; this email must produce nothing of its own. See
	// IsZerodhaFundingNoticeEmail for why letting it reach the generic reader
	// would book a phantom transaction.
	if emailparse.IsZerodhaFundingNoticeEmail(e.Subject) {
		return unrecognized(), true
	}

	if trades := emailparse.ParseCoinAllotment(e.Subject, e.TextBody); len(trades) > 0 {
		return persistZerodhaTrades(db, trades, e, emailDate), true
	}

	wantsPDF := emailparse.IsZerodhaContractNoteEmail(e.Subject) || emailparse.IsZerodhaHoldingStatementEmail(e.Subject)
	if !wantsPDF || len(e.PDFAttachments) == 0 {
		return Outcome{}, false
	}

	var password string
	var ok bool
	if lookup != nil {
		password, ok = lookup(inst.Issuer)
	}
	if !ok {
		return pendingPDFPassword(), true
	}

	text, err := pdfparse.ExtractText(e.PDFAttachments[0].Content, password)
	if err != nil {
		// Wrong or since-changed password. Held for retry rather than
		// discarded — fixing it in Settings and syncing again picks this
		// message back up, the same way a missing password does.
		return pendingPDFPassword(), true
	}

	if emailparse.IsZerodhaContractNoteEmail(e.Subject) {
		trades := emailparse.ParseZerodhaContractNoteText(text)
		if len(trades) == 0 {
			return unrecognized(), true
		}
		return persistZerodhaTrades(db, trades, e, emailDate), true
	}

	asOf, holdings := emailparse.ParseZerodhaHoldingsSnapshot(text)
	if len(holdings) == 0 {
		return unrecognized(), true
	}
	var lastID int64
	for _, h := range holdings {
		id, err := ledger.RecordHoldingSnapshot(db, models.ParsedTrade{
			Symbol: h.Name, Identifier: h.ISIN, Broker: inst.Issuer,
			Currency: "INR", Kind: emailparse.KindForISIN(h.ISIN),
		}, h.Units, h.Rate, h.Value, asOf)
		if err != nil {
			log.Printf("ingest: recording Zerodha holding %s (%s): %v", h.Name, h.ISIN, err)
			continue
		}
		lastID = id
	}
	if lastID == 0 {
		return unrecognized(), true
	}
	return Outcome{ParsedAs: "trade", InvestmentID: lastID}, true
}

// persistZerodhaTrades stores every trade parsed from one Zerodha email.
//
// Unlike INDmoney's order mails (always exactly one instrument), a Coin
// allotment or a contract note commonly names several: each is its own trade,
// deduplicated on the instrument and side within the message rather than on
// the message alone, so a mail naming five funds doesn't collapse into one.
func persistZerodhaTrades(db *sql.DB, trades []models.ParsedTrade, e *emailparse.Email, emailDate string) Outcome {
	var lastID int64
	stored := 0
	for i := range trades {
		t := &trades[i]
		if t.TradeDate == "" {
			t.TradeDate = emailDate
		}
		investmentID, err := ledger.ResolveHolding(db, t)
		if err != nil || investmentID == 0 {
			continue
		}

		base := e.MessageID
		if base == "" {
			base = fmt.Sprintf("%s|%s", t.Broker, t.TradeDate)
		}
		dedupe := fmt.Sprintf("%s|%s|%s", base, t.Identifier, t.Side)

		if _, err := ledger.RecordTrade(db, investmentID, t, dedupe); err != nil {
			continue
		}
		lastID = investmentID
		stored++
	}
	if stored == 0 {
		return unrecognized()
	}
	return Outcome{ParsedAs: "trade", InvestmentID: lastID}
}

func persistBalanceOnly(db *sql.DB, issuer, institutionName, kind string,
	facts emailparse.AccountFacts, emailDate string) Outcome {

	accountID := ledger.ResolveAccount(db, ledger.AccountHint{
		Issuer: issuer, Name: institutionName, Last4: facts.AccountLast4, Kind: kind,
	})
	if accountID == 0 {
		return unrecognized()
	}
	recordFacts(db, accountID, facts, emailDate)
	return Outcome{ParsedAs: "balance", AccountID: accountID}
}

// recordFacts writes the balance and limit an alert reported. Both are
// no-ops when the alert didn't carry them.
func recordFacts(db *sql.DB, accountID int64, facts emailparse.AccountFacts, emailDate string) {
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
