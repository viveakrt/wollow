package emailparse

import (
	"regexp"
	"strings"
	"time"

	"wollow/backend/internal/money/models"
)

// A generic transaction-alert reader for the issuers no hand-written parser
// covers.
//
// Indian bank, card and wallet alerts converge on a small set of sentences —
// "Rs.500.00 is debited from your account ending 1234", "INR 1,018 spent on
// credit card no. XX5792", "Rs 200 has been credited to your Paytm wallet".
// Rather than one parser per issuer forever, this reads that shared shape and
// the per-issuer parsers stay for the templates it can't (HDFC's label:value
// deposit alerts, Axis's table layout).
//
// It is deliberately conservative: it insists on an amount *and* a direction
// word before claiming a transaction, because a false positive here writes a
// wrong number into someone's ledger, while a false negative just leaves the
// message marked 'unrecognized' and visible in the inbox.

var (
	// Amount with an Indian currency marker. Decimals are optional because
	// plenty of senders write "INR 1018"; the non-breaking space that Axis
	// emits between marker and digits is matched explicitly.
	amountPattern = `(?:rs\.?|inr|₹)[\s\x{00a0}]*([\d,]+(?:\.\d{1,2})?)`

	genericDebitRe = regexp.MustCompile(`(?i)` + amountPattern +
		`[^.\n]{0,60}?\b(?:is |has been |was |been )?(?:debited|spent|withdrawn|deducted|paid|charged)\b`)
	genericCreditRe = regexp.MustCompile(`(?i)` + amountPattern +
		`[^.\n]{0,60}?\b(?:is |has been |was |been )?(?:credited|received|deposited|added|refunded)\b`)

	// "spent on credit card no. XX5792" / "debited from A/c XX1234" — the
	// direction word can also come first.
	genericDebitFirstRe  = regexp.MustCompile(`(?i)\b(?:debited|spent|withdrawn|deducted|charged)\b[^.\n]{0,40}?` + amountPattern)
	genericCreditFirstRe = regexp.MustCompile(`(?i)\b(?:credited|received|deposited|refunded)\b[^.\n]{0,40}?` + amountPattern)

	// Card or account tail. "ending 1234", "A/c XX1234", "card no. XXXX5792",
	// "account no. ****4125".
	genericLast4Re = regexp.MustCompile(`(?i)\b(?:a/?c|acct|account|card)\b[\s.]*(?:no\.?|number|num)?[\s.:]*` +
		`(?:ending(?:\s+(?:in|with))?)?[\s.:]*[xX*•]{0,12}(\d{4})\b`)
	genericEndingRe = regexp.MustCompile(`(?i)\bending(?:\s+(?:in|with))?[\s.:]*[xX*•]{0,12}(\d{4})\b`)

	genericVPARe      = regexp.MustCompile(`(?i)\bVPA\s+(\S+?)(?:\s*\(([^)]*)\))?[\s.,]`)
	genericMerchantRe = regexp.MustCompile(`(?i)\b(?:at|to|towards|in favou?r of|paid to)\s+` +
		`([A-Za-z0-9][A-Za-z0-9 &._'\-]{2,48}?)(?:\s+on\b|\s*[.,\n]|$)`)
	genericRefRe = regexp.MustCompile(`(?i)\b(?:upi(?:\s+transaction)?\s+reference\s+no|reference\s+no|ref\s*no|ref|utr|txn\s*id|transaction\s*id)\b\.?\s*:?\s*([A-Za-z0-9]{6,24})\b`)
	genericBalRe = regexp.MustCompile(`(?i)\b(?:available\s+balance|avl\.?\s*bal|closing\s+balance|balance)\b[^\d₹]{0,20}` + amountPattern)

	// Dates, in the several shapes alerts use: 01-01-26, 01/01/2026,
	// 01-JAN-2026, 1 Jan 2026, Jan 1, 2026.
	genericDateRe = regexp.MustCompile(`(?i)\b(\d{1,2}[-/](?:\d{1,2}|[A-Za-z]{3})[-/]\d{2,4}|` +
		`\d{1,2}\s+[A-Za-z]{3,9},?\s+\d{4}|[A-Za-z]{3,9}\s+\d{1,2},?\s+\d{4})\b`)
)

