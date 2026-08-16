package txnclassify

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"wollow/backend/internal/mail/ai"
)

// fakeProvider answers with canned model output, keyed by a substring of the
// prompt so one test can drive several transactions through a single pass.
type fakeProvider struct {
	ai.NoopProvider
	answers map[string]string
	fail    bool
}

// promptNarration pulls out the transaction's own narration line.
//
// Matching against the whole prompt does not work: the instructions carry
// worked examples naming real merchants ("UPI-SWIGGY-…" -> "Swiggy",
// "NEFT Cr-…-MANASH E COMMERCE"), so every prompt contains those words and a
// fixture keyed on them answers the same way for every transaction.
func promptNarration(prompt string) string {
	const marker = "Narration:"
	i := strings.Index(prompt, marker)
	if i == -1 {
		return ""
	}
	rest := prompt[i+len(marker):]
	if end := strings.IndexByte(rest, '\n'); end != -1 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func (f fakeProvider) Complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	if f.fail {
		return "", context.DeadlineExceeded
	}
	narration := promptNarration(prompt)
	for marker, answer := range f.answers {
		if strings.Contains(narration, marker) {
			return answer, nil
		}
	}
	return `{"category":"","nature":"expense","needs_review":true,"confidence":0.1}`, nil
}

// countingProvider records how many times the model was actually called, which
// is the point of grouping.
type countingProvider struct {
	ai.NoopProvider
	answers map[string]string
	mu      sync.Mutex
	count   int
}

func (c *countingProvider) Complete(ctx context.Context, prompt string, maxTokens int) (string, error) {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	narration := promptNarration(prompt)
	for marker, answer := range c.answers {
		if strings.Contains(narration, marker) {
			return answer, nil
		}
	}
	return `{"category":"","nature":"expense","needs_review":true}`, nil
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Opened the way platform/db does. The pass classifies concurrently, and
	// modernc.org/sqlite needs writes serialized — a pool of more than one
	// connection makes a worker fail with "database is locked" instead of
	// storing its answer.
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/test.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })

	schema := `
	CREATE TABLE finance_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, account_type TEXT);
	CREATE TABLE categories (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT UNIQUE, type TEXT, sort_order INTEGER DEFAULT 0);
	CREATE TABLE transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER, txn_date TEXT, narration TEXT DEFAULT '', ref_no TEXT DEFAULT '',
		withdrawal_amt REAL DEFAULT 0, deposit_amt REAL DEFAULT 0, closing_balance REAL,
		type TEXT DEFAULT 'expense', category_id INTEGER, merchant TEXT DEFAULT '',
		payment_method TEXT DEFAULT '', transfer_kind TEXT DEFAULT '', counterparty TEXT DEFAULT '',
		notes TEXT DEFAULT '');
	CREATE TABLE transaction_classifications (
		transaction_id INTEGER PRIMARY KEY, category TEXT DEFAULT '', subcategory TEXT DEFAULT '',
		merchant TEXT DEFAULT '', payment_method TEXT DEFAULT '', nature TEXT DEFAULT '',
		transfer_kind TEXT DEFAULT '', counterparty TEXT DEFAULT '', is_recurring INTEGER DEFAULT 0,
		is_bill INTEGER DEFAULT 0, is_refund INTEGER DEFAULT 0, needs_review INTEGER DEFAULT 0,
		confidence REAL DEFAULT 0, summary TEXT DEFAULT '', model TEXT DEFAULT '',
		classified_at TEXT DEFAULT '', applied INTEGER DEFAULT 0);

	INSERT INTO finance_accounts (id, name, account_type) VALUES (1, 'HDFC Savings', 'bank');
	INSERT INTO categories (name, type, sort_order) VALUES
		('Food & Dining','expense',1), ('Shopping','expense',2), ('Salary','income',3);`
	if _, err := database.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestRunClassifiesAndFillsEmptyFields(t *testing.T) {
	database := newTestDB(t)
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
		VALUES (1, 1, '2026-08-05', 'UPI-SWIGGY-9871@ybl-PAYMENT', 450, 'expense')`)

	provider := fakeProvider{answers: map[string]string{
		"SWIGGY": `{"category":"Food & Dining","subcategory":"food delivery","merchant":"Swiggy",
			"payment_method":"UPI","nature":"expense","is_recurring":false,
			"confidence":0.95,"summary":"Food delivery order paid by UPI"}`,
	}}

	result, err := Run(context.Background(), database, provider, "test-model", nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Classified != 1 || result.Applied != 1 {
		t.Fatalf("got %+v, want 1 classified and 1 applied", result)
	}

	var categoryName, merchant, method string
	err = database.QueryRow(`
		SELECT COALESCE(c.name,''), t.merchant, t.payment_method
		FROM transactions t LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.id = 1`).Scan(&categoryName, &merchant, &method)
	if err != nil {
		t.Fatal(err)
	}
	if categoryName != "Food & Dining" {
		t.Errorf("category = %q, want Food & Dining", categoryName)
	}
	if merchant != "Swiggy" {
		t.Errorf("merchant = %q, want Swiggy", merchant)
	}
	if method != "UPI" {
		t.Errorf("payment method = %q, want UPI", method)
	}

	var confidence float64
	var summary string
	database.QueryRow(`SELECT confidence, summary FROM transaction_classifications WHERE transaction_id = 1`).
		Scan(&confidence, &summary)
	if confidence != 0.95 || summary == "" {
		t.Errorf("stored classification = %v/%q, want the model's confidence and summary", confidence, summary)
	}
}

// The user's own edits are the point of the whole product: a classification
// pass must never overwrite a category or merchant a person already set.
func TestRunNeverOverwritesUserValues(t *testing.T) {
	database := newTestDB(t)
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type, category_id, merchant)
		VALUES (1, 1, '2026-08-05', 'UPI-SWIGGY', 450, 'expense',
			(SELECT id FROM categories WHERE name='Shopping'), 'My Own Label')`)

	provider := fakeProvider{answers: map[string]string{
		"SWIGGY": `{"category":"Food & Dining","merchant":"Swiggy","nature":"expense","confidence":0.99}`,
	}}
	if _, err := Run(context.Background(), database, provider, "m", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var categoryName, merchant string
	database.QueryRow(`
		SELECT COALESCE(c.name,''), t.merchant FROM transactions t
		LEFT JOIN categories c ON c.id = t.category_id WHERE t.id = 1`).Scan(&categoryName, &merchant)
	if categoryName != "Shopping" {
		t.Errorf("category = %q, want the user's Shopping to survive", categoryName)
	}
	if merchant != "My Own Label" {
		t.Errorf("merchant = %q, want the user's label to survive", merchant)
	}
}

