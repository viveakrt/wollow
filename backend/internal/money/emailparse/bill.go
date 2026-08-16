package emailparse

import (
	"regexp"
	"strings"

	"wollow/backend/internal/money/models"
)

// Credit card statement/bill emails. Some issuers (ICICI Amazon Pay) put
// due-date/min-due/total-due directly in the HTML body; others (BOBCARD,
// HDFC Diners) only send a PDF attachment (sometimes password-protected)
// with generic marketing copy in the body — for those we can only extract
// the card last-4 and statement period from the subject line, and record a
// bill reminder without due-date/amount (user fills in manually or opens
// the PDF themselves).

var (
	billCardLast4Re = regexp.MustCompile(`(?:ending in|card no\.?|Card XX|XX)\s*(\d{4})`)

	// "Payment due\nby 05 August, 2026" (ICICI: label and date on separate lines)
	billPaymentDueRe = regexp.MustCompile(`Payment due\s*\n?\s*by\s+([A-Za-z]+\s+\d{1,2},?\s+\d{4})`)

	billMinDueRe   = regexp.MustCompile(`Minimum Amount Due:?\s*[₹�]?\s*([\d,]+\.\d{2})`)
	billTotalDueRe = regexp.MustCompile(`Total Amount Due:?\s*\n?\s*[₹�]?\s*([\d,]+\.\d{2})`)

	// Subject patterns:
	//   "... Credit Card Statement for the period <D1> to <D2>"
	//   "... credit card ending in <L4> – <Month-Year>"
	//   "... Credit Card Statement - <Month-Year>"
	billSubjectPeriodRe = regexp.MustCompile(`for the period\s+(.+?to.+?\d{4})`)
	billSubjectMonthRe  = regexp.MustCompile(`[-–]\s*([A-Za-z]+[- ]\d{4})\s*$`)

	// The card product name sits between an optional "for your" lead-in and the
	// word "statement": "Your HDFC Bank - Diners Privilege Credit Card
	// Statement - June-2026" -> "Diners Privilege Credit Card".
	billCardNameRe = regexp.MustCompile(`(?i)^(?:e-statement\s+)?(?:for\s+)?(?:your\s+)?(.{3,70}?)\s+(?:e-)?statement\b`)
)

// billCardNameTrim are lead-ins that survive the capture and read as noise in
// an account name.
var billCardNameTrim = []string{"your ", "the ", "-", "–", ":"}

// parseCardName pulls the card product out of a statement subject line.
func parseCardName(subject string) string {
	m := billCardNameRe.FindStringSubmatch(subject)
	if m == nil {
		return ""
	}
	name := strings.TrimSpace(m[1])
	for changed := true; changed; {
		changed = false
		for _, prefix := range billCardNameTrim {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, prefix) {
				name = strings.TrimSpace(name[len(prefix):])
				changed = true
			}
		}
	}
	name = strings.TrimRight(name, " -–:")
	// A name that is just the issuer adds nothing over the issuer itself.
	if len(name) < 4 {
		return ""
	}
	return name
}

// IsBillEmail does a cheap subject-line check for statement/bill emails, to
// distinguish them from transaction alerts before running the heavier
// per-issuer transaction parsers.
//
// "Statement" alone is too eager: banks send "statement of account" summaries
// and brokers send "holding statement", neither of which is a bill. The
// qualifying words below are what make it a card statement carrying an amount
// due.
func IsBillEmail(subject string) bool {
	lower := strings.ToLower(subject)
	if !strings.Contains(lower, "statement") {
		return false
	}
	for _, negative := range []string{"holding statement", "portfolio statement", "statement of holdings"} {
		if strings.Contains(lower, negative) {
			return false
		}
	}
	return true
}

// ParseBillEmail extracts what it can from a statement/bill email's subject
// and body. Fields it can't find are left zero/empty — callers should still
// record the bill reminder with whatever was found (e.g. just card+period)
// so the user knows a statement arrived even if the PDF wasn't parsed.
func ParseBillEmail(issuer, subject, body string) models.ParsedBillEmail {
	b := models.ParsedBillEmail{Issuer: issuer, CardName: parseCardName(subject)}

	if m := billCardLast4Re.FindStringSubmatch(subject); m != nil {
		b.CardLast4 = m[1]
	} else if m := billCardLast4Re.FindStringSubmatch(body); m != nil {
		b.CardLast4 = m[1]
	}

	if m := billSubjectPeriodRe.FindStringSubmatch(subject); m != nil {
		b.StatementPeriod = strings.TrimSpace(m[1])
	} else if m := billSubjectMonthRe.FindStringSubmatch(subject); m != nil {
		b.StatementPeriod = strings.TrimSpace(m[1])
	}

	if m := billPaymentDueRe.FindStringSubmatch(body); m != nil {
		b.DueDate = strings.TrimSpace(m[1])
	}
	if m := billMinDueRe.FindStringSubmatch(body); m != nil {
		v := parseAmountCommas(m[1])
		b.MinimumDue = &v
	}
	if m := billTotalDueRe.FindStringSubmatch(body); m != nil {
		v := parseAmountCommas(m[1])
		b.TotalDue = &v
	}

	return b
}
