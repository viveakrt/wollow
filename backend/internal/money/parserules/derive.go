package parserules

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxPrefixRunes = 40
	maxSuffixRunes = 24
)

var (
	amountInSpanRe = regexp.MustCompile(`\d[\d,]*(?:\.\d{1,2})?`)
	numberInSpanRe = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
	last4InSpanRe  = regexp.MustCompile(`(\d{4})\D*$`)
	wordInSpanRe   = regexp.MustCompile(`[A-Za-z]+`)
	dateInSpanRe   = regexp.MustCompile(capturePattern(FieldDate))
)

// Derive turns the spans a user marked in a sample into anchored fields.
//
// For each span it first tightens the selection to the value itself (a
// selected "Rs.1,234.00" is the figure, the "Rs." becomes anchor text), then
// looks for the shortest run of words before it — and, when that is not
// enough, after it — that identifies exactly that position in the sample.
// Short anchors survive template drift; unique ones avoid reading a different
// value of the same shape. The sample must be NormalizeText output.
func Derive(sampleText string, spans []Span) ([]Field, error) {
	if len(spans) == 0 {
		return nil, fmt.Errorf("mark at least one value in the email")
	}
	if len(spans) > maxFields {
		return nil, fmt.Errorf("a rule can extract at most %d values", maxFields)
	}

	runes := []rune(sampleText)
	seen := map[string]bool{}
	fields := make([]Field, 0, len(spans))
	for _, s := range spans {
		if seen[s.Name] {
			return nil, fmt.Errorf("%q is marked twice", s.Name)
		}
		seen[s.Name] = true

		kind, ok := kindForName(s.Name)
		if !ok {
			return nil, fmt.Errorf("unknown field %q", s.Name)
		}
		if s.Start < 0 || s.End > len(runes) || s.Start >= s.End {
			return nil, fmt.Errorf("%s: the selection is outside the text", s.Name)
		}
		if s.Sample != "" && string(runes[s.Start:s.End]) != s.Sample {
			return nil, fmt.Errorf("%s: the selection does not match the text; select it again", s.Name)
		}

		start, end, err := shrink(runes, s.Start, s.End, kind)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		f := Field{Name: s.Name, Kind: kind, Start: start, End: end, Sample: string(runes[start:end])}
		f, err = anchorField(sampleText, runes, f)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}

	for i := range fields {
		for j := range fields {
			if i < j && fields[i].Start < fields[j].End && fields[j].Start < fields[i].End {
				return nil, fmt.Errorf("%q and %q overlap", fields[i].Name, fields[j].Name)
			}
		}
	}
	return fields, nil
}

// shrink narrows a selection to the value inside it, so a generous drag
// ("Rs. 1,234.00 is") still anchors on the figure alone.
func shrink(runes []rune, start, end int, kind FieldKind) (int, int, error) {
	marked := string(runes[start:end])
	var loc []int
	switch kind {
	case FieldAmount:
		loc = amountInSpanRe.FindStringIndex(marked)
		if loc == nil {
			return 0, 0, fmt.Errorf("there is no amount in the selection")
		}
	case FieldNumber:
		loc = numberInSpanRe.FindStringIndex(marked)
		if loc == nil {
			return 0, 0, fmt.Errorf("there is no number in the selection")
		}
	case FieldLast4:
		m := last4InSpanRe.FindStringSubmatchIndex(marked)
		if m == nil {
			return 0, 0, fmt.Errorf("select at least the last four digits")
		}
		loc = []int{m[2], m[3]}
	case FieldDate:
		loc = dateInSpanRe.FindStringIndex(marked)
		if loc == nil {
			return 0, 0, fmt.Errorf("there is no date in the selection")
		}
	case FieldWord:
		loc = wordInSpanRe.FindStringIndex(marked)
		if loc == nil {
			return 0, 0, fmt.Errorf("there is no word in the selection")
		}
	default:
		trimmed := strings.TrimSpace(marked)
		if trimmed == "" {
			return 0, 0, fmt.Errorf("the selection is empty")
		}
		lead := strings.Index(marked, trimmed)
		loc = []int{lead, lead + len(trimmed)}
	}
	return start + utf8.RuneCountInString(marked[:loc[0]]),
		start + utf8.RuneCountInString(marked[:loc[1]]), nil
}

