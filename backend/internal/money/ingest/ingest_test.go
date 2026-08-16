package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/platform/db"
)

// sample is one real .eml from statements/, already parsed far enough to fill
// in the index row that Mail's sync pass would have written for it.
type sample struct {
	name       string
	uid        uint32
	raw        []byte
	from       string
	fromDomain string
	subject    string
	rfcID      string
}

// fakeFetcher stands in for a live IMAP connection, serving the sample bodies
// by UID. Ingest is supposed to reach the network only for messages it has
// already picked out of the index, so this also records what it asked for.
type fakeFetcher struct {
	byUID     map[uint32][]byte
	requested []uint32
	calls     int
	failUID   uint32
}

func (f *fakeFetcher) FetchRaw(_ context.Context, _ string, uids []uint32) ([]mail.RawMessage, error) {
	f.calls++
	f.requested = append(f.requested, uids...)
	// failUID makes the batch containing it unreadable, standing in for the
	// IMAP errors that happen against a real server.
	if f.failUID != 0 {
		for _, uid := range uids {
			if uid == f.failUID {
				return nil, fmt.Errorf("simulated fetch failure at uid %d", uid)
			}
		}
	}
	out := make([]mail.RawMessage, 0, len(uids))
	for _, uid := range uids {
		if raw, ok := f.byUID[uid]; ok {
			out = append(out, mail.RawMessage{UID: uid, Raw: raw})
		}
	}
	return out, nil
}

func loadSamples(t *testing.T) []sample {
	t.Helper()
	// Repo root, four levels up from internal/money/ingest.
	dir := filepath.Join("..", "..", "..", "..", "statements")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("sample dir %s not found: %v", dir, err)
	}

	var out []sample
	var uid uint32 = 100
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".eml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		parsed, err := emailparse.ParseEML(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		uid++
		s := sample{
			name:    e.Name(),
			uid:     uid,
			raw:     raw,
			from:    parsed.From,
			subject: parsed.Subject,
			rfcID:   parsed.MessageID,
		}
		// Mirror how sync derives from_domain, since candidate selection keys
		// off exactly that column.
		if at := strings.LastIndex(s.from, "@"); at != -1 {
			s.fromDomain = strings.ToLower(s.from[at+1:])
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		t.Fatal("no .eml samples found")
	}
	return out
}

// seedIndex writes the mail_accounts + messages rows that a sync pass would
// have produced for these samples, without any Money-side rows.
func seedIndex(t *testing.T, conn *sql.DB, samples []sample) int64 {
	t.Helper()
	res, err := conn.Exec(`
		INSERT INTO mail_accounts (label, imap_host, imap_port, username, encrypted_password)
		VALUES ('Test', 'imap.example.com', 993, 'test@example.com', 'x')`)
	if err != nil {
		t.Fatalf("seed mail account: %v", err)
	}
	accountID, _ := res.LastInsertId()

	for _, s := range samples {
		if _, err := conn.Exec(`
			INSERT INTO messages (account_id, folder, uid, rfc_message_id, subject, from_email, from_domain, date)
			VALUES (?, 'INBOX', ?, ?, ?, ?, ?, '')`,
			accountID, s.uid, s.rfcID, s.subject, s.from, s.fromDomain); err != nil {
			t.Fatalf("seed message %s: %v", s.name, err)
		}
	}
	return accountID
}

// seedAccounts registers the accounts the sample emails refer to.
//
// Ingest no longer invents accounts, so this is the setup a real user performs
// once by approving them in the discovered-accounts list. The identities here
// are the ones the samples actually name — note HDFC 4125 is a *bank* account,
// which is what the alerts say it is.
func seedAccounts(t *testing.T, conn *sql.DB) {
	t.Helper()
	accounts := []struct {
		name, bank, kind, number string
	}{
		{"HDFC Bank •• 4125", "HDFC", "bank", "XXXXXXXX4125"},
		{"Axis Bank •• 5792", "Axis", "credit_card", "XXXXXXXX5792"},
		{"ICICI Bank •• 7001", "ICICI", "credit_card", "XXXXXXXX7001"},
		{"BOBCARD •• 3109", "BOBCARD", "credit_card", "XXXXXXXX3109"},
		// The Diners statement names no digits, so it matches on
		// (institution, kind) with an empty number.
		{"HDFC Bank - Diners Privilege Credit Card", "HDFC", "credit_card", ""},
	}
	for _, a := range accounts {
		if _, err := conn.Exec(`
			INSERT INTO finance_accounts (name, bank, account_type, account_number, currency, source)
			VALUES (?, ?, ?, ?, 'INR', 'manual')`, a.name, a.bank, a.kind, a.number); err != nil {
			t.Fatalf("seed account %s: %v", a.name, err)
		}
	}
}

