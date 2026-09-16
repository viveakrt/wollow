package moneyapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"wollow/backend/internal/money/models"
	"wollow/backend/internal/money/parserules"
)

const sampleText = "UPI txn\nRs.1234.00 is debited from your account ending 4125 towards VPA x@y (SWIGGY) on 12-08-26."

// spanJSON locates values in the (ASCII) sample so the test never has to
// count offsets by hand.
func spanJSON(t *testing.T, text string, marks map[string]string) string {
	t.Helper()
	var spans []parserules.Span
	for name, value := range marks {
		at := strings.Index(text, value)
		if at == -1 {
			t.Fatalf("%s: %q not in sample", name, value)
		}
		spans = append(spans, parserules.Span{Name: name, Start: at, End: at + len(value), Sample: value})
	}
	b, _ := json.Marshal(spans)
	return string(b)
}

func ruleBody(t *testing.T, extra string) string {
	t.Helper()
	spans := spanJSON(t, sampleText, map[string]string{
		"amount": "1234.00", "account_last4": "4125", "counterparty": "SWIGGY", "date": "12-08-26",
	})
	sample, _ := json.Marshal(map[string]any{
		"mailAccountId": 1, "uid": 5, "folder": "INBOX", "subject": "UPI txn", "from": "alerts@hdfcbank.net", "text": sampleText,
	})
	return `{"name":"HDFC UPI","kind":"transaction","issuer":"HDFC","senderDomain":"hdfcbank.net","accountType":"bank",
		"attributes":{"direction":"expense"},"sample":` + string(sample) + `,"spans":` + spans + extra + `}`
}

func TestParserRuleCRUDDerivesFields(t *testing.T) {
	server, mux := newTestServer(t)

	created := do(t, mux, "POST", "/api/money/parser-rules", ruleBody(t, ""))
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	rule := decode[parserules.Rule](t, created)
	if len(rule.Fields) != 4 || !rule.Enabled || rule.Sample.Text != sampleText {
		t.Fatalf("created rule = %+v", rule)
	}
	for _, f := range rule.Fields {
		if f.Prefix == "" && f.Suffix == "" && !f.AtLineEnd {
			t.Errorf("field %s has no anchors", f.Name)
		}
	}

	list := do(t, mux, "GET", "/api/money/parser-rules", "")
	rules := decode[[]parserRuleSummary](t, list)
	if len(rules) != 1 || !rules[0].HasSample || rules[0].Sample.Text != "" {
		t.Errorf("list = %+v; want one rule with hasSample and no text", rules)
	}

	// Toggling from the list sends the rule without its sample text; the
	// stored text must survive.
	rules[0].Enabled = false
	toggled, _ := json.Marshal(rules[0].Rule)
	updated := do(t, mux, "PUT", "/api/money/parser-rules/1", string(toggled))
	if updated.Code != http.StatusOK {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	after := decode[parserules.Rule](t, do(t, mux, "GET", "/api/money/parser-rules/1", ""))
	if after.Enabled || after.Sample.Text != sampleText || len(after.Fields) != 4 {
		t.Errorf("after toggle: enabled=%v text=%q fields=%d", after.Enabled, after.Sample.Text, len(after.Fields))
	}

	if w := do(t, mux, "DELETE", "/api/money/parser-rules/1", ""); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}
	if w := do(t, mux, "GET", "/api/money/parser-rules/1", ""); w.Code != http.StatusNotFound {
		t.Errorf("get after delete: %d", w.Code)
	}
	var n int
	server.DB.QueryRow(`SELECT COUNT(*) FROM email_parser_rules`).Scan(&n)
	if n != 0 {
		t.Errorf("%d rules remain", n)
	}
}

func TestParserRuleRejectsUnreadableSample(t *testing.T) {
	_, mux := newTestServer(t)
	// A rule whose only value is the amount, unbound, cannot identify an
	// account — it must be refused rather than saved to fail on every mail.
	spans := spanJSON(t, sampleText, map[string]string{"amount": "1234.00"})
	sample, _ := json.Marshal(map[string]any{"subject": "UPI txn", "text": sampleText})
	body := `{"name":"Broken","kind":"transaction","senderDomain":"hdfcbank.net","sample":` + string(sample) + `,"spans":` + spans + `}`
	if w := do(t, mux, "POST", "/api/money/parser-rules", body); w.Code != http.StatusBadRequest {
		t.Errorf("create: %d %s", w.Code, w.Body.String())
	}
}

