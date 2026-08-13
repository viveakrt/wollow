package moneyapi

import (
	"os"
	"path/filepath"
	"testing"

	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/platform/db"
)

// TestProcessRealSampleEmails exercises processEmail against the real
// sample .eml files in statements/, verifying the full parse->persist path
// (not just the parser package) works, including dedupe and account
// matching, without needing a live IMAP connection.
func TestProcessRealSampleEmails(t *testing.T) {
	// Repo root, four levels up from internal/money/moneyapi.
	sampleDir := filepath.Join("..", "..", "..", "..", "statements")
	entries, err := os.ReadDir(sampleDir)
	if err != nil {
		// Fail rather than skip: this test silently skipped for a while because
		// the relative path no longer resolved, which read as a green run.
		t.Fatalf("sample dir %s not found: %v", sampleDir, err)
	}

	// db.Open takes the data *directory* and names the file itself.
	conn, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	counts := map[string]int{}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".eml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sampleDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		parsed, err := emailparse.ParseEML(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		issuer := emailparse.IssuerForSender(parsed.From)
		kind, _, _ := processEmail(conn, issuer, parsed)
		counts[kind]++
		t.Logf("%s -> issuer=%s kind=%s", entry.Name(), issuer, kind)
	}

	t.Logf("counts: %+v", counts)
	if counts["transaction"] == 0 {
		t.Error("expected at least one transaction to be extracted from sample emails")
	}
	if counts["bill"] == 0 {
		t.Error("expected at least one bill to be extracted from sample emails")
	}

	var txnCount, billCount int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txnCount)
	conn.QueryRow(`SELECT COUNT(*) FROM bills`).Scan(&billCount)
	if txnCount != counts["transaction"] {
		t.Errorf("db has %d transactions, expected %d", txnCount, counts["transaction"])
	}
	if billCount != counts["bill"] {
		t.Errorf("db has %d bills, expected %d", billCount, counts["bill"])
	}

	// Re-processing the same emails must not create duplicates.
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".eml" {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(sampleDir, entry.Name()))
		parsed, _ := emailparse.ParseEML(raw)
		issuer := emailparse.IssuerForSender(parsed.From)
		processEmail(conn, issuer, parsed)
	}
	var txnCount2 int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txnCount2)
	if txnCount2 != txnCount {
		t.Errorf("re-processing created duplicates: had %d, now %d", txnCount, txnCount2)
	}
}