// anchorField finds prefix/suffix text that reproduces exactly the marked
// value when applied to the sample.
func anchorField(text string, runes []rune, f Field) (Field, error) {
	lineStart := f.Start
	for lineStart > 0 && runes[lineStart-1] != '\n' {
		lineStart--
	}
	lineEnd := f.End
	for lineEnd < len(runes) && runes[lineEnd] != '\n' {
		lineEnd++
	}

	prefixes := prefixCandidates(runes, lineStart, f.Start)
	after := runes[f.End:lineEnd]
	atLineEnd := strings.TrimSpace(string(after)) == ""
	suffixes := suffixCandidates(after)

	type tail struct {
		suffix    string
		atLineEnd bool
	}
	var tails []tail
	if f.Kind == FieldText {
		// Free text has no shape of its own; something must close it.
		if atLineEnd {
			tails = append(tails, tail{"", true})
		}
		for _, s := range suffixes {
			tails = append(tails, tail{s, false})
		}
	} else {
		tails = append(tails, tail{"", false})
		for _, s := range suffixes {
			tails = append(tails, tail{s, false})
		}
		if atLineEnd {
			tails = append(tails, tail{"", true})
		}
	}

	for _, t := range tails {
		var fallback *Field
		for _, prefix := range prefixes {
			candidate := f
			candidate.Prefix, candidate.Suffix, candidate.AtLineEnd = prefix, t.suffix, t.atLineEnd
			re, err := compileField(candidate)
			if err != nil {
				continue
			}
			matches := re.FindAllStringSubmatchIndex(text, -1)
			ours := -1
			for i, m := range matches {
				if m[2] < 0 {
					continue
				}
				if runeOffset(text, m[2]) == f.Start && runeOffset(text, m[3]) == f.End {
					ours = i
					break
				}
			}
			if ours == -1 {
				continue
			}
			if len(matches) == 1 {
				return candidate, nil
			}
			if fallback == nil {
				candidate.Occurrence = ours + 1
				c := candidate
				fallback = &c
			}
		}
		if fallback != nil {
			return *fallback, nil
		}
	}
	return f, fmt.Errorf("%s: could not find text around %q that identifies it; try selecting the value more precisely",
		f.Name, f.Sample)
}

// prefixCandidates lists the text before a value, shortest first: the last
// word, the last two words, and so on, up to a cap. When the value starts its
// line the empty prefix (meaning "at line start") comes first, and the
// candidates continue into the previous line so a "Label:\nvalue" layout can
// anchor on the label.
func prefixCandidates(runes []rune, lineStart, start int) []string {
	var out []string
	if strings.TrimSpace(string(runes[lineStart:start])) == "" {
		out = append(out, "")
	}

	regionStart := lineStart
	if lineStart > 0 {
		regionStart = lineStart - 1
		for regionStart > 0 && runes[regionStart-1] != '\n' {
			regionStart--
		}
	}
	region := runes[regionStart:start]
	for i := len(region) - 1; i >= 0; i-- {
		if unicode.IsSpace(region[i]) || (i > 0 && !unicode.IsSpace(region[i-1])) {
			continue
		}
		cand := strings.TrimRightFunc(string(region[i:]), unicode.IsSpace)
		if cand == "" {
			continue
		}
		if utf8.RuneCountInString(cand) > maxPrefixRunes && len(out) > 0 {
			break
		}
		out = append(out, cand)
	}
	return out
}

// suffixCandidates lists the text after a value: its next word, then its next
// two words.
func suffixCandidates(after []rune) []string {
	var out []string
	words := strings.Fields(string(after))
	for n := 1; n <= 2 && n <= len(words); n++ {
		cand := strings.Join(words[:n], " ")
		if utf8.RuneCountInString(cand) > maxSuffixRunes && n > 1 {
			break
		}
		out = append(out, cand)
	}
	return out
}

func runeOffset(text string, byteOffset int) int {
	return utf8.RuneCountInString(text[:byteOffset])
}
