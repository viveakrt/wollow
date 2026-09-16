package ingest

import (
	"context"
	"testing"

	"wollow/backend/internal/money/parserules"
)

// TestLinksRoundTrip is the acceptance check at the data layer: after ingest,
// you can start from a transaction, reach the message that produced it, and
// get back to the same transaction. If either direction is missing, the
// cross-product UI links have nothing to render.
func TestLinksRoundTrip(t *testing.T) {
	conn := openDB(t)
	sample := hdfcDebit(1301, "1234.00", "SWIGGY", "123456789012")
	messages := []message{
		hdfcDebit(1302, "10.00", "ONE", "111111111111"),
		hdfcDebit(1303, "20.00", "TWO", "222222222222"),
	}
	mailbox := seedIndex(t, conn, messages)
	addAccount(t, conn, "HDFC Savings", "HDFC", "bank", "XXXXXXXX4125")
	defineRule(t, conn, parserules.KindTransaction, "HDFC", "hdfcbank.net", sample, hdfcMarks, nil)

	if _, err := Run(context.Background(), conn, newFetcher(messages), mailbox, "INBOX"); err != nil {
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

	if len(links) != len(messages) {
		t.Fatalf("%d linked transactions, want %d", len(links), len(messages))
	}
	for _, l := range links {
		if l.mailAccountID != mailbox {
			t.Errorf("transaction %d links to mailbox %d, want %d", l.txnID, l.mailAccountID, mailbox)
		}
		if l.uid == 0 {
			t.Errorf("transaction %d has no UID; Mail could not address the message", l.txnID)
		}
		if l.subject == "" {
			t.Errorf("transaction %d has an empty source subject; the link would render blank", l.txnID)
		}

		// Message -> transaction: the same index row must point back here.
		var backRef int64
		if err := conn.QueryRow(`
			SELECT l.transaction_id
			FROM messages m
			JOIN message_links l ON l.message_id = m.id
			WHERE m.account_id = ? AND m.folder = 'INBOX' AND m.uid = ?`,
			l.mailAccountID, l.uid).Scan(&backRef); err != nil {
			t.Fatalf("no link back from message uid %d: %v", l.uid, err)
		}
		if backRef != l.txnID {
			t.Errorf("round trip broke: transaction %d -> uid %d -> transaction %d", l.txnID, l.uid, backRef)
		}
	}
}
