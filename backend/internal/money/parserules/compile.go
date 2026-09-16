package parserules

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	maxFields     = 20
	maxPatternLen = 2048
)

type compiledField struct {
	Field
	re *regexp.Regexp
}

// compileField turns a field's literal anchors into a regular expression.
//
// The anchors are the user's own text, so they are always quoted: the only
// regex syntax in the result is what this file writes around them. Digits in
// an anchor are generalized — a date or reference sitting next to the value
// changes with every message — and so are whitespace runs, account-number
// masks and anything that looks like an address.
func compileField(f Field) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`(?i)`)
	if f.Prefix == "" {
		b.WriteString(`(?m:^)`)
	} else {
		b.WriteString(anchorPattern(f.Prefix))
		b.WriteString(`\s*`)
	}
	b.WriteString(capturePattern(f.Kind))
	switch {
	case f.Suffix != "":
		b.WriteString(`\s*`)
		b.WriteString(anchorPattern(f.Suffix))
	case f.AtLineEnd:
		b.WriteString(`[ \t]*(?:\n|$)`)
	case f.Kind == FieldText:
		return nil, fmt.Errorf("field %q: a text value needs some text after it, or must end its line", f.Name)
	}
	pattern := b.String()
	if len(pattern) > maxPatternLen {
		return nil, fmt.Errorf("field %q: the text around the value is too long to anchor on", f.Name)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("field %q: %w", f.Name, err)
	}
	return re, nil
}

// capturePattern is the one capturing group in a field's pattern: what the
// value itself may look like.
func capturePattern(k FieldKind) string {
	switch k {
	case FieldAmount:
		return `([\d,]+(?:\.\d{1,2})?)`
	case FieldNumber:
		return `([\d,]+(?:\.\d+)?)`
	case FieldLast4:
		// A masked tail ("XX4125", "****4125") or a full number; either way
		// the last four digits are the identity.
		return `[Xx*•]*\d*(\d{4})\b`
	case FieldDate:
		return `(\d{4}-\d{2}-\d{2}|\d{1,2}[-/. ](?:\d{1,2}|[A-Za-z]{3,9})[-/. ,]+\d{2,4}|[A-Za-z]{3,9}\s+\d{1,2},?\s+\d{4})`
	case FieldWord:
		return `([A-Za-z]+)`
	default:
		return `(.+?)`
	}
}

// anchorPattern quotes an anchor's literal text, generalizing the parts that
// vary from one message to the next.
func anchorPattern(literal string) string {
	var b strings.Builder
	runes := []rune(literal)
	i := 0
	for i < len(runes) {
		if unicode.IsSpace(runes[i]) {
			for i < len(runes) && unicode.IsSpace(runes[i]) {
				i++
			}
			b.WriteString(`\s+`)
			continue
		}
		j := i
		for j < len(runes) && !unicode.IsSpace(runes[j]) {
			j++
		}
		word := runes[i:j]
		if strings.ContainsRune(string(word), '@') {
			// A VPA or email address next to the value names the payee, which
			// differs every time.
			b.WriteString(`\S+`)
		} else {
			b.WriteString(wordPattern(word))
		}
		i = j
	}
	return b.String()
}

func wordPattern(word []rune) string {
	var b strings.Builder
	for i := 0; i < len(word); {
		switch {
		case unicode.IsDigit(word[i]):
			j := i + 1
			for j < len(word) && (unicode.IsDigit(word[j]) || strings.ContainsRune(",.:/-", word[j])) {
				j++
			}
			// Punctuation that closes the number ("26.") belongs to the
			// sentence, not the figure.
			for j > i+1 && !unicode.IsDigit(word[j-1]) {
				j--
			}
			b.WriteString(`\d[\d,.:/-]*`)
			i = j
		case isMaskRune(word[i]):
			j := i
			for j < len(word) && isMaskRune(word[j]) {
				j++
			}
			if j-i >= 2 && j < len(word) && unicode.IsDigit(word[j]) {
				b.WriteString(`[Xx*•]+`)
			} else {
				b.WriteString(regexp.QuoteMeta(string(word[i:j])))
			}
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(word[i])))
			i++
		}
	}
	return b.String()
}

func isMaskRune(r rune) bool {
	return r == 'X' || r == 'x' || r == '*' || r == '•'
}
