package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/money/parserules"
	"wollow/backend/internal/platform/db"
)

// fakeFetcher stands in for a live IMAP connection, serving message bodies by
// UID. Ingest is supposed to reach the network only for messages it has
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

// message is one email as it would sit in the mailbox: its raw bytes plus the
// header facts Mail's sync pass indexes.
type message struct {
	uid     uint32
	from    string
	subject string
	body    string
	raw     []byte
	rfcID   string
}

var messageSeq int

func newMessage(uid uint32, from, subject, body string) message {
	messageSeq++
	rfcID := fmt.Sprintf("<msg-%d-%d@example.com>", uid, messageSeq)
	raw := []byte("From: " + from + "\r\nTo: me@example.com\r\nSubject: " + subject +
		"\r\nDate: Tue, 12 Aug 2026 10:00:00 +0530\r\nMessage-ID: " + rfcID +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
	return message{uid: uid, from: from, subject: subject, body: body, raw: raw, rfcID: rfcID}
}

func domainOfAddress(from string) string {
	if at := strings.LastIndex(from, "@"); at != -1 {
		return strings.ToLower(from[at+1:])
	}
	return ""
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

// seedIndex writes the mail_accounts + messages rows that a sync pass would
// have produced for these messages, without any Money-side rows.
func seedIndex(t *testing.T, conn *sql.DB, messages []message) int64 {
	t.Helper()
	res, err := conn.Exec(`
		INSERT INTO mail_accounts (label, imap_host, imap_port, username, encrypted_password)
		VALUES ('Test', 'imap.example.com', 993, 'test@example.com', 'x')`)
	if err != nil {
		t.Fatalf("seed mail account: %v", err)
	}
	accountID, _ := res.LastInsertId()

	for _, m := range messages {
		if _, err := conn.Exec(`
			INSERT INTO messages (account_id, folder, uid, rfc_message_id, subject, from_email, from_domain, date)
			VALUES (?, 'INBOX', ?, ?, ?, ?, ?, '2026-08-12T10:00:00+05:30')`,
			accountID, m.uid, m.rfcID, m.subject, m.from, domainOfAddress(m.from)); err != nil {
			t.Fatalf("seed message %d: %v", m.uid, err)
		}
	}
	return accountID
}

func newFetcher(messages []message) *fakeFetcher {
	byUID := make(map[uint32][]byte, len(messages))
	for _, m := range messages {
		byUID[m.uid] = m.raw
	}
	return &fakeFetcher{byUID: byUID}
}

func addAccount(t *testing.T, conn *sql.DB, name, bank, kind, number string) int64 {
	t.Helper()
	res, err := conn.Exec(`
		INSERT INTO finance_accounts (name, bank, account_type, account_number, currency, source)
		VALUES (?, ?, ?, ?, 'INR', 'manual')`, name, bank, kind, number)
	if err != nil {
		t.Fatalf("seed account %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

type mark struct {
	name  string
	value string
}

func spansFor(t *testing.T, text string, marks []mark) []parserules.Span {
	t.Helper()
	var out []parserules.Span
	for _, m := range marks {
		at := strings.Index(text, m.value)
		if at == -1 {
			t.Fatalf("%s: %q not found in sample", m.name, m.value)
		}
		start := utf8.RuneCountInString(text[:at])
		out = append(out, parserules.Span{Name: m.name, Start: start, End: start + utf8.RuneCountInString(m.value), Sample: m.value})
	}
	return out
}

// defineRule derives a rule from marks on a sample message and stores it, the
// way the editor does.
func defineRule(t *testing.T, conn *sql.DB, kind parserules.Kind, issuer, domain string, sample message, marks []mark, mutate func(*parserules.Rule)) *parserules.Rule {
	t.Helper()
	text := parserules.SampleText(sample.subject, sample.body)
	fields, err := parserules.Derive(text, spansFor(t, text, marks))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	r := &parserules.Rule{
		Name: string(kind) + " rule", Kind: kind, Enabled: true,
		Issuer: issuer, SenderDomain: domain, AccountType: "bank", Fields: fields,
		Sample: parserules.Sample{Subject: sample.subject, From: sample.from, Text: text},
	}
	if mutate != nil {
		mutate(r)
	}
	if _, err := parserules.Insert(conn, r); err != nil {
		t.Fatalf("insert rule: %v", err)
	}
	return r
}

const hdfcFrom = "alerts@hdfcbank.net"

func hdfcDebit(uid uint32, amount, payee, ref string) message {
	return newMessage(uid, hdfcFrom, "You have done a UPI txn. Check details!",
		"Dear Customer,\nRs."+amount+" is debited from your account ending 4125 towards VPA "+
			strings.ToLower(payee)+"@ybl ("+payee+") on 12-08-26.\nUPI transaction reference no.: "+ref+".\n"+
			"If you did not authorize this transaction, click here to modify or unsubscribe from Insta Alerts.")
}

var hdfcMarks = []mark{
	{"amount", "1234.00"}, {"account_last4", "4125"}, {"counterparty", "SWIGGY"},
	{"date", "12-08-26"}, {"reference", "123456789012"},
}

func count(t *testing.T, conn *sql.DB, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// A rule defined on one alert reads the next: the transaction is written to
// the account the alert names, and the link records which rule read it.
func TestRuleReadsTransactionAndStampsRule(t *testing.T) {
	conn := openDB(t)
	sample := hdfcDebit(101, "1234.00", "SWIGGY", "123456789012")
	msg := hdfcDebit(102, "500.00", "ZOMATO", "987654321098")
	mailbox := seedIndex(t, conn, []message{msg})
	accountID := addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")
	rule := defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", sample, hdfcMarks, nil)

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.Scanned != 1 || result.Transactions != 1 {
		t.Fatalf("result = %+v, want 1 scanned, 1 transaction", result)
	}

	var amount float64
	var merchant, txnDate, method string
	var linkedAccount int64
	if err := conn.QueryRow(`SELECT withdrawal_amt, merchant, txn_date, payment_method, account_id FROM transactions`).
		Scan(&amount, &merchant, &txnDate, &method, &linkedAccount); err != nil {
		t.Fatalf("no transaction: %v", err)
	}
	if amount != 500 || merchant != "ZOMATO" || txnDate != "2026-08-12" || method != "UPI" || linkedAccount != accountID {
		t.Errorf("transaction = %.2f %q %s %q account %d", amount, merchant, txnDate, method, linkedAccount)
	}

	var parsedAs string
	var ruleID sql.NullInt64
	conn.QueryRow(`SELECT parsed_as, rule_id FROM message_links`).Scan(&parsedAs, &ruleID)
	if parsedAs != "transaction" || !ruleID.Valid || ruleID.Int64 != rule.ID {
		t.Errorf("link = %q rule %v, want transaction by rule %d", parsedAs, ruleID, rule.ID)
	}
	if n := count(t, conn, `SELECT match_count FROM email_parser_rules WHERE id = ?`, rule.ID); n != 1 {
		t.Errorf("match_count = %d, want 1", n)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM finance_accounts`); n != 1 {
		t.Errorf("accounts = %d; ingest must not create any", n)
	}
}

// Mail from a known institution with no rule stays visible as unrecognized;
// mail from an unknown sender with no rule is not even fetched.
func TestNoRuleMeansUnrecognized(t *testing.T) {
	conn := openDB(t)
	bank := newMessage(201, "alerts@icicibank.com", "Transaction alert", "INR 100 spent on card XX7001")
	stranger := newMessage(202, "news@somewhere.example", "Hello", "Nothing financial")
	mailbox := seedIndex(t, conn, []message{bank, stranger})
	fetcher := newFetcher([]message{bank, stranger})

	result, err := Run(context.Background(), conn, fetcher, mailbox, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.Scanned != 1 || result.Unrecognized != 1 {
		t.Errorf("result = %+v, want 1 scanned, 1 unrecognized", result)
	}
	if len(fetcher.requested) != 1 || fetcher.requested[0] != 201 {
		t.Errorf("fetched %v, want only the bank's message", fetcher.requested)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM message_links WHERE parsed_as = 'unrecognized' AND uid = 201`); n != 1 {
		t.Errorf("unrecognized links = %d, want 1", n)
	}
}

// A rule can teach a sender the registry never heard of.
func TestRuleSenderWidensCandidates(t *testing.T) {
	conn := openDB(t)
	sample := newMessage(301, "alerts@smallbank.example", "Debit alert",
		"Rs.250.00 debited from A/c XX9001 to MERCHANT ONE on 12-08-26.")
	msg := newMessage(302, "alerts@smallbank.example", "Debit alert",
		"Rs.75.00 debited from A/c XX9001 to MERCHANT TWO on 13-08-26.")
	mailbox := seedIndex(t, conn, []message{msg})
	addAccount(t, conn, "Small bank", "SmallBank", "bank", "9001")
	defineRule(t, conn, parserules.KindTransaction, "SmallBank", "smallbank.example", sample, []mark{
		{"amount", "250.00"}, {"account_last4", "XX9001"}, {"counterparty", "MERCHANT ONE"}, {"date", "12-08-26"},
	}, nil)

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.Transactions != 1 {
		t.Fatalf("result = %+v, want 1 transaction", result)
	}
	var merchant string
	var amount float64
	conn.QueryRow(`SELECT merchant, withdrawal_amt FROM transactions`).Scan(&merchant, &amount)
	if merchant != "MERCHANT TWO" || amount != 75 {
		t.Errorf("transaction = %q %.2f", merchant, amount)
	}
}

// A message naming an account nobody registered is held — with what it said
// about the account — and imported once that account exists. No account is
// ever created by ingest.
func TestMissingAccountIsHeldThenImported(t *testing.T) {
	conn := openDB(t)
	sample := hdfcDebit(401, "1234.00", "SWIGGY", "123456789012")
	msg := hdfcDebit(402, "500.00", "ZOMATO", "987654321098")
	mailbox := seedIndex(t, conn, []message{msg})
	defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", sample, hdfcMarks, nil)

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.PendingAccount != 1 || result.Transactions != 0 {
		t.Fatalf("result = %+v, want 1 pending", result)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM finance_accounts`); n != 0 {
		t.Fatalf("ingest created %d accounts", n)
	}
	var issuer, name, last4, kind string
	if err := conn.QueryRow(`SELECT pending_issuer, pending_name, pending_last4, pending_kind
		FROM message_links WHERE parsed_as = 'pending_account'`).Scan(&issuer, &name, &last4, &kind); err != nil {
		t.Fatalf("no pending link: %v", err)
	}
	if issuer != "HDFC" || name != "HDFC Bank" || last4 != "4125" || kind != "bank" {
		t.Errorf("pending hint = %q %q %q %q", issuer, name, last4, kind)
	}

	// A second pass changes nothing: the message is linked, so it is not a
	// candidate, and nothing is fetched.
	fetcher := newFetcher([]message{msg})
	if _, err := Run(context.Background(), conn, fetcher, mailbox, "INBOX"); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Errorf("held message was fetched again")
	}

	// The user adds the account. Releasing the held mail makes it a candidate
	// again, and the next pass imports it.
	addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "4125")
	released, err := ClearPendingFor(conn, "HDFC", "4125")
	if err != nil || released != 1 {
		t.Fatalf("released %d (%v), want 1", released, err)
	}
	result, err = Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if result.Transactions != 1 {
		t.Errorf("result after adding the account = %+v, want 1 transaction", result)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM message_links WHERE parsed_as = 'pending_account'`); n != 0 {
		t.Errorf("%d pending links remain", n)
	}
}

// A digit-less hint (a wallet) is released on the institution, however the
// user typed it.
func TestClearPendingForMatchesInstitutionName(t *testing.T) {
	conn := openDB(t)
	mailbox := seedIndex(t, conn, nil)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as, pending_issuer, pending_name)
		VALUES (?, '<w1>', 'pending_account', 'AmazonPay', 'Amazon Pay')`, mailbox)

	if n, _ := ClearPendingFor(conn, "Some other bank", ""); n != 0 {
		t.Errorf("released %d for an unrelated bank", n)
	}
	if n, _ := ClearPendingFor(conn, "amazon pay", ""); n != 1 {
		t.Errorf("released %d for the display name, want 1", n)
	}
}

func TestRescanStuckClearsPending(t *testing.T) {
	conn := openDB(t)
	mailbox := seedIndex(t, conn, nil)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as) VALUES (?, '<a>', 'pending_account')`, mailbox)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as) VALUES (?, '<b>', 'unrecognized')`, mailbox)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as) VALUES (?, '<c>', 'balance')`, mailbox)

	cleared, err := RescanStuck(conn, mailbox)
	if err != nil || cleared != 2 {
		t.Fatalf("cleared %d (%v), want 2", cleared, err)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM message_links`); n != 1 {
		t.Errorf("%d links remain, want the balance link only", n)
	}
}

// ClearForRule releases only the unread mail from the rule's own sender.
func TestClearForRuleReleasesThatSendersMail(t *testing.T) {
	conn := openDB(t)
	mailbox := seedIndex(t, conn, nil)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as, sender) VALUES (?, '<a>', 'unrecognized', 'alerts@hdfcbank.net')`, mailbox)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as, sender) VALUES (?, '<b>', 'unrecognized', 'alerts@axisbank.com')`, mailbox)
	conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as, sender) VALUES (?, '<c>', 'transaction', 'alerts@hdfcbank.net')`, mailbox)

	n, err := ClearForRule(conn, mailbox, &parserules.Rule{SenderDomain: "hdfcbank.net"})
	if err != nil || n != 1 {
		t.Fatalf("cleared %d (%v), want 1", n, err)
	}
}

