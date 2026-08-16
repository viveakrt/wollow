// Package emailparser turns raw bank/card alert emails (fetched over IMAP,
// or loaded from a .eml file for testing) into transactions and bill
// reminders. See hdfc.go, axis.go, and bill.go for per-issuer parsers.
package emailparse

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strconv"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // registers non-UTF8 charset decoders
)

// Email is a normalized, already-decoded view of a message: decoded
// subject, sender, and best-effort plain text body (HTML is stripped to
// text if no text/plain part exists).
type Email struct {
	MessageID      string
	From           string
	Subject        string
	Date           string
	TextBody       string
	HasPDF         bool
	PDFAttachments []PDFAttachment
}

type PDFAttachment struct {
	FileName string
	Content  []byte
}

// ParseEML reads a raw RFC 5322 message (as fetched over IMAP, or a .eml
// file's contents) into a normalized Email.
func ParseEML(raw []byte) (*Email, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if message.IsUnknownCharset(err) {
		// still usable; go-message falls back to a best-effort decode
	} else if err != nil {
		return nil, fmt.Errorf("parse message: %w", err)
	}

	e := &Email{}
	header := entity.Header
	e.MessageID = strings.TrimSpace(header.Get("Message-Id"))
	e.Date = strings.TrimSpace(header.Get("Date"))

	if from, err := header.Text("From"); err == nil {
		e.From = extractEmailAddress(from)
	} else {
		e.From = extractEmailAddress(header.Get("From"))
	}

	if subj, err := header.Text("Subject"); err == nil {
		e.Subject = subj
	} else {
		e.Subject = decodeMIMEWord(header.Get("Subject"))
	}

	var htmlBody, textBody string
	entity.Walk(func(path []int, walkEntity *message.Entity, err error) error {
		if err != nil {
			return nil
		}
		ctype, params, _ := walkEntity.Header.ContentType()
		disp, dispParams, _ := walkEntity.Header.ContentDisposition()

		if strings.HasPrefix(ctype, "multipart/") {
			// Walk recurses into multipart parts itself via MultipartReader,
			// which needs this entity's Body reader untouched — do not
			// consume it here.
			return nil
		}

		// The filename lives in Content-Disposition for most senders and in
		// Content-Type's name= for older ones; some (BOBCARD) supply neither.
		fileName := firstNonEmpty(dispParams["filename"], params["name"])
		isPDF := strings.Contains(strings.ToLower(ctype), "pdf") ||
			strings.HasSuffix(strings.ToLower(fileName), ".pdf")
		if disp == "attachment" || isPDF {
			if isPDF {
				e.HasPDF = true
				if body, readErr := io.ReadAll(walkEntity.Body); readErr == nil {
					if fileName == "" {
						fileName = "statement.pdf"
					}
					e.PDFAttachments = append(e.PDFAttachments, PDFAttachment{
						FileName: fileName,
						Content:  body,
					})
				}
			}
			return nil
		}

		body, readErr := io.ReadAll(walkEntity.Body)
		if readErr != nil {
			return nil
		}
		switch ctype {
		case "text/plain":
			textBody += string(body)
		case "text/html":
			htmlBody += string(body)
		}
		return nil
	})

	if textBody != "" {
		e.TextBody = textBody
	} else if htmlBody != "" {
		e.TextBody = stripHTML(htmlBody)
	}

	return e, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func extractEmailAddress(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "<"); i != -1 {
		if j := strings.Index(s[i:], ">"); j != -1 {
			return strings.ToLower(strings.TrimSpace(s[i+1 : i+j]))
		}
	}
	return strings.ToLower(s)
}

func decodeMIMEWord(s string) string {
	dec := new(mime.WordDecoder)
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// stripHTML is a minimal tag stripper sufficient for bank alert emails
// (simple tables/divs, no scripts/styles with embedded text worth keeping).
func stripHTML(html string) string {
	var b strings.Builder
	inTag := false
	inScript := false
	tagBuf := strings.Builder{}
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
			tagBuf.Reset()
		case r == '>' && inTag:
			inTag = false
			tag := strings.ToLower(strings.TrimSpace(tagBuf.String()))
			if strings.HasPrefix(tag, "script") || strings.HasPrefix(tag, "style") {
				inScript = strings.HasPrefix(tag, "script") || strings.HasPrefix(tag, "style")
			}
			if strings.HasPrefix(tag, "/script") || strings.HasPrefix(tag, "/style") {
				inScript = false
			}
			if tag == "br" || tag == "br/" || tag == "/tr" || tag == "/p" || tag == "/div" || tag == "/td" {
				b.WriteByte('\n')
			}
		case inTag:
			tagBuf.WriteRune(r)
		case inScript:
			// skip
		default:
			b.WriteRune(r)
		}
	}
	text := html2entities(b.String())
	lines := strings.Split(text, "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

var htmlEntities = map[string]string{
	"&nbsp;": " ", "&amp;": "&", "&lt;": "<", "&gt;": ">",
	"&quot;": "\"", "&#39;": "'", "&rsquo;": "'", "&ndash;": "-",
	"&#8377;": "₹", "&#8377": "₹", "&rupee;": "₹",
}

var numericEntityRe = regexp.MustCompile(`&#(\d+);?`)

func html2entities(s string) string {
	for k, v := range htmlEntities {
		s = strings.ReplaceAll(s, k, v)
	}
	s = numericEntityRe.ReplaceAllStringFunc(s, func(m string) string {
		digits := numericEntityRe.FindStringSubmatch(m)[1]
		n, err := strconv.Atoi(digits)
		if err != nil {
			return m
		}
		return string(rune(n))
	})
	return s
}

// Debug prints the MIME structure (content-type, disposition, byte length
// per part) without printing part content, for troubleshooting.
func Debug(raw []byte) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		fmt.Println("read err:", err)
		return
	}
	entity.Walk(func(path []int, walkEntity *message.Entity, err error) error {
		ctype, params, cerr := walkEntity.Header.ContentType()
		disp, _, _ := walkEntity.Header.ContentDisposition()
		bodyLen := -1
		if !strings.HasPrefix(ctype, "multipart/") {
			body, _ := io.ReadAll(walkEntity.Body)
			bodyLen = len(body)
		}
		fmt.Printf("path=%v ctype=%q params=%v disp=%q walkErr=%v ctypeErr=%v bodyLen=%d\n",
			path, ctype, params, disp, err, cerr, bodyLen)
		return nil
	})
}