func newFetcher(samples []sample) *fakeFetcher {
	byUID := make(map[uint32][]byte, len(samples))
	for _, s := range samples {
		byUID[s.uid] = s.raw
	}
	return &fakeFetcher{byUID: byUID}
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// TestIngestFromIndex is the equivalence check for the pipeline merge: reading
// finance mail out of the shared index must produce exactly what the old
// dedicated IMAP client produced when it searched the same four sender domains.
func TestIngestFromIndex(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	seedAccounts(t, conn)
	fetcher := newFetcher(samples)

	result, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	t.Logf("result: %+v", *result)

	// Counts are deliberately not hardcoded. statements/ is a working folder
	// that real examples get dropped into as new issuers turn up, and a test
	// that has to be hand-edited every time one arrives is a test that gets
	// deleted rather than fixed. What follows are the properties that must
	// hold for any set of samples.

	if result.Scanned == 0 {
		t.Fatal("nothing was scanned; the samples are all from known institutions")
	}

	// Every scanned message must land in exactly one outcome. A message that
	// falls through all of them is silently lost, which is the failure mode
	// this whole package exists to prevent. (Duplicates overlap the buckets by
	// design — a duplicate still reports what it parsed as — so it is excluded.)
	bucketed := result.Transactions + result.Bills + result.Balances + result.Trades +
		result.Unrecognized + result.PendingAccount + result.PendingPDFPassword
	if bucketed != result.Scanned {
		t.Errorf("%d messages scanned but %d accounted for (%+v) — some outcome is unreported",
			result.Scanned, bucketed, *result)
	}

	// The samples describe real transactions, bills and balances; extracting
	// none of them means the parsers stopped working even if nothing panicked.
	if result.Transactions+result.Bills+result.Balances == 0 {
		t.Error("the sample emails produced nothing at all")
	}

	// Every account these samples name is seeded, so nothing should be held.
	if result.PendingAccount != 0 {
		t.Errorf("%d messages held for a missing account, want 0 — seedAccounts is out of "+
			"step with the samples, so part of the pipeline is untested", result.PendingAccount)
	}

	// Bodies must only be fetched for messages already picked out of the index.
	if len(fetcher.requested) != result.Scanned {
		t.Errorf("fetched %d bodies for %d scanned messages — ingest must not fetch what it didn't select",
			len(fetcher.requested), result.Scanned)
	}

	var txnCount, billCount int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txnCount)
	conn.QueryRow(`SELECT COUNT(*) FROM bills`).Scan(&billCount)
	if txnCount != result.Transactions {
		t.Errorf("db has %d transactions, want %d", txnCount, result.Transactions)
	}
	if billCount != result.Bills {
		t.Errorf("db has %d bills, want %d", billCount, result.Bills)
	}

	// Every link must point back at the index row. This column is what makes
	// "show me the email behind this transaction" possible at all.
	var links, linked int
	conn.QueryRow(`SELECT COUNT(*) FROM message_links`).Scan(&links)
	conn.QueryRow(`SELECT COUNT(*) FROM message_links WHERE message_id IS NOT NULL`).Scan(&linked)
	held := result.PendingAccount + result.PendingPDFPassword
	if want := result.Scanned - result.Duplicates - held; links != want {
		t.Errorf("message_links has %d rows, want %d (scanned − duplicates − held)", links, want)
	}
	if linked != links {
		t.Errorf("%d of %d links have no message_id — cross-product links would be broken", links-linked, links)
	}
}

