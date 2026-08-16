package mail

import (
	"bufio"
	"bytes"
	"net/textproto"
	"regexp"
	"strings"
)

// UnsubscribeInfo is what a message's List-Unsubscribe header(s) offer,
// per RFC 2369 / RFC 8058.
type UnsubscribeInfo struct {
	HTTPURL string // first http(s) URI found, if any
	Mailto  string // first mailto: URI found, if any
	// OneClick is true when List-Unsubscribe-Post advertises RFC 8058
	// one-click support, meaning HTTPURL should be POSTed to rather than GET.
	OneClick bool
}

var listUnsubURIRE = regexp.MustCompile(`<([^>]+)>`)

// ParseUnsubscribeHeaders reads List-Unsubscribe (and List-Unsubscribe-Post)
// out of a raw RFC 5322 header block. Returns nil if the message doesn't
// advertise any unsubscribe method at all.
func ParseUnsubscribeHeaders(raw []byte) *UnsubscribeInfo {
	// A header block fetched via IMAP's BODY[HEADER] already ends in a blank
	// line per RFC 3501, but a defensive extra one costs nothing and keeps
	// ReadMIMEHeader from erroring on a server that omits it.
	buf := bytes.NewBuffer(raw)
	buf.WriteString("\r\n\r\n")

	header, err := textproto.NewReader(bufio.NewReader(buf)).ReadMIMEHeader()
	if err != nil && header == nil {
		return nil
	}

	value := header.Get("List-Unsubscribe")
	if value == "" {
		return nil
	}

	info := &UnsubscribeInfo{}
	for _, m := range listUnsubURIRE.FindAllStringSubmatch(value, -1) {
		uri := strings.TrimSpace(m[1])
		lower := strings.ToLower(uri)
		switch {
		case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
			if info.HTTPURL == "" {
				info.HTTPURL = uri
			}
		case strings.HasPrefix(lower, "mailto:"):
			if info.Mailto == "" {
				info.Mailto = uri
			}
		}
	}
	if info.HTTPURL == "" && info.Mailto == "" {
		return nil
	}

	info.OneClick = strings.Contains(strings.ToLower(header.Get("List-Unsubscribe-Post")), "one-click")
	return info
}
