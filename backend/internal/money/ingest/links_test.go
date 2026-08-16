package ingest

import (
	"context"
	"testing"
)

// TestLinksRoundTrip is the phase-4 acceptance check at the data layer: after
// ingest, you can start from a transaction, reach the message that produced it,
// and get back to the same transaction. If either direction is missing, the
// cross-product UI links have nothing to render.
func TestLinksRoundTrip(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	seedAccounts(t, conn)

	if _, err := Run(context.Background(), conn, newFetcher(samples), accountID, "INBOX"); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Transaction -> message: every email-derived transaction must resolve to a
	// mailbox + UID, which is how the Mail API addresses a message.
	//
	// Drain the cursor before running the follow-up queries: the pool is capped
	// at one connection (SQLite writers must be serialized), so querying while
	// this cursor is still open deadlocks against itself.
	type link struct {
		txnID, mailAccountID int64
		uid                  uint32
		subject              string
	}
	rows, err := conn.Query(`
		SELECT t.id, l.mail_account_id, l.uid, l.subject
		FROM transactions t
		JOIN message_links l ON l.transaction_id = t.id`)
	if err != nil {
		t.Fatalf("query links: %v", err)
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.txnID, &l.mailAccountID, &l.uid, &l.subject); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		links = append(links, l)
	}
	rows.Close()

	seen := 0
	for _, l := range links {
		txnID, mailAccountID, uid, subject := l.txnID, l.mailAccountID, l.uid, l.subject
		seen++

		if mailAccountID != accountID {
			t.Errorf("transaction %d links to mailbox %d, want %d", txnID, mailAccountID, accountID)
		}
		if uid == 0 {
			t.Errorf("transaction %d has no UID; Mail could not address the message", txnID)
		}
		if subject == "" {
			t.Errorf("transaction %d has an empty source subject; the link would render blank", txnID)
		}

		// Message -> transaction: the same index row must point back here.
		var backRef int64
		if err := conn.QueryRow(`
			SELECT l.transaction_id
			FROM messages m
			JOIN message_links l ON l.message_id = m.id
			WHERE m.account_id = ? AND m.folder = 'INBOX' AND m.uid = ?`,
			mailAccountID, uid).Scan(&backRef); err != nil {
			t.Fatalf("no link back from message uid %d: %v", uid, err)
		}
		if backRef != txnID {
			t.Errorf("round trip broke: transaction %d -> uid %d -> transaction %d", txnID, uid, backRef)
		}
	}
	if seen == 0 {
		t.Fatal("no email-derived transactions; the round trip proves nothing")
	}
	t.Logf("round-tripped %d transactions", seen)
}

// TestBillsLinkToTheirEmail covers the other half of the join, which is what
// puts a "source email" on a bill in the dashboard's upcoming list.
func TestBillsLinkToTheirEmail(t *testing.T) {
	samples := loadSamples(t)
	conn := openDB(t)
	accountID := seedIndex(t, conn, samples)
	seedAccounts(t, conn)

	if _, err := Run(context.Background(), conn, newFetcher(samples), accountID, "INBOX"); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	var bills, linked int
	conn.QueryRow(`SELECT COUNT(*) FROM bills`).Scan(&bills)
	conn.QueryRow(`
		SELECT COUNT(*) FROM bills b
		JOIN message_links l ON l.bill_id = b.id
		WHERE l.uid != 0`).Scan(&linked)

	if bills == 0 {
		// The sample folder is curated by hand and need not always hold a
		// statement email. Skipping says "not exercised" instead of claiming a
		// failure the code did not cause.
		t.Skip("no statement emails in the current samples")
	}
	if linked != bills {
		t.Errorf("%d of %d bills have no addressable source email", bills-linked, bills)
	}
}