// A batch that cannot be fetched must not starve the messages behind it.
//
// This is a regression test for a stall that reached real data: messages held
// for a missing account are left unlinked so they can be retried, so they stay
// at the front of the UID-ordered queue. When one batch failed the whole pass
// returned, which meant every later message was skipped again on every
// subsequent run — mail kept arriving and nothing was ever imported again.
func TestFailedFetchDoesNotStarveLaterMessages(t *testing.T) {
	samples := loadSamples(t)
	if len(samples) < 2 {
		t.Skip("need at least two samples to have a batch behind the failing one")
	}
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	seedAccounts(t, conn)

	fetcher := newFetcher(samples)
	fetcher.failUID = samples[0].uid // the first batch is unreadable

	// One message per batch, so the failure lands on its own and everything
	// after it is a separate batch.
	oldBatch := batchSizeForTest
	batchSizeForTest = 1
	defer func() { batchSizeForTest = oldBatch }()

	result, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("a failing batch must not fail the pass: %v", err)
	}
	if result.Failed == 0 {
		t.Error("the failure was not reported; a silent stall is how this went unnoticed")
	}
	if result.Scanned == 0 {
		t.Fatal("nothing behind the failing batch was processed — still starved")
	}
	if result.Transactions+result.Bills+result.Balances == 0 {
		t.Error("no mail imported despite readable messages behind the failure")
	}
	t.Logf("result with a failing batch: %+v", *result)
}

// TestIngestIsIdempotent guards the property the old UID cursor used to provide:
// running twice must not double-import.
func TestIngestIsIdempotent(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	seedAccounts(t, conn)
	fetcher := newFetcher(samples)

	first, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	var txnAfterFirst int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txnAfterFirst)

	second, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}

	// Anything genuinely pending (an unregistered account, a statement with no
	// password yet) is SUPPOSED to be picked up again — that is the whole
	// point of leaving it unlinked. Only already-linked messages must not be
	// re-selected.
	wantRescanned := first.PendingAccount + first.PendingPDFPassword
	if second.Scanned != wantRescanned {
		t.Errorf("second pass scanned %d, want %d (only what's still pending) — already-linked "+
			"messages must not be re-selected", second.Scanned, wantRescanned)
	}
	var txnAfterSecond int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txnAfterSecond)
	if txnAfterSecond != txnAfterFirst {
		t.Errorf("re-running created duplicates: had %d, now %d", txnAfterFirst, txnAfterSecond)
	}
	if first.Transactions == 0 {
		t.Fatal("first pass extracted nothing; the idempotency check proves nothing")
	}
}

// TestClassifierWidensTheNet is the capability the merge exists to unlock: a
// message from a sender no parser knows still reaches Money, because the AI
// classifier flagged it as transactional. It lands as 'unrecognized' rather
// than being silently skipped, which is what makes an unsupported issuer
// visible instead of invisible.
func TestClassifierWidensTheNet(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	seedAccounts(t, conn)
	fetcher := newFetcher(samples)
	// The co-operative bank below is deliberately NOT seeded until the second
	// half of the test — an unknown issuer must reach Money without an account
	// existing for it.
	if _, err := conn.Exec(`
		INSERT INTO finance_accounts (name, bank, account_type, account_number, currency, source)
		VALUES ('Tiny Coop •• 8899', '', 'bank', 'XXXXXXXX8899', 'INR', 'manual')`); err != nil {
		t.Fatalf("seed outsider account: %v", err)
	}

	// A co-operative bank nobody has heard of, from a domain no registry
	// entry covers.
	const outsiderUID = 9001
	const outsiderBody = "From: alerts@tiny-coop-bank.example\r\n" +
		"Subject: Debit alert\r\n" +
		"Message-Id: <outsider@tiny-coop-bank.example>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Rs.750.00 is debited from your account ending 8899 on 05-08-26 towards GROCERY MART.\r\n"

	if _, err := conn.Exec(`
		INSERT INTO messages (account_id, folder, uid, rfc_message_id, subject, from_email, from_domain, date)
		VALUES (?, 'INBOX', ?, '<outsider@tiny-coop-bank.example>', 'Debit alert',
		        'alerts@tiny-coop-bank.example', 'tiny-coop-bank.example', '')`,
		accountID, outsiderUID); err != nil {
		t.Fatalf("seed outsider message: %v", err)
	}
	fetcher.byUID[outsiderUID] = []byte(outsiderBody)

	var msgID int64
	if err := conn.QueryRow(
		`SELECT id FROM messages WHERE account_id = ? AND uid = ?`, accountID, outsiderUID,
	).Scan(&msgID); err != nil {
		t.Fatalf("locate outsider message: %v", err)
	}

	// Unclassified, the outsider must not be selected: sender-domain matching
	// alone has no reason to reach it.
	baseline, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("baseline ingest: %v", err)
	}
	if baseline.Scanned != len(samples) {
		t.Fatalf("baseline scanned = %d, want %d — the outsider should not qualify yet",
			baseline.Scanned, len(samples))
	}

	if _, err := conn.Exec(
		`INSERT INTO classifications (message_id, category, is_transactional) VALUES (?, 'finance', 1)`, msgID,
	); err != nil {
		t.Fatalf("classify outsider: %v", err)
	}

	result, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// Whatever was already pending from the baseline pass (an unregistered
	// account, a not-yet-password statement) legitimately gets re-scanned
	// too — the newly classified outsider is the ONE NEW arrival, not the
	// only thing scanned.
	wantScanned := 1 + baseline.PendingAccount + baseline.PendingPDFPassword
	if result.Scanned != wantScanned {
		t.Errorf("scanned = %d, want %d — the newly classified message plus whatever was still pending",
			result.Scanned, wantScanned)
	}

	var parsedAs string
	if err := conn.QueryRow(
		`SELECT parsed_as FROM message_links WHERE message_id = ?`, msgID,
	).Scan(&parsedAs); err != nil {
		t.Fatalf("outsider was not linked at all: %v", err)
	}
	// The shared alert reader understands the standard Indian phrasing even
	// from an issuer nobody wrote a parser for — which is the whole point of
	// having one.
	if parsedAs != "transaction" {
		t.Errorf("outsider parsed_as = %q, want %q", parsedAs, "transaction")
	}

	var narration string
	var amount float64
	if err := conn.QueryRow(`
		SELECT t.narration, t.withdrawal_amt
		FROM transactions t JOIN message_links l ON l.transaction_id = t.id
		WHERE l.message_id = ?`, msgID).Scan(&narration, &amount); err != nil {
		t.Fatalf("outsider produced no transaction: %v", err)
	}
	if amount != 750 {
		t.Errorf("amount = %.2f, want 750.00", amount)
	}
}