// The same alert delivered twice (two Message-IDs) is one transaction, and
// both messages link to it.
func TestDuplicateAlertLinksToTheExistingTransaction(t *testing.T) {
	conn := openDB(t)
	sample := hdfcDebit(501, "1234.00", "SWIGGY", "123456789012")
	first := hdfcDebit(502, "500.00", "ZOMATO", "987654321098")
	second := hdfcDebit(503, "500.00", "ZOMATO", "987654321098")
	mailbox := seedIndex(t, conn, []message{first, second})
	addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")
	defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", sample, hdfcMarks, nil)

	if _, err := Run(context.Background(), conn, newFetcher([]message{first, second}), mailbox, "INBOX"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM transactions`); n != 1 {
		t.Errorf("transactions = %d, want 1", n)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM message_links WHERE parsed_as = 'transaction' AND transaction_id IS NOT NULL`); n != 2 {
		t.Errorf("linked messages = %d, want both", n)
	}
}

func TestArchivedAccountIsNotMatched(t *testing.T) {
	conn := openDB(t)
	sample := hdfcDebit(601, "1234.00", "SWIGGY", "123456789012")
	msg := hdfcDebit(602, "500.00", "ZOMATO", "987654321098")
	mailbox := seedIndex(t, conn, []message{msg})
	id := addAccount(t, conn, "Old HDFC", "HDFC", "bank", "XXXXXXXX4125")
	conn.Exec(`UPDATE finance_accounts SET archived_at = '2026-01-01T00:00:00Z' WHERE id = ?`, id)
	defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", sample, hdfcMarks, nil)

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.PendingAccount != 1 || result.Transactions != 0 {
		t.Errorf("result = %+v, want the message held", result)
	}
}