func TestParserRuleSampleNeedsAMailSession(t *testing.T) {
	_, mux := newTestServer(t)
	w := do(t, mux, "GET", "/api/money/parser-rules/sample?mailAccountId=1&uid=5", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("sample without a session: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, mux, "GET", "/api/money/parser-rules/sample?mailAccountId=1", ""); w.Code != http.StatusBadRequest {
		t.Errorf("sample without uid: %d", w.Code)
	}
}

func TestParserRuleFieldsCatalogue(t *testing.T) {
	_, mux := newTestServer(t)
	fields := decode[map[string][]parserules.FieldSpec](t, do(t, mux, "GET", "/api/money/parser-rules/fields", ""))
	if len(fields["transaction"]) == 0 || fields["transaction"][0].Name != "amount" || !fields["transaction"][0].Required {
		t.Errorf("transaction fields = %+v", fields["transaction"])
	}
}

func createAccount(t *testing.T, mux *http.ServeMux, body string) models.Account {
	t.Helper()
	w := do(t, mux, "POST", "/api/money/accounts", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create account: %d %s", w.Code, w.Body.String())
	}
	return decode[models.Account](t, w)
}

func TestArchivedAccountIsHiddenUnlessAsked(t *testing.T) {
	_, mux := newTestServer(t)
	a := createAccount(t, mux, `{"name":"Old card","bank":"Axis","accountType":"credit_card","accountNumber":"5792"}`)

	archived := do(t, mux, "POST", "/api/money/accounts/1/archive", "")
	if archived.Code != http.StatusOK || decode[models.Account](t, archived).ArchivedAt == "" {
		t.Fatalf("archive: %d %s", archived.Code, archived.Body.String())
	}

	if got := decode[[]models.Account](t, do(t, mux, "GET", "/api/money/accounts", "")); len(got) != 0 {
		t.Errorf("archived account still listed: %+v", got)
	}
	if got := decode[[]models.Account](t, do(t, mux, "GET", "/api/money/accounts?includeArchived=1", "")); len(got) != 1 {
		t.Errorf("includeArchived listed %d accounts, want 1", len(got))
	}
	summary := decode[map[string]any](t, do(t, mux, "GET", "/api/money/dashboard/summary", ""))
	if accounts, _ := summary["accounts"].([]any); len(accounts) != 0 {
		t.Errorf("dashboard still shows the archived account")
	}

	restored := decode[models.Account](t, do(t, mux, "POST", "/api/money/accounts/1/unarchive", ""))
	if restored.ArchivedAt != "" || restored.ID != a.ID {
		t.Errorf("unarchive = %+v", restored)
	}
	if got := decode[[]models.Account](t, do(t, mux, "GET", "/api/money/accounts", "")); len(got) != 1 {
		t.Errorf("restored account not listed")
	}
}

// A card's outstanding is stated as the positive figure the user sees and
// stored as the negative balance the ledger uses, anchored as a snapshot.
func TestSetBalanceRecordsAManualSnapshot(t *testing.T) {
	server, mux := newTestServer(t)
	createAccount(t, mux, `{"name":"Card","bank":"Axis","accountType":"credit_card","accountNumber":"5792","creditLimit":100000}`)

	w := do(t, mux, "POST", "/api/money/accounts/1/set-balance", `{"amount":2500,"asOf":"2026-08-15"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set-balance: %d %s", w.Code, w.Body.String())
	}
	if got := decode[models.Account](t, w); got.CurrentBalance != -2500 {
		t.Errorf("currentBalance = %v, want -2500", got.CurrentBalance)
	}
	var balance float64
	var source string
	if err := server.DB.QueryRow(`SELECT balance, source FROM balance_snapshots WHERE account_id = 1`).Scan(&balance, &source); err != nil {
		t.Fatalf("no snapshot: %v", err)
	}
	if balance != -2500 || source != "manual" {
		t.Errorf("snapshot = %v %q", balance, source)
	}

	createAccount(t, mux, `{"name":"Savings","bank":"HDFC","accountType":"bank","accountNumber":"4125"}`)
	if got := decode[models.Account](t, do(t, mux, "POST", "/api/money/accounts/2/set-balance", `{"amount":7000}`)); got.CurrentBalance != 7000 {
		t.Errorf("bank balance = %v, want 7000", got.CurrentBalance)
	}
}

func TestPendingAccountsAreGroupedAndReleasedOnCreate(t *testing.T) {
	server, mux := newTestServer(t)
	if _, err := server.DB.Exec(`INSERT INTO mail_accounts (label, imap_host, imap_port, username, encrypted_password)
		VALUES ('m', 'h', 993, 'u', 'x')`); err != nil {
		t.Fatal(err)
	}
	for i, hint := range []struct{ issuer, name, last4, kind string }{
		{"HDFC", "HDFC Bank", "4125", "bank"},
		{"HDFC", "HDFC Bank", "4125", "bank"},
		{"Axis", "Axis Bank", "5792", "credit_card"},
	} {
		if _, err := server.DB.Exec(`INSERT INTO message_links
			(mail_account_id, rfc_message_id, uid, subject, received_at, parsed_as, pending_issuer, pending_name, pending_last4, pending_kind)
			VALUES (1, ?, ?, ?, ?, 'pending_account', ?, ?, ?, ?)`,
			"<p"+string(rune('a'+i))+">", 100+i, "Alert "+string(rune('a'+i)), "2026-08-1"+string(rune('0'+i)),
			hint.issuer, hint.name, hint.last4, hint.kind); err != nil {
			t.Fatal(err)
		}
	}

	groups := decode[[]pendingAccountGroup](t, do(t, mux, "GET", "/api/money/accounts/pending", ""))
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Last4 != "4125" || groups[0].Count != 2 || groups[0].Name != "HDFC Bank" || groups[0].LatestSubject != "Alert b" {
		t.Errorf("first group = %+v", groups[0])
	}
	if groups[0].Sample.UID == 0 {
		t.Errorf("group carries no sample message")
	}

	createAccount(t, mux, `{"name":"HDFC Savings","bank":"HDFC","accountType":"bank","accountNumber":"XXXXXXXX4125"}`)
	groups = decode[[]pendingAccountGroup](t, do(t, mux, "GET", "/api/money/accounts/pending", ""))
	if len(groups) != 1 || groups[0].Last4 != "5792" {
		t.Errorf("after creating the account, groups = %+v", groups)
	}
}
