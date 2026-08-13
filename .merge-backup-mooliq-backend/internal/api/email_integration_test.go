package api

import (
	"os"
	"path/filepath"
	"testing"

	"mooliq/backend/internal/db"
	"mooliq/backend/internal/emailparser"
)

// TestProcessRealSampleEmails exercises processEmail against the real
// sample .eml files in statements/, verifying the full parse->persist path
// (not just the parser package) works, including dedupe and account
// matching, without needing a live IMAP connection.
func TestProcessRealSampleEmails(t *testing.T) {
	sampleDir := filepath.Join("..", "..", "..", "statements")
	entries, err := os.ReadDir(sampleDir)
	if err != nil {
		t.Skipf("sample dir not found: %v", err)
	}

	tmpDB := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(tmpDB)
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
		parsed, err := emailparser.ParseEML(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		issuer := emailparser.IssuerForSender(parsed.From)
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
		parsed, _ := emailparser.ParseEML(raw)
		issuer := emailparser.IssuerForSender(parsed.From)
		processEmail(conn, issuer, parsed)
	}
	var txnCount2 int
	conn.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&txnCount2)
	if txnCount2 != txnCount {
		t.Errorf("re-processing created duplicates: had %d, now %d", txnCount, txnCount2)
	}
}