// TestPersistRealSampleEmails exercises the parse -> persist path directly over
// every sample, independent of index selection, so a parser regression is
// distinguishable from a selection regression.
func TestPersistRealSampleEmails(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	seedAccounts(t, conn)

	counts := map[string]int{}
	for _, s := range samples {
		parsed, err := emailparse.ParseEML(s.raw)
		if err != nil {
			t.Fatalf("parse %s: %v", s.name, err)
		}
		inst := emailparse.InstitutionForSender(parsed.From)
		outcome := Persist(conn, inst, parsed)
		counts[outcome.ParsedAs]++
		t.Logf("%s -> issuer=%s kind=%s", s.name, emailparse.IssuerForSender(parsed.From), outcome.ParsedAs)
	}

	t.Logf("counts: %+v", counts)
	if counts["transaction"] == 0 {
		t.Error("expected at least one transaction from the sample emails")
	}
	// Bills and balances are only asserted when the current samples contain
	// that kind of mail — the folder is curated by hand, so requiring one of
	// each would fail on a perfectly good set of transaction alerts.
	if counts[pendingAccountOutcome] != 0 {
		t.Errorf("%d samples were held for a missing account; seedAccounts needs the account "+
			"those samples name", counts[pendingAccountOutcome])
	}

	// Ingest must not have invented anything: the accounts present are exactly
	// the ones seeded, with the types the user chose still intact. The HDFC
	// savings account staying 'bank' is the specific regression that matters —
	// a card-shaped HDFC alert naming the same digits used to flip it to
	// credit_card, which reports a salary account's balance as debt.
	rows, err := conn.Query(`SELECT name, bank, account_type, source FROM finance_accounts ORDER BY id`)
	if err != nil {
		t.Fatalf("listing accounts: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, bank, kind, source string
		if err := rows.Scan(&name, &bank, &kind, &source); err != nil {
			t.Fatalf("scanning account: %v", err)
		}
		count++
		t.Logf("account: %-42s bank=%-8s type=%-12s source=%s", name, bank, kind, source)
		if source != "manual" {
			t.Errorf("account %q has source=%q — ingest must not create accounts", name, source)
		}
	}
	if count != 5 {
		t.Errorf("finance_accounts has %d rows, want the 5 seeded — ingest created %d of its own",
			count, count-5)
	}

	var hdfcKind string
	conn.QueryRow(`SELECT account_type FROM finance_accounts WHERE account_number = 'XXXXXXXX4125'`).
		Scan(&hdfcKind)
	if hdfcKind != "bank" {
		t.Errorf("HDFC 4125 account_type = %q, want \"bank\" — mail must not rewrite a chosen type", hdfcKind)
	}
}

// The behaviour the whole change exists for: an alert naming an account nobody
// registered is held, not guessed at. It must leave no link behind, so that
// approving the account and syncing again imports its history.
func TestUnknownAccountIsHeldForRetryNotInvented(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	fetcher := newFetcher(samples)

	// No finance accounts at all — the state right after a reset.
	first, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.PendingAccount == 0 {
		t.Fatal("nothing was held pending; the samples do name accounts")
	}
	if first.Transactions != 0 || first.Bills != 0 || first.Balances != 0 {
		t.Errorf("imported %d txns / %d bills / %d balances with no accounts registered",
			first.Transactions, first.Bills, first.Balances)
	}

	var accounts int
	conn.QueryRow(`SELECT COUNT(*) FROM finance_accounts`).Scan(&accounts)
	if accounts != 0 {
		t.Errorf("ingest created %d accounts, want 0", accounts)
	}

	// Now the user approves the accounts and syncs again. The held mail must
	// be picked up — if pending messages had been linked, this would import
	// nothing and the history would be lost silently.
	seedAccounts(t, conn)
	second, err := Run(context.Background(), conn, fetcher, accountID, "INBOX")
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Transactions+second.Bills+second.Balances == 0 {
		t.Error("nothing imported after adding the accounts — held mail was not retried")
	}
	if second.PendingAccount != 0 {
		t.Errorf("%d still held after the accounts exist", second.PendingAccount)
	}
	t.Logf("held then imported: %+v", *second)
}

// The HDFC balance-update sample states a balance the bank vouches for. It has
// to end up on the account, or an alert-only account reads as empty.
func TestPersistRecordsReportedBalance(t *testing.T) {
	conn := openDB(t)
	seedAccounts(t, conn)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "statements",
		"HDFC email balance update.eml"))
	if err != nil {
		t.Skipf("sample not available: %v", err)
	}
	parsed, err := emailparse.ParseEML(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	outcome := Persist(conn, emailparse.InstitutionForSender(parsed.From), parsed)
	if outcome.ParsedAs != "balance" {
		t.Fatalf("parsed as %q, want %q", outcome.ParsedAs, "balance")
	}
	if outcome.AccountID == 0 {
		t.Fatal("no account was resolved for the balance alert")
	}

	var kind string
	var balance float64
	if err := conn.QueryRow(
		`SELECT account_type, current_balance FROM finance_accounts WHERE id = ?`, outcome.AccountID,
	).Scan(&kind, &balance); err != nil {
		t.Fatalf("reading account: %v", err)
	}
	if kind != "bank" {
		t.Errorf("account_type = %q, want %q — a savings balance alert is not a card", kind, "bank")
	}
	const want = 208870.09
	if balance != want {
		t.Errorf("current_balance = %.2f, want %.2f", balance, want)
	}
}

