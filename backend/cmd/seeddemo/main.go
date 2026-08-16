// Command seeddemo populates a Wollow database from the sample emails in
// statements/, so the app can be run and looked at without connecting a real
// mailbox.
//
// It writes exactly what a sync pass followed by a finance ingest pass would
// have written: a mailbox, an indexed message per sample, and the transactions,
// bills and message_links that ingest derives from them. Message bodies are not
// stored (the index never stores them), so opening a message in the UI will
// still try to reach IMAP and fail — everything else, including both directions
// of the Mail ↔ Money links, works.
//
// Development only. Never point this at a database you care about.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"wollow/backend/internal/mail"
	"wollow/backend/internal/money/emailparse"
	"wollow/backend/internal/money/ingest"
	"wollow/backend/internal/platform/crypto"
	"wollow/backend/internal/platform/db"
)

// localFetcher serves message bodies from disk instead of over IMAP, which is
// all ingest needs — it only ever asks for UIDs it picked out of the index.
type localFetcher struct{ byUID map[uint32][]byte }

func (f *localFetcher) FetchRaw(_ context.Context, _ string, uids []uint32) ([]mail.RawMessage, error) {
	out := make([]mail.RawMessage, 0, len(uids))
	for _, uid := range uids {
		if raw, ok := f.byUID[uid]; ok {
			out = append(out, mail.RawMessage{UID: uid, Raw: raw})
		}
	}
	return out, nil
}

func main() {
	dataDir := flag.String("data", "./data", "data directory holding wollow.db")
	samples := flag.String("samples", "../statements", "directory of sample .eml files")
	masterKey := flag.String("key", os.Getenv("WOLLOW_MASTER_KEY"), "hex master key (defaults to $WOLLOW_MASTER_KEY)")
	classifyAll := flag.Bool("classify", true, "mark every sample transactional, so classifier-driven discovery is exercised too")
	flag.Parse()

	if *masterKey == "" {
		log.Fatal("a master key is required: pass -key or set WOLLOW_MASTER_KEY")
	}
	box, err := crypto.New(*masterKey)
	if err != nil {
		log.Fatalf("crypto: %v", err)
	}

	conn, err := db.Open(*dataDir)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	entries, err := os.ReadDir(*samples)
	if err != nil {
		log.Fatalf("read samples from %s: %v", *samples, err)
	}

	encrypted, err := box.Encrypt("demo-app-password")
	if err != nil {
		log.Fatalf("encrypt: %v", err)
	}
	res, err := conn.Exec(`
		INSERT INTO mail_accounts (label, imap_host, imap_port, username, encrypted_password)
		VALUES ('Demo mailbox', 'imap.example.invalid', 993, 'demo@example.invalid', ?)`, encrypted)
	if err != nil {
		log.Fatalf("insert mailbox: %v", err)
	}
	accountID, _ := res.LastInsertId()

	byUID := map[uint32][]byte{}
	var uid uint32 = 1000
	indexed := 0

	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".eml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(*samples, e.Name()))
		if err != nil {
			log.Printf("skip %s: %v", e.Name(), err)
			continue
		}
		parsed, err := emailparse.ParseEML(raw)
		if err != nil {
			log.Printf("skip %s: %v", e.Name(), err)
			continue
		}

		uid++
		byUID[uid] = raw

		domain := ""
		if at := strings.LastIndex(parsed.From, "@"); at != -1 {
			domain = strings.ToLower(parsed.From[at+1:])
		}

		msgRes, err := conn.Exec(`
			INSERT INTO messages (account_id, folder, uid, rfc_message_id, subject, from_name,
			                      from_email, from_domain, date, snippet, seen)
			VALUES (?, 'INBOX', ?, ?, ?, '', ?, ?, ?, ?, 0)`,
			accountID, uid, parsed.MessageID, parsed.Subject, parsed.From, domain,
			parsed.Date, snippet(parsed.TextBody))
		if err != nil {
			log.Fatalf("index %s: %v", e.Name(), err)
		}
		indexed++

		if *classifyAll {
			msgID, _ := msgRes.LastInsertId()
			if _, err := conn.Exec(`
				INSERT INTO classifications (message_id, category, priority, action, is_transactional, confidence, summary, model)
				VALUES (?, 'finance', 'medium', 'review', 1, 0.9, '', 'seeddemo')`, msgID); err != nil {
				log.Fatalf("classify %s: %v", e.Name(), err)
			}
		}
	}

	result, err := ingest.Run(context.Background(), conn, &localFetcher{byUID: byUID}, accountID, "INBOX")
	if err != nil {
		log.Fatalf("ingest: %v", err)
	}

	fmt.Printf("seeded %s\n", filepath.Join(*dataDir, "wollow.db"))
	fmt.Printf("  mailbox id:   %d\n", accountID)
	fmt.Printf("  indexed:      %d messages\n", indexed)
	fmt.Printf("  scanned:      %d\n", result.Scanned)
	fmt.Printf("  transactions: %d\n", result.Transactions)
	fmt.Printf("  bills:        %d\n", result.Bills)
	fmt.Printf("  unrecognized: %d\n", result.Unrecognized)
}

func snippet(body string) string {
	s := strings.Join(strings.Fields(body), " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
