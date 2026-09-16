// Package parserules holds the data-driven email parsers the user defines from
// the UI: a rule names the sender it applies to and, for each value it
// extracts, the literal text that surrounds that value in a sample message.
// Ingest applies the same rules to every later message from that sender.
//
// The package owns three things that must agree with each other exactly —
// how message text is normalized, how a marked span becomes an anchored
// pattern, and how that pattern is applied — so both the editor (which derives
// a rule from a sample) and ingest (which applies it) go through here.
package parserules

import (
	"strings"
	"unicode"
)

// NormalizeText is the one text shape every consumer works on: LF line ends,
// no exotic spaces, one space between words, no blank lines. Offsets and
// anchors are only meaningful against text that went through here.
func NormalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Map(func(r rune) rune {
			// Zero-width space and byte-order mark: invisible, and they split
			// words that should anchor as one.
			if r == 0x200B || r == 0xFEFF {
				return -1
			}
			if unicode.IsSpace(r) {
				return ' '
			}
			return r
		}, line)
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// SampleText is the text a rule is defined on and applied to: the subject as
// the first line, then the body. Putting the subject in the same text is what
// lets a value that only appears there (Axis prints the amount in the subject)
// be marked like any other.
func SampleText(subject, body string) string {
	return NormalizeText(subject + "\n" + body)
}