// A detected transfer is stored as a suggestion but must not silently change
// the transaction's type — that would move money out of the expense totals
// with no one having asked.
func TestRunStoresTransferSuggestionWithoutApplyingIt(t *testing.T) {
	database := newTestDB(t)
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
		VALUES (1, 1, '2026-08-10', 'NEFT-ZERODHA BROKING', 25000, 'expense')`)

	provider := fakeProvider{answers: map[string]string{
		"ZERODHA": `{"category":"","nature":"transfer","transfer_kind":"investment",
			"counterparty":"Zerodha","confidence":0.9,"summary":"Money moved into a broking account"}`,
	}}
	if _, err := Run(context.Background(), database, provider, "m", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var txnType, kind string
	database.QueryRow(`SELECT type, transfer_kind FROM transactions WHERE id = 1`).Scan(&txnType, &kind)
	if txnType != "expense" || kind != "" {
		t.Errorf("transaction = %q/%q, want it untouched until the user applies", txnType, kind)
	}

	var nature, suggestedKind, counterparty string
	database.QueryRow(`SELECT nature, transfer_kind, counterparty FROM transaction_classifications
		WHERE transaction_id = 1`).Scan(&nature, &suggestedKind, &counterparty)
	if nature != "transfer" || suggestedKind != "investment" || counterparty != "Zerodha" {
		t.Errorf("suggestion = %q/%q/%q, want the transfer reading stored", nature, suggestedKind, counterparty)
	}
}

// Filling a blank field must not mark the suggestion resolved. The UI hides
// the accept/ignore prompt on an applied row, so a transfer that also filled
// in a merchant would otherwise become invisible — detected, and then silently
// never offered to the user.
func TestOutstandingSuggestionStaysUnapplied(t *testing.T) {
	database := newTestDB(t)
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
		VALUES (1, 1, '2026-08-10', 'NEFT-ZERODHA', 25000, 'expense')`)

	provider := fakeProvider{answers: map[string]string{
		"ZERODHA": `{"category":"","merchant":"Zerodha","payment_method":"NEFT","nature":"transfer",
			"transfer_kind":"investment","counterparty":"Zerodha","confidence":0.93}`,
	}}
	if _, err := Run(context.Background(), database, provider, "m", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The safe fill still happened...
	var merchant string
	database.QueryRow(`SELECT merchant FROM transactions WHERE id = 1`).Scan(&merchant)
	if merchant != "Zerodha" {
		t.Errorf("merchant = %q, want the blank field filled", merchant)
	}

	// ...but the transfer is still waiting on a person.
	var applied int
	database.QueryRow(`SELECT applied FROM transaction_classifications WHERE transaction_id = 1`).Scan(&applied)
	if applied != 0 {
		t.Error("applied = 1, want the outstanding transfer suggestion still offered to the user")
	}
}