// ParseGenericAlert extracts a transaction from an alert email whose issuer has
// no dedicated parser. Returns (nil, false) when the message doesn't read as a
// single money movement.
func ParseGenericAlert(subject, body string) (*models.ParsedEmailTransaction, bool) {
	// Subject and body are searched together, subject first: many issuers put
	// the whole transaction in the subject line ("INR 1018 spent on credit card
	// no. XX5792") and bury it in marketing markup in the body.
	scan := subject + "\n" + truncateForScan(body)

	amount, direction, at := findAmountAndDirection(scan)
	if amount == 0 {
		return nil, false
	}

	// The clause the bank wrote about *this* transaction. Everything that
	// describes the payment lives in it, and nothing that describes the email
	// does — which is what keeps footer boilerplate ("click here to modify or
	// unsubscribe from Insta Alerts") out of the merchant field.
	sentence := sentenceAround(scan, at)

	txn := &models.ParsedEmailTransaction{
		Amount: amount,
		Type:   direction,
	}

	if m := genericEndingRe.FindStringSubmatch(scan); m != nil {
		txn.AccountLast4 = m[1]
	} else if m := genericLast4Re.FindStringSubmatch(scan); m != nil {
		txn.AccountLast4 = m[1]
	}

	if m := genericVPARe.FindStringSubmatch(scan); m != nil {
		txn.PaymentMethod = "UPI"
		txn.Merchant = strings.TrimSpace(m[2])
		if txn.Merchant == "" {
			txn.Merchant = strings.TrimSpace(m[1])
		}
		// Mirrors how the same payment reads on a bank statement, so an alert
		// and a statement row for one payment look alike.
		txn.Narration = "UPI-" + txn.Merchant + "-" + strings.TrimSpace(m[1])
	} else {
		// The transaction's own sentence first; only then the whole email.
		if m := genericMerchantRe.FindStringSubmatch(sentence); m != nil {
			txn.Merchant = cleanMerchant(m[1])
		}
		if txn.Merchant == "" {
			if m := genericMerchantRe.FindStringSubmatch(scan); m != nil {
				txn.Merchant = cleanMerchant(m[1])
			}
		}
	}

	if txn.PaymentMethod == "" {
		txn.PaymentMethod = detectPaymentMethod(scan)
	}
	if m := genericRefRe.FindStringSubmatch(scan); m != nil {
		txn.RefNo = m[1]
	}
	if m := genericDateRe.FindStringSubmatch(scan); m != nil {
		txn.TxnDate = parseFlexDate(m[1])
	}
	if m := genericBalRe.FindStringSubmatch(scan); m != nil {
		txn.ClosingBalance = parseAmountCommas(m[1])
	}
	if txn.Narration == "" {
		txn.Narration = buildNarration(sentence, subject, txn.Merchant)
	}

	return txn, true
}

// findAmountAndDirection returns the transaction amount and whether it moved
// out ("expense") or in ("income").
//
// Whichever pattern matches *earliest* in the text wins. Alerts routinely
// mention both directions — a debit alert ends with "available balance
// credited/…" boilerplate — and the transaction being reported is always the
// one stated first.
// It also returns where in the text the match started, so the caller can read
// the sentence the amount sits in rather than the whole email.
func findAmountAndDirection(scan string) (amount float64, direction string, at int) {
	type hit struct {
		index     int
		amount    float64
		direction string
	}
	var best *hit

	consider := func(re *regexp.Regexp, direction string) {
		loc := re.FindStringSubmatchIndex(scan)
		if loc == nil {
			return
		}
		amount := parseAmountCommas(scan[loc[2]:loc[3]])
		if amount == 0 {
			return
		}
		if best == nil || loc[0] < best.index {
			best = &hit{index: loc[0], amount: amount, direction: direction}
		}
	}

	consider(genericDebitRe, "expense")
	consider(genericCreditRe, "income")
	consider(genericDebitFirstRe, "expense")
	consider(genericCreditFirstRe, "income")

	if best == nil {
		return 0, "", -1
	}
	return best.amount, best.direction, best.index
}

// sentenceAround returns the sentence containing idx.
//
// Boundaries are a newline, or sentence punctuation followed by a space — not
// a bare "." — because every amount in these mails contains one ("Rs.5000.00")
// and splitting on it would cut the sentence in half.
func sentenceAround(s string, idx int) string {
	if idx < 0 || idx >= len(s) {
		return ""
	}
	isBoundary := func(i int) bool {
		if s[i] != '.' && s[i] != '!' && s[i] != '?' {
			return false
		}
		return i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == '\n' || s[i+1] == '\r'
	}

	start := 0
	for i := idx - 1; i >= 0; i-- {
		if s[i] == '\n' || isBoundary(i) {
			start = i + 1
			break
		}
	}
	end := len(s)
	for i := idx; i < len(s); i++ {
		if s[i] == '\n' {
			end = i
			break
		}
		if isBoundary(i) {
			end = i + 1
			break
		}
	}
	return strings.TrimSpace(s[start:end])
}