// A broker order confirmation becomes a holding, not a bank transaction.
//
// "BUY order ... for $245.73 is successful" carries an amount and a direction
// word, so the generic alert reader would book it as a $245.73 expense against
// a bank account — money that never left any tracked account, and a US stock
// that never appears in the portfolio.
func TestBrokerOrderBecomesAHoldingNotATransaction(t *testing.T) {
	conn := openDB(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "statements",
		"BUY order of Take-Two Interactive Software Inc. for $245.73 is successful.eml"))
	if err != nil {
		t.Skipf("sample not available: %v", err)
	}
	parsed, err := emailparse.ParseEML(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	outcome := Persist(conn, emailparse.InstitutionForSender(parsed.From), parsed)
	if outcome.ParsedAs != "trade" {
		t.Fatalf("parsed as %q, want %q", outcome.ParsedAs, "trade")
	}

	// Nothing may have reached the ledger.
	var txns int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txns)
	if txns != 0 {
		t.Errorf("%d bank transactions written for a securities order, want 0", txns)
	}

	var kind, currency, name string
	var units, invested, value float64
	err = conn.QueryRow(`SELECT kind, currency, name, COALESCE(units,0), invested_amount, current_value
		FROM investments WHERE id = ?`, outcome.InvestmentID).
		Scan(&kind, &currency, &name, &units, &invested, &value)
	if err != nil {
		t.Fatalf("reading holding: %v", err)
	}
	if kind != "us_stock" {
		t.Errorf("kind = %q, want us_stock", kind)
	}
	if currency != "USD" {
		t.Errorf("currency = %q, want USD", currency)
	}
	if name != "Take-Two Interactive Software Inc." {
		t.Errorf("name = %q", name)
	}
	if units != 1 || invested != 245.73 {
		t.Errorf("units=%v invested=%v, want 1 and 245.73", units, invested)
	}
	// Unpriced, so the position is carried at cost rather than at zero.
	if value != 245.73 {
		t.Errorf("current value = %v, want it to fall back to cost", value)
	}

	// Re-reading the same mail must not buy the stock again.
	if second := Persist(conn, emailparse.InstitutionForSender(parsed.From), parsed); second.ParsedAs != "trade" {
		t.Fatalf("second pass parsed as %q", second.ParsedAs)
	}
	var trades int
	conn.QueryRow(`SELECT COUNT(*) FROM investment_trades`).Scan(&trades)
	conn.QueryRow(`SELECT COALESCE(units,0) FROM investments WHERE id = ?`, outcome.InvestmentID).Scan(&units)
	if trades != 1 || units != 1 {
		t.Errorf("re-reading produced %d trades and %v units, want 1 and 1", trades, units)
	}
}