// The opposite case: a reading that agrees with the transaction and is sure of
// itself has nothing left to confirm, so it must not nag.
func TestAgreeingConfidentReadingIsMarkedApplied(t *testing.T) {
	database := newTestDB(t)
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
		VALUES (1, 1, '2026-08-05', 'UPI-SWIGGY', 450, 'expense')`)

	provider := fakeProvider{answers: map[string]string{
		"SWIGGY": `{"category":"Food & Dining","merchant":"Swiggy","nature":"expense","confidence":0.96}`,
	}}
	if _, err := Run(context.Background(), database, provider, "m", nil, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var applied int
	database.QueryRow(`SELECT applied FROM transaction_classifications WHERE transaction_id = 1`).Scan(&applied)
	if applied != 1 {
		t.Error("applied = 0, want an agreeing confident reading to need no confirmation")
	}
}

// One narration, one question. A narration names the payee and the rail, so
// every transaction carrying it is the same kind of payment — asking about
// each occurrence separately costs a model call per row and invites the
// answers to disagree with each other.
func TestOneCallPerNarrationCoversEveryOccurrence(t *testing.T) {
	database := newTestDB(t)
	// The same payee four times, plus one other payment.
	for i, d := range []string{"2026-08-01", "2026-07-01", "2026-06-01", "2026-05-01"} {
		database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
			VALUES (?, 1, ?, 'UPI-Vyapar-Vyapar', 2500, 'expense')`, i+1, d)
	}
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
		VALUES (5, 1, '2026-08-02', 'UPI-SWIGGY-9871@ybl', 450, 'expense')`)

	calls := &countingProvider{answers: map[string]string{
		"Vyapar": `{"category":"Shopping","merchant":"Vyapar","nature":"expense","confidence":0.9}`,
		"SWIGGY": `{"category":"Food & Dining","merchant":"Swiggy","nature":"expense","confidence":0.95}`,
	}}

	result, err := Run(context.Background(), database, calls, "m", nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	t.Logf("result: %+v  calls=%d", result, calls.count)

	if calls.count != 2 {
		t.Errorf("made %d model calls for 2 distinct narrations, want 2", calls.count)
	}
	if result.Narrations != 2 {
		t.Errorf("narrations = %d, want 2", result.Narrations)
	}
	if result.Classified != 5 {
		t.Errorf("classified = %d, want all 5 transactions covered", result.Classified)
	}

	// Every occurrence must carry the category, not just the one asked about.
	var tagged int
	database.QueryRow(`SELECT COUNT(*) FROM transactions t JOIN categories c ON c.id = t.category_id
		WHERE t.narration = 'UPI-Vyapar-Vyapar' AND c.name = 'Shopping'`).Scan(&tagged)
	if tagged != 4 {
		t.Errorf("%d of 4 Vyapar rows categorised, want all of them", tagged)
	}

	// And each row keeps its own classification record.
	var records int
	database.QueryRow(`SELECT COUNT(*) FROM transaction_classifications`).Scan(&records)
	if records != 5 {
		t.Errorf("stored %d classifications, want one per transaction (5)", records)
	}
}

// Case and spacing differences in the same narration must not split the group.
func TestNarrationGroupingIgnoresCaseAndSpacing(t *testing.T) {
	groups := groupByNarration([]Txn{
		{ID: 1, Narration: "UPI-Vyapar-Vyapar"},
		{ID: 2, Narration: "upi-vyapar-vyapar"},
		{ID: 3, Narration: "UPI-Vyapar-Vyapar  "},
		{ID: 4, Narration: "UPI-Other-Other"},
	})
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if len(groups[0].members) != 3 {
		t.Errorf("first group has %d members, want 3", len(groups[0].members))
	}
}

// With no narration the merchant identifies the payment; with neither, the
// transaction must still be read rather than silently folded into a group.
func TestNarrationGroupingFallsBackSafely(t *testing.T) {
	groups := groupByNarration([]Txn{
		{ID: 1, Merchant: "Swiggy"},
		{ID: 2, Merchant: "Swiggy"},
		{ID: 3},
		{ID: 4},
	})
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3 (one merchant group + two singletons)", len(groups))
	}
}

// A category the user doesn't have must not be coerced into a neighbour: a
// confidently wrong label is worse than an admitted gap.
func TestParseRejectsUnknownCategory(t *testing.T) {
	allowed := map[string]string{"food & dining": "Food & Dining"}

	c, err := Parse(`{"category":"Groceries","nature":"expense","confidence":0.9}`, allowed, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if c.Category != "" {
		t.Errorf("category = %q, want it dropped", c.Category)
	}
	if !c.NeedsReview {
		t.Error("needs_review = false, want an unknown category flagged for review")
	}

	// A known category differing only in case still matches, and is stored
	// under its canonical name.
	c, err = Parse(`{"category":"food & DINING","nature":"expense"}`, allowed, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if c.Category != "Food & Dining" {
		t.Errorf("category = %q, want canonical Food & Dining", c.Category)
	}
}

// A transfer has no spending category, so an empty one is the correct answer
// rather than a gap. Flagging it would put every correctly-read transfer into
// the review queue and train the user to ignore the queue.
func TestParseDoesNotFlagTransfersForMissingCategory(t *testing.T) {
	allowed := map[string]string{"food & dining": "Food & Dining"}

	c, err := Parse(`{"category":"","nature":"transfer","transfer_kind":"investment",
		"counterparty":"Zerodha","confidence":0.93}`, allowed, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if c.NeedsReview {
		t.Error("needs_review = true, want a categoryless transfer treated as complete")
	}

	// An expense with no usable category is still a genuine gap.
	c, err = Parse(`{"category":"","nature":"expense","confidence":0.5}`, allowed, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if !c.NeedsReview {
		t.Error("needs_review = false, want an uncategorized expense flagged")
	}
}

func TestParseTolerantOfModelFormatting(t *testing.T) {
	allowed := map[string]string{"salary": "Salary"}
	raw := "Sure! Here's the classification:\n```json\n" +
		`{"category":"Salary","nature":"income","confidence":1.4}` + "\n```\nHope that helps."

	c, err := Parse(raw, allowed, "expense")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Category != "Salary" || c.Nature != "income" {
		t.Errorf("got %q/%q, want Salary/income", c.Category, c.Nature)
	}
	if c.Confidence != 1 {
		t.Errorf("confidence = %v, want it clamped to 1", c.Confidence)
	}
}