var paymentMethodWords = []struct {
	word   string
	method string
}{
	{"upi", "UPI"},
	{"neft", "NEFT"},
	{"imps", "IMPS"},
	{"rtgs", "RTGS"},
	{"credit card", "Credit Card"},
	{"debit card", "Debit Card"},
	{"atm", "ATM"},
	{"net banking", "NetBanking"},
	{"netbanking", "NetBanking"},
	{"wallet", "Wallet"},
	{"auto debit", "Auto Debit"},
	{"standing instruction", "SI"},
	{"nach", "NACH"},
	{"ecs", "ECS"},
	{"cheque", "Cheque"},
}

func detectPaymentMethod(scan string) string {
	lower := strings.ToLower(scan)
	for _, pm := range paymentMethodWords {
		if strings.Contains(lower, pm.word) {
			return pm.method
		}
	}
	return ""
}

// merchantNoise are words that show up where a merchant name should be when the
// "at/to" pattern latches onto a sentence rather than a payee.
var merchantNoise = []string{
	"your account", "your card", "your a/c", "the following", "this transaction",
	"report it", "block upi", "customer care", "know more", "view details",
	// Footer boilerplate. Every alert ends with an unsubscribe line, and
	// "…click here to modify or unsubscribe from Insta Alerts" reads as
	// "to <merchant>" — which is how a monthly salary credit came to be
	// labelled with the unsubscribe text.
	"modify or unsubscribe", "unsubscribe", "insta alert", "click here",
	"log in", "login", "visit ", "download", "terms and conditions",
}

func cleanMerchant(raw string) string {
	merchant := strings.TrimSpace(raw)
	merchant = strings.Trim(merchant, ".,;:-")
	lower := strings.ToLower(merchant)
	for _, noise := range merchantNoise {
		if strings.HasPrefix(lower, noise) {
			return ""
		}
	}
	if len(merchant) < 3 {
		return ""
	}
	return merchant
}

// buildNarration describes the transaction using what the bank said about the
// payment, in decreasing order of usefulness.
//
// The sentence carrying the amount comes first. It names the payee, the
// account tail and the date in the bank's own words, which is what both a
// human scanning the ledger and the categorizer need. The subject line is a
// last resort and a poor one: "You have done a UPI txn. Check details!" is a
// description of the *email*, identical across every such alert, so it tells
// you nothing about where the money went and gives a model nothing to work
// with either.
func buildNarration(sentence, subject, merchant string) string {
	if detail := cleanNarration(sentence); detail != "" {
		return detail
	}
	if merchant != "" {
		return merchant
	}
	return cleanNarration(subject)
}

func cleanNarration(raw string) string {
	narration := strings.Join(strings.Fields(raw), " ")
	// Alert subjects routinely open with a decorative emoji.
	narration = strings.TrimSpace(strings.TrimLeft(narration, "❗⚠️✅🔔💰📩 "))
	// A greeting is not a description of anything.
	for _, prefix := range []string{"Dear Customer,", "Dear Customer", "Greetings from"} {
		narration = strings.TrimSpace(strings.TrimPrefix(narration, prefix))
	}
	if len(narration) > 180 {
		narration = strings.TrimSpace(narration[:180])
	}
	return narration
}

// parseFlexDate is extended here (beyond the HDFC layouts it started with) to
// cover the formats the generic patterns can match.
var flexDateLayouts = []string{
	"02-Jan-06", "02-Jan-2006", "02-01-06", "02-01-2006",
	"02/01/06", "02/01/2006", "02/Jan/2006", "02/Jan/06",
	"2 Jan 2006", "02 Jan 2006", "2 January 2006", "02 January 2006",
	"Jan 2, 2006", "January 2, 2006", "Jan 2 2006", "January 2 2006",
}

// ParseAlertDate normalizes any date shape the alert parsers can produce to
// YYYY-MM-DD, returning "" when nothing matched — callers substitute the
// message's own received date rather than storing a half-parsed string.
func ParseAlertDate(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ", "))
	s = strings.Join(strings.Fields(s), " ")
	for _, layout := range flexDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}