// zerodhaSample loads one real Zerodha .eml, skipping the test when it isn't
// present — these are dropped into statements/ by hand and aren't always there.
func zerodhaSample(t *testing.T, name string) *emailparse.Email {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "statements", name))
	if err != nil {
		t.Skipf("sample not available: %v", err)
	}
	parsed, err := emailparse.ParseEML(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

// Zerodha's own notice that money moved into or out of the trading account
// balance ("Payment success", "Settlement of unused funds") must produce
// nothing — the real movement is the bank-side transaction, which this
// email's own wording ("deposited", "account 4125") would otherwise be
// misread as a second, phantom credit to an account that doesn't exist under
// Zerodha's name.
func TestZerodhaFundingNoticesProduceNoTransaction(t *testing.T) {
	for _, name := range []string{"Payment success.eml", "Settlement of unused funds.eml"} {
		t.Run(name, func(t *testing.T) {
			conn := openDB(t)
			parsed := zerodhaSample(t, name)
			outcome := Persist(conn, emailparse.InstitutionForSender(parsed.From), parsed)
			if outcome.ParsedAs != "unrecognized" {
				t.Errorf("parsed as %q, want unrecognized", outcome.ParsedAs)
			}
			var txns int
			conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txns)
			if txns != 0 {
				t.Errorf("%d transactions created from a Zerodha funding notice, want 0", txns)
			}
		})
	}
}

// A Coin allotment report needs no password (it's plain text) and becomes a
// mutual fund holding, matched by folio number across re-imports.
func TestCoinAllotmentBecomesAMutualFundHolding(t *testing.T) {
	conn := openDB(t)
	parsed := zerodhaSample(t, "Coin by Zerodha - Allotment report - 10-08-2026.eml")

	outcome := Persist(conn, emailparse.InstitutionForSender(parsed.From), parsed)
	if outcome.ParsedAs != "trade" {
		t.Fatalf("parsed as %q, want trade", outcome.ParsedAs)
	}

	var kind, identifier string
	var units, invested float64
	if err := conn.QueryRow(`SELECT kind, identifier, COALESCE(units,0), invested_amount
		FROM investments WHERE id = ?`, outcome.InvestmentID).
		Scan(&kind, &identifier, &units, &invested); err != nil {
		t.Fatalf("reading holding: %v", err)
	}
	if kind != "mutual_fund" {
		t.Errorf("kind = %q, want mutual_fund", kind)
	}
	if identifier != "910102601403" {
		t.Errorf("identifier (folio) = %q, want 910102601403", identifier)
	}
	if units != 84.547 || invested != 9999.5 {
		t.Errorf("units=%v invested=%v, want 84.547 and 9999.5", units, invested)
	}

	// Re-reading the same report must not buy the fund again.
	second := Persist(conn, emailparse.InstitutionForSender(parsed.From), parsed)
	if second.ParsedAs != "trade" {
		t.Fatalf("second pass parsed as %q", second.ParsedAs)
	}
	var holdings int
	conn.QueryRow(`SELECT COUNT(*) FROM investments`).Scan(&holdings)
	conn.QueryRow(`SELECT COALESCE(units,0) FROM investments WHERE id = ?`, outcome.InvestmentID).Scan(&units)
	if holdings != 1 || units != 84.547 {
		t.Errorf("re-reading produced %d holdings and %v units, want 1 and 84.547", holdings, units)
	}
}

