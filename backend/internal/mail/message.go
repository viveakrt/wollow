package mail

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"

	"github.com/emersion/go-message"

	// Registers decoders for the non-UTF-8 charsets real mail is full of
	// (ISO-8859-1, windows-1252, Shift_JIS, GB2312 …). Without this blank
	// import go-message returns an UnknownCharsetError for those parts and the
	// message fails to open at all.
	_ "github.com/emersion/go-message/charset"
)

// ParsedBody is everything worth showing from one message's MIME tree: the two
// body flavours plus a manifest of every non-body part, each addressable by
// PartID through FetchPart.
type ParsedBody struct {
	Text        string
	HTML        string
	Attachments []Attachment
}

// parseMessage walks a raw RFC 5322 message and separates displayable bodies
// from attachments.
//
// It is deliberately total: a malformed MIME tree yields whatever was readable
// up to the break rather than an error, because a message that cannot be
// parsed perfectly must still open. The only hard failure is a message whose
// top-level header will not parse, and even that falls back to treating the
// whole blob as plain text.
func parseMessage(raw []byte) *ParsedBody {
	out := &ParsedBody{}

	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		// Not even the outer header parsed. Show the raw bytes minus the
		// header block rather than nothing at all.
		out.Text = rawFallbackText(raw)
		return out
	}

	var text, html strings.Builder
	entity.Walk(func(path []int, part *message.Entity, walkErr error) error {
		if walkErr != nil || part == nil {
			// A part we can't read shouldn't sink the ones we can.
			return nil
		}

		contentType, ctParams, _ := part.Header.ContentType()
		contentType = strings.ToLower(contentType)

		// Walk descends into multipart containers itself using this entity's
		// body reader; consuming it here would strip out every child part.
		if strings.HasPrefix(contentType, "multipart/") {
			return nil
		}

		disposition, dispParams, _ := part.Header.ContentDisposition()
		disposition = strings.ToLower(strings.TrimSpace(disposition))

		fileName := headerFileName(ctParams, dispParams)
		contentID := strings.Trim(strings.TrimSpace(part.Header.Get("Content-Id")), "<>")

		isBodyCandidate := (contentType == "text/plain" || contentType == "text/html") &&
			disposition != "attachment" && fileName == ""

		if isBodyCandidate {
			body, readErr := io.ReadAll(part.Body)
			if readErr != nil && len(body) == 0 {
				return nil
			}
			if contentType == "text/html" {
				appendPart(&html, string(body))
			} else {
				appendPart(&text, string(body))
			}
			return nil
		}

		size, _ := io.Copy(io.Discard, part.Body)
		if fileName == "" {
			fileName = defaultFileName(contentType, partKey(path))
		}
		out.Attachments = append(out.Attachments, Attachment{
			PartID:      partKey(path),
			FileName:    fileName,
			ContentType: contentType,
			ContentID:   contentID,
			Size:        size,
			// Inline parts are the ones an HTML body references with cid:; they
			// are listed separately so the UI can resolve those references
			// without also offering them as downloads.
			Inline: contentID != "" || disposition == "inline",
		})
		return nil
	})

	out.Text = text.String()
	out.HTML = html.String()

	if out.Text == "" && out.HTML == "" && len(out.Attachments) == 0 {
		out.Text = rawFallbackText(raw)
	}
	return out
}

// appendPart concatenates sibling body parts (some senders split a body across
// several parts of the same type) with a blank line between them.
func appendPart(b *strings.Builder, s string) {
	if s == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(s)
}

// partKey encodes a go-message walk path as an IMAP-style dotted part number
// ("1", "2.1"). The root entity of a non-multipart message has an empty path
// and is addressed as "1", matching how IMAP numbers a single-part body.
func partKey(path []int) string {
	if len(path) == 0 {
		return "1"
	}
	segments := make([]string, len(path))
	for i, p := range path {
		segments[i] = strconv.Itoa(p + 1)
	}
	return strings.Join(segments, ".")
}

// headerFileName prefers the Content-Disposition filename, falling back to the
// Content-Type name parameter that older senders use instead.
func headerFileName(ctParams, dispParams map[string]string) string {
	for _, candidate := range []string{dispParams["filename"], ctParams["name"]} {
		if name := sanitizeFileName(candidate); name != "" {
			return name
		}
	}
	return ""
}

// sanitizeFileName decodes any RFC 2047 encoding and strips path separators so
// a hostile filename can't escape a download directory.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if decoded, err := new(mime.WordDecoder).DecodeHeader(name); err == nil {
		name = decoded
	}
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i != -1 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	return strings.TrimSpace(strings.TrimPrefix(name, "."))
}

// defaultFileName invents a name for an unnamed part so the UI always has
// something to render and a download always has something to save as.
func defaultFileName(contentType, part string) string {
	ext := "bin"
	if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
		ext = strings.TrimPrefix(exts[0], ".")
	} else if _, subtype, ok := strings.Cut(contentType, "/"); ok && subtype != "" {
		ext = subtype
	}
	return fmt.Sprintf("part-%s.%s", strings.ReplaceAll(part, ".", "-"), ext)
}

// rawFallbackText drops the header block from a message we couldn't parse and
// returns the remainder, so a broken message still shows *something*.
func rawFallbackText(raw []byte) string {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i != -1 {
		return string(raw[i+4:])
	}
	if i := bytes.Index(raw, []byte("\n\n")); i != -1 {
		return string(raw[i+2:])
	}
	return string(raw)
}

// findPart walks a raw message looking for one part by its dotted part number,
// returning its decoded bytes together with the metadata a download response
// needs.
func findPart(raw []byte, partID string) (*PartContent, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, fmt.Errorf("mail: parse message for part %s: %w", partID, err)
	}

	var found *PartContent
	entity.Walk(func(path []int, part *message.Entity, walkErr error) error {
		if walkErr != nil || part == nil || found != nil {
			return nil
		}
		contentType, ctParams, _ := part.Header.ContentType()
		if strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
			return nil
		}
		_, dispParams, _ := part.Header.ContentDisposition()

		// A cid: reference is matched as readily as a part number, so the HTML
		// body's <img src="cid:..."> can be rewritten to a URL without the
		// client first having to look the part up in the manifest.
		contentID := strings.Trim(strings.TrimSpace(part.Header.Get("Content-Id")), "<>")
		if partKey(path) != partID && (contentID == "" || contentID != partID) {
			return nil
		}

		body, readErr := io.ReadAll(part.Body)
		if readErr != nil && len(body) == 0 {
			return nil
		}
		fileName := headerFileName(ctParams, dispParams)
		if fileName == "" {
			fileName = defaultFileName(strings.ToLower(contentType), partKey(path))
		}
		found = &PartContent{
			FileName:    fileName,
			ContentType: contentType,
			Content:     body,
		}
		return nil
	})

	if found == nil {
		return nil, fmt.Errorf("mail: part %q not found", partID)
	}
	return found, nil
}