func TestBalanceRuleRecordsSnapshot(t *testing.T) {
	conn := openDB(t)
	sample := newMessage(701, hdfcFrom, "Balance update",
		"The available balance in your account ending XX4125 is Rs. INR 2,08,870.09 as of 12-AUG-26.")
	msg := newMessage(702, hdfcFrom, "Balance update",
		"The available balance in your account ending XX4125 is Rs. INR 1,500.00 as of 20-AUG-26.")
	mailbox := seedIndex(t, conn, []message{msg})
	accountID := addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")
	defineRule(t, conn, parserules.KindBalance, "HDFC", "hdfcbank.net", sample, []mark{
		{"balance", "2,08,870.09"}, {"account_last4", "XX4125"}, {"as_of", "12-AUG-26"},
	}, nil)

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.Balances != 1 {
		t.Fatalf("result = %+v, want 1 balance", result)
	}
	var asOf string
	var balance, current float64
	conn.QueryRow(`SELECT as_of, balance FROM balance_snapshots WHERE account_id = ?`, accountID).Scan(&asOf, &balance)
	conn.QueryRow(`SELECT current_balance FROM finance_accounts WHERE id = ?`, accountID).Scan(&current)
	if asOf != "2026-08-20" || balance != 1500 || current != 1500 {
		t.Errorf("snapshot %s %.2f, current %.2f", asOf, balance, current)
	}
}