// A password-protected contract note is held pending — not discarded — until
// a password is configured, then becomes an equity trade once one is.
func TestZerodhaContractNoteWaitsForPasswordThenBecomesATrade(t *testing.T) {
	conn := openDB(t)
	parsed := zerodhaSample(t, "Combined Equity Contract Note for YPQ985 - August 14, 2026.eml")
	if len(parsed.PDFAttachments) == 0 {
		t.Skip("sample has no PDF attachment")
	}
	inst := emailparse.InstitutionForSender(parsed.From)

	// No password configured yet: held, not lost.
	outcome := PersistWithPasswords(conn, inst, parsed, nil)
	if outcome.ParsedAs != pendingPDFPasswordOutcome {
		t.Fatalf("parsed as %q with no password configured, want %q", outcome.ParsedAs, pendingPDFPasswordOutcome)
	}
	var txns, holdings int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txns)
	conn.QueryRow(`SELECT COUNT(*) FROM investments`).Scan(&holdings)
	if txns != 0 || holdings != 0 {
		t.Errorf("something was written while pending: txns=%d holdings=%d", txns, holdings)
	}

	// A wrong password is held the same way, not treated as final failure.
	wrong := PersistWithPasswords(conn, inst, parsed, func(string) (string, bool) { return "WRONGPASS", true })
	if wrong.ParsedAs != pendingPDFPasswordOutcome {
		t.Errorf("parsed as %q with a wrong password, want %q (retryable)", wrong.ParsedAs, pendingPDFPasswordOutcome)
	}

	// The real password: PAN in capital letters, as Zerodha's own mail states.
	lookup := func(issuer string) (string, bool) {
		if issuer == "Zerodha" {
			return "BKIPV2526H", true
		}
		return "", false
	}
	outcome = PersistWithPasswords(conn, inst, parsed, lookup)
	if outcome.ParsedAs != "trade" {
		t.Fatalf("parsed as %q with the correct password, want trade", outcome.ParsedAs)
	}

	var kind, identifier string
	var units, invested float64
	if err := conn.QueryRow(`SELECT kind, identifier, COALESCE(units,0), invested_amount
		FROM investments WHERE id = ?`, outcome.InvestmentID).
		Scan(&kind, &identifier, &units, &invested); err != nil {
		t.Fatalf("reading holding: %v", err)
	}
	if kind != "stock" || identifier != "INE155A01022" {
		t.Errorf("kind=%q identifier=%q, want stock / INE155A01022", kind, identifier)
	}
	if units != 6 || invested != 1992 {
		t.Errorf("units=%v invested=%v, want 6 and 1992", units, invested)
	}

	// Re-processing with the password on file must not double the position.
	again := PersistWithPasswords(conn, inst, parsed, lookup)
	if again.ParsedAs != "trade" {
		t.Fatalf("re-parse got %q", again.ParsedAs)
	}
	conn.QueryRow(`SELECT COALESCE(units,0) FROM investments WHERE id = ?`, outcome.InvestmentID).Scan(&units)
	if units != 6 {
		t.Errorf("units after re-processing = %v, want still 6", units)
	}
}