// A contradictory answer — a transfer kind on something it called an expense —
// resolves to the nature, not a half-applied transfer.
func TestParseDropsTransferKindOnNonTransfer(t *testing.T) {
	c, err := Parse(`{"category":"","nature":"expense","transfer_kind":"family","counterparty":"Mom"}`,
		map[string]string{}, "expense")
	if err != nil {
		t.Fatal(err)
	}
	if c.TransferKind != "" {
		t.Errorf("transfer_kind = %q, want it cleared on a non-transfer", c.TransferKind)
	}
}

func TestRunSurvivesProviderFailure(t *testing.T) {
	database := newTestDB(t)
	database.Exec(`INSERT INTO transactions (id, account_id, txn_date, narration, withdrawal_amt, type)
		VALUES (1, 1, '2026-08-05', 'ANYTHING', 100, 'expense')`)

	result, err := Run(context.Background(), database, fakeProvider{fail: true}, "m", nil, nil)
	if err != nil {
		t.Fatalf("Run should not surface a per-transaction failure: %v", err)
	}
	if result.Failed != 1 || result.Classified != 0 {
		t.Errorf("got %+v, want the failure counted and nothing stored", result)
	}

	var n int
	database.QueryRow(`SELECT COUNT(*) FROM transaction_classifications`).Scan(&n)
	if n != 0 {
		t.Errorf("stored %d classifications, want none — a failed call must be retried next pass", n)
	}
}

// The model is given the transaction's own details and nothing about the email
// that carried it — no subject, no sender. Anything dropped here is a signal
// the categorizer silently stops having.
func TestBuildPromptCarriesTransactionDetail(t *testing.T) {
	balance := 208974.20
	prompt := BuildPrompt(Txn{
		Date:           "2026-08-05",
		AccountName:    "HDFC Savings",
		AccountType:    "bank",
		Direction:      "debit",
		Amount:         450.50,
		Narration:      "UPI-SWIGGY-9871@ybl",
		RefNo:          "AXI123",
		Merchant:       "Swiggy",
		PaymentMethod:  "UPI",
		ClosingBalance: &balance,
		Notes:          "team lunch",
	}, []string{"Food & Dining"}, []string{"Salary"})

	for _, want := range []string{
		"2026-08-05", "HDFC Savings", "bank", "debit", "450.50",
		"UPI-SWIGGY-9871@ybl", "AXI123", "Swiggy", "UPI", "208974.20",
		"team lunch", "Food & Dining", "Salary",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}