func TestBillRuleUpsertsStatement(t *testing.T) {
	conn := openDB(t)
	from := "statements@icicibank.com"
	body := func(total string) string {
		return "Dear Customer,\nYour credit card ending in 7001 statement is ready.\nTotal Amount Due:\n" + total +
			"\nMinimum Amount Due: INR 1,234.00\nPayment due\nby 05 August, 2026\nThank you"
	}
	subject := "Your ICICI Bank Credit Card Statement for the period 12-Jul-2026 to 11-Aug-2026"
	sample := newMessage(801, from, subject, body("INR 12,345.00"))
	first := newMessage(802, from, subject, body("INR 12,345.00"))
	resend := newMessage(803, from, subject, body("INR 12,345.00"))
	mailbox := seedIndex(t, conn, []message{first, resend})
	// Unrelated links push message_links rowids past the bill's, so a resend linked
	// through a stale last_insert_rowid would point at no bill at all.
	for i := 0; i < 3; i++ {
		conn.Exec(`INSERT INTO message_links (mail_account_id, rfc_message_id, parsed_as) VALUES (?, ?, 'unrecognized')`,
			mailbox, fmt.Sprintf("<other-%d>", i))
	}
	card := addAccount(t, conn, "ICICI card", "ICICI", "credit_card", "XXXXXXXX7001")
	defineRule(t, conn, parserules.KindBill, "ICICI", "icicibank.com", sample, []mark{
		{"card_last4", "7001"}, {"total_due", "12,345.00"}, {"minimum_due", "1,234.00"},
		{"due_date", "05 August, 2026"}, {"statement_period", "12-Jul-2026 to 11-Aug-2026"},
	}, func(r *parserules.Rule) { r.AccountType = "credit_card" })

	result, err := Run(context.Background(), conn, newFetcher([]message{first, resend}), mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.Bills != 2 || result.Failed != 0 {
		t.Fatalf("result = %+v, want both statement mails read as bills and none failed", result)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM bills`); n != 1 {
		t.Errorf("bills = %d, want the resend merged into 1", n)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM message_links WHERE parsed_as = 'bill' AND bill_id = (SELECT id FROM bills)`); n != 2 {
		t.Errorf("%d statement mails link to the bill, want 2", n)
	}
	var total float64
	var due string
	var accountID int64
	conn.QueryRow(`SELECT total_due, due_date, account_id FROM bills`).Scan(&total, &due, &accountID)
	if total != 12345 || due != "2026-08-05" || accountID != card {
		t.Errorf("bill = %.2f due %q account %d", total, due, accountID)
	}
}

func TestTradeRuleBecomesAHolding(t *testing.T) {
	conn := openDB(t)
	from := "orders@indmoney.com"
	sample := newMessage(901, from, "Your BUY order for Apple Inc for $245.73 is successful",
		"Ticker: Apple Inc Amount: $245.73 Price: $245.73 Shares: 1 Order Type: Market US a/c: 12AB34")
	msg := newMessage(902, from, "Your BUY order for Tesla Inc for $100.50 is successful",
		"Ticker: Tesla Inc Amount: $100.50 Price: $50.25 Shares: 2 Order Type: Market US a/c: 12AB34")
	mailbox := seedIndex(t, conn, []message{msg})

	text := parserules.SampleText(sample.subject, sample.body)
	bodyAt := strings.Index(text, "Ticker:")
	spans := spansFor(t, text[bodyAt:], []mark{{"symbol", "Apple Inc"}, {"amount", "245.73"}, {"units", "1"}})
	offset := utf8.RuneCountInString(text[:bodyAt])
	for i := range spans {
		spans[i].Start += offset
		spans[i].End += offset
	}
	spans = append(spans, spansFor(t, text, []mark{{"side_word", "BUY"}})...)
	fields, err := parserules.Derive(text, spans)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	rule := &parserules.Rule{
		Name: "INDmoney orders", Kind: parserules.KindTrade, Enabled: true, Issuer: "INDmoney",
		SenderDomain: "indmoney.com", AccountType: "investment", Fields: fields,
		Attributes: parserules.Attributes{Currency: "USD", InstrumentKind: "us_stock"},
	}
	if _, err := parserules.Insert(conn, rule); err != nil {
		t.Fatal(err)
	}

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.Trades != 1 {
		t.Fatalf("result = %+v, want 1 trade", result)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM transactions`); n != 0 {
		t.Errorf("a broker order became %d ledger transactions", n)
	}
	var name, currency string
	var units float64
	if err := conn.QueryRow(`SELECT name, currency, COALESCE(units, 0) FROM investments`).Scan(&name, &currency, &units); err != nil {
		t.Fatalf("no holding: %v", err)
	}
	if name != "Tesla Inc" || currency != "USD" || units != 2 {
		t.Errorf("holding = %q %s %.2f", name, currency, units)
	}
	if n := count(t, conn, `SELECT COUNT(*) FROM investment_trades`); n != 1 {
		t.Errorf("trades = %d, want 1", n)
	}
}

// One unreadable batch must not starve the messages behind it.
func TestFailedFetchDoesNotStarveLaterMessages(t *testing.T) {
	conn := openDB(t)
	sample := hdfcDebit(1001, "1234.00", "SWIGGY", "123456789012")
	messages := []message{
		hdfcDebit(1002, "10.00", "ONE", "111111111111"),
		hdfcDebit(1003, "20.00", "TWO", "222222222222"),
		hdfcDebit(1004, "30.00", "THREE", "333333333333"),
	}
	mailbox := seedIndex(t, conn, messages)
	addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")
	defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", sample, hdfcMarks, nil)

	old := batchSizeForTest
	batchSizeForTest = 1
	t.Cleanup(func() { batchSizeForTest = old })

	fetcher := newFetcher(messages)
	fetcher.failUID = 1002
	result, err := Run(context.Background(), conn, fetcher, mailbox, "INBOX")
	if err != nil {
		t.Fatalf("a failed batch must not fail the pass: %v", err)
	}
	if result.Failed != 1 || result.Transactions != 2 {
		t.Errorf("result = %+v, want 1 failed and the 2 behind it imported", result)
	}

	// The failed message stays unlinked, so a later pass picks it up.
	fetcher.failUID = 0
	result, err = Run(context.Background(), conn, fetcher, mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.Transactions != 1 {
		t.Errorf("retry result = %+v, want the failed message imported", result)
	}
}

// The AI classifier can flag a message from a sender no registry or rule
// names; it is then examined and, with nothing to read it, left visible as
// unrecognized rather than skipped.
func TestClassifierWidensTheNet(t *testing.T) {
	conn := openDB(t)
	msg := newMessage(1101, "billing@newfintech.example", "Payment received", "We received your payment of Rs 999.")
	mailbox := seedIndex(t, conn, []message{msg})
	var messageID int64
	conn.QueryRow(`SELECT id FROM messages WHERE uid = 1101`).Scan(&messageID)
	if _, err := conn.Exec(`INSERT INTO classifications (message_id, category, is_transactional, model)
		VALUES (?, 'finance', 1, 'test')`, messageID); err != nil {
		t.Fatal(err)
	}

	result, err := Run(context.Background(), conn, newFetcher([]message{msg}), mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Unrecognized != 1 {
		t.Errorf("result = %+v, want the flagged message scanned and unrecognized", result)
	}
}

// A rule whose sender matches but whose values this message doesn't carry
// falls through to the next rule, so one sender can have several templates.
func TestRulesFallThroughBySubject(t *testing.T) {
	conn := openDB(t)
	debitSample := hdfcDebit(1201, "1234.00", "SWIGGY", "123456789012")
	balanceSample := newMessage(1202, hdfcFrom, "Balance update",
		"The available balance in your account ending XX4125 is Rs. INR 2,08,870.09 as of 12-AUG-26.")
	debit := hdfcDebit(1203, "500.00", "ZOMATO", "987654321098")
	balance := newMessage(1204, hdfcFrom, "Balance update",
		"The available balance in your account ending XX4125 is Rs. INR 1,500.00 as of 20-AUG-26.")
	mailbox := seedIndex(t, conn, []message{debit, balance})
	addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")
	defineRule(t, conn, parserules.KindBalance, "HDFC", "hdfcbank.net", balanceSample, []mark{
		{"balance", "2,08,870.09"}, {"account_last4", "XX4125"}, {"as_of", "12-AUG-26"},
	}, func(r *parserules.Rule) { r.Priority = 10 })
	defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", debitSample, hdfcMarks, nil)

	result, err := Run(context.Background(), conn, newFetcher([]message{debit, balance}), mailbox, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if result.Transactions != 1 || result.Balances != 1 {
		t.Errorf("result = %+v, want one of each", result)
	}
}