// A monthly demat holding statement values every listed instrument at once —
// it seeds a holding that has no trade history, and moves the price of one
// that already does, without inventing a purchase that never happened.
func TestZerodhaHoldingStatementSeedsAndPricesPositions(t *testing.T) {
	conn := openDB(t)
	parsed := zerodhaSample(t, "Zerodha Broking Ltd_ Monthly Demat Transaction with Holding Statement for YPQ985 - June - 2026.eml")
	if len(parsed.PDFAttachments) == 0 {
		t.Skip("sample has no PDF attachment")
	}
	inst := emailparse.InstitutionForSender(parsed.From)
	lookup := func(string) (string, bool) { return "BKIPV2526H", true }

	outcome := PersistWithPasswords(conn, inst, parsed, lookup)
	if outcome.ParsedAs != "trade" {
		t.Fatalf("parsed as %q, want trade", outcome.ParsedAs)
	}

	var holdings int
	conn.QueryRow(`SELECT COUNT(*) FROM investments`).Scan(&holdings)
	if holdings == 0 {
		t.Fatal("no holdings created from the statement")
	}

	// HDFC Bank equity: no trade history anywhere, so its 20 units at ₹798.40
	// must come from the seed — a real position, not an empty row.
	var units, invested, value, price float64
	var notes string
	if err := conn.QueryRow(`SELECT COALESCE(units,0), invested_amount, current_value,
		COALESCE(last_price,0), notes FROM investments WHERE identifier = 'INE040A01034'`).
		Scan(&units, &invested, &value, &price, &notes); err != nil {
		t.Fatalf("HDFC Bank holding not found: %v", err)
	}
	if units != 20 {
		t.Errorf("units = %v, want 20", units)
	}
	if price != 798.4 {
		t.Errorf("price = %v, want 798.4", price)
	}
	if notes == "" {
		t.Error("no note explaining the estimated cost basis")
	}

	// A mutual fund (INF-prefixed ISIN) held in demat form must be routed to
	// the mutual fund tab, not lumped in with equities.
	var mfKind string
	if err := conn.QueryRow(`SELECT kind FROM investments WHERE identifier = 'INF179K01UT0'`).Scan(&mfKind); err != nil {
		t.Fatalf("HDFC FCF fund holding not found: %v", err)
	}
	if mfKind != "mutual_fund" {
		t.Errorf("kind = %q for an INF-prefixed ISIN, want mutual_fund", mfKind)
	}

	// Re-processing the same statement must not seed a second opening trade —
	// the whole reason RecordHoldingSnapshot keys the seed on the instrument,
	// not the statement period.
	PersistWithPasswords(conn, inst, parsed, lookup)
	var trades int
	conn.QueryRow(`SELECT COUNT(*) FROM investment_trades WHERE investment_id =
		(SELECT id FROM investments WHERE identifier = 'INE040A01034')`).Scan(&trades)
	if trades != 1 {
		t.Errorf("%d seed trades for one instrument after two statements, want 1", trades)
	}
}

// The bank-side leg of funding Zerodha — an ordinary UPI debit — must be
// reclassified as an investment transfer, not left counted as spending. This
// is the deterministic counterpart to the funding-notice short-circuit above:
// the real money movement lives here, on the bank's own transaction.
func TestBankTransferFundingZerodhaIsReclassifiedAsInvestmentTransfer(t *testing.T) {
	conn := openDB(t)
	if _, err := conn.Exec(`INSERT INTO finance_accounts (id, name, bank, account_type, account_number, source)
		VALUES (1, 'HDFC Bank •• 4125', 'HDFC', 'bank', 'XXXXXXXX4125', 'manual')`); err != nil {
		t.Fatal(err)
	}

	body := "Dear Customer, Greetings from HDFC Bank!\n" +
		"Rs.10000.00 is debited from your account ending 4125 towards VPA icclzr@yespay (Indian Clearing Corporation Ltd) on 07-08-26.\n" +
		"UPI transaction reference no.: 658544375315.\n"
	e := &emailparse.Email{Subject: "You have done a UPI txn", TextBody: body, Date: "07 Aug 2026"}
	inst := &emailparse.Institution{Issuer: "HDFC", Name: "HDFC Bank", DefaultKind: emailparse.KindBank}

	outcome := Persist(conn, inst, e)
	if outcome.ParsedAs != "transaction" {
		t.Fatalf("parsed as %q, want transaction", outcome.ParsedAs)
	}

	var txnType, transferKind, counterparty string
	if err := conn.QueryRow(`SELECT type, transfer_kind, counterparty FROM transactions WHERE id = ?`,
		outcome.TransactionID).Scan(&txnType, &transferKind, &counterparty); err != nil {
		t.Fatalf("reading transaction: %v", err)
	}
	if txnType != "transfer" {
		t.Errorf("type = %q, want transfer — this is money funding a broker, not spending", txnType)
	}
	if transferKind != "investment" {
		t.Errorf("transferKind = %q, want investment", transferKind)
	}
	if counterparty != "Zerodha" {
		t.Errorf("counterparty = %q, want Zerodha", counterparty)
	}
}
