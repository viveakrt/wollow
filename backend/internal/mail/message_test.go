package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleDir is the repo's statements/ folder, three levels up from
// internal/mail. The real mail in it is the only honest test of a MIME parser.
func sampleDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "statements")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("sample dir %s not available: %v", dir, err)
	}
	return dir
}

func readSample(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sampleDir(t), name))
	if err != nil {
		t.Fatalf("reading sample %s: %v", name, err)
	}
	return raw
}

// Every sample must yield a displayable body. Before this parser existed, an
// HTML-only message produced no body at all on screen, which is what "emails
// don't open" looked like from the outside.
func TestParseMessageProducesABodyForEverySample(t *testing.T) {
	dir := sampleDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading sample dir: %v", err)
	}

	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".eml") {
			continue
		}
		seen++
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		parsed := parseMessage(raw)
		if parsed.Text == "" && parsed.HTML == "" {
			t.Errorf("%s: no text and no HTML body", e.Name())
		}
	}
	if seen == 0 {
		t.Skip("no .eml samples present")
	}
}

func TestParseMessageHTMLOnlyMail(t *testing.T) {
	parsed := parseMessage(readSample(t, "HDFC credit update.eml"))

	if parsed.HTML == "" {
		t.Fatal("expected an HTML body")
	}
	if !strings.Contains(parsed.HTML, "rcyadav28@okhdfcbank") {
		t.Error("HTML body does not contain the alert text")
	}
	if len(parsed.Attachments) != 0 {
		t.Errorf("expected no attachments, got %d", len(parsed.Attachments))
	}
}

// A statement mail is the case the old parser dropped on the floor: it reached
// the attachment and skipped it, so the PDF was invisible in the UI.
func TestParseMessageSurfacesAttachments(t *testing.T) {
	parsed := parseMessage(readSample(t,
		"Amazon Pay ICICI Bank Credit Card Statement for the period June 13, 2026 to July 12, 2026.eml"))

	if parsed.HTML == "" {
		t.Fatal("expected an HTML body alongside the attachment")
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if att.ContentType != "application/pdf" {
		t.Errorf("content type = %q, want application/pdf", att.ContentType)
	}
	if !strings.HasSuffix(att.FileName, ".pdf") {
		t.Errorf("file name = %q, want a .pdf", att.FileName)
	}
	if att.Size == 0 {
		t.Error("attachment size was not measured")
	}
	if att.Inline {
		t.Error("a disposition=attachment PDF should not be marked inline")
	}
	if att.PartID == "" {
		t.Error("attachment has no part id, so it can never be fetched")
	}
}

// An attachment with no filename anywhere still has to be downloadable, so the
// parser invents one from the content type.
func TestParseMessageNamesUnnamedAttachments(t *testing.T) {
	parsed := parseMessage(readSample(t,
		"E-statement for your BOBCARD BOBCARD-UNI (GOLDX)-RUPAY credit card ending in 3109 – June 2026.eml"))

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	if name := parsed.Attachments[0].FileName; name == "" || strings.Contains(name, "/") {
		t.Errorf("invented file name = %q, want a bare non-empty name", name)
	}
}

// Fetching a part back by the id the manifest advertised must return the same
// part — this is the contract the attachment download URL relies on.
func TestFindPartRoundTripsThePartID(t *testing.T) {
	raw := readSample(t,
		"Your HDFC Bank - Diners Privilege Credit Card Statement - June-2026.eml")
	parsed := parseMessage(raw)
	if len(parsed.Attachments) == 0 {
		t.Fatal("expected at least one attachment")
	}
	want := parsed.Attachments[0]

	got, err := findPart(raw, want.PartID)
	if err != nil {
		t.Fatalf("findPart(%q): %v", want.PartID, err)
	}
	if got.ContentType != want.ContentType {
		t.Errorf("content type = %q, want %q", got.ContentType, want.ContentType)
	}
	if int64(len(got.Content)) != want.Size {
		t.Errorf("fetched %d bytes, manifest said %d", len(got.Content), want.Size)
	}
}

func TestFindPartRejectsUnknownParts(t *testing.T) {
	raw := readSample(t, "HDFC credit update.eml")
	if _, err := findPart(raw, "9.9"); err == nil {
		t.Fatal("expected an error for a part that does not exist")
	}
}

// A message whose MIME structure is nonsense still has to open. Returning an
// error here is what made a malformed message unopenable rather than ugly.
func TestParseMessageDegradesInsteadOfFailing(t *testing.T) {
	raw := []byte("Subject: broken\r\nContent-Type: multipart/mixed; boundary=nope\r\n\r\nthis body never opens a part\r\n")
	parsed := parseMessage(raw)
	if parsed.Text == "" && parsed.HTML == "" {
		t.Fatal("expected a fallback body for an unparseable message")
	}
	if strings.Contains(parsed.Text, "Subject: broken") {
		t.Error("fallback body should start after the header block")
	}
}

func TestSanitizeFileNameStripsPaths(t *testing.T) {
	cases := map[string]string{
		`../../etc/passwd`:    "passwd",
		`C:\Windows\evil.exe`: "evil.exe",
		"  spaced.pdf  ":      "spaced.pdf",
		"":                    "",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
