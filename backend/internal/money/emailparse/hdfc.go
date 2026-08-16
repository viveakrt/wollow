package emailparse

import (
	"regexp"
	"strconv"
	"strings"

	"wollow/backend/internal/money/models"
)

// HDFC alert emails (sender alerts@hdfcbank.bank.in / Emailstatements.cards@hdfcbank.bank.in)
// come in a few templates, distinguished by wording in the body:
//
//   - Balance update: "The available balance in your account ending XX1234
//     is Rs. INR 1,234.56 as of 01-JAN-26." — snapshot only, not a
//     transaction; we surface it as a balance sync, no ParsedEmailTransaction.
//   - UPI debit: "Rs.1234.00 is debited from your account ending 1234
//     towards VPA name@bank (PAYEE NAME) on 01-01-26." plus a line
//     "UPI transaction reference no.: 123456789012."
//   - NEFT/deposit credit: label:value lines -
//     "Amount received: INR 1,234.00" / "Account: XX1234" /
//     "Date: 01-JAN-2026" / "Reference Details: NEFT Cr-..." /
//     "Available Balance: INR 12,345.00"

const HDFCSenderDomain = "hdfcbank.bank.in"

var (
	hdfcBalanceRe = regexp.MustCompile(`available balance in your account ending\s*(?:XX)?(\d{2,6})\s+is\s+Rs\.?\s*INR\s*([\d,]+\.\d{2})\s+as of\s+([\d]{1,2}-[A-Za-z]{3}-\d{2,4})`)
	hdfcDebitRe   = regexp.MustCompile(`Rs\.?\s*([\d,]+\.\d{2})\s+is debited from your account ending\s*(?:XX)?(\d{2,6})\s+towards\s+VPA\s+(\S+)\s*\(([^)]*)\)\s+on\s+([\d]{1,2}-[\d]{1,2}-\d{2,4})`)
	hdfcRefRe     = regexp.MustCompile(`UPI transaction reference no\.?:?\s*(\d+)`)

	hdfcAmountReceivedRe = regexp.MustCompile(`Amount received:\s*INR\s*([\d,]+\.\d{2})`)
	hdfcAccountRe        = regexp.MustCompile(`Account:\s*(?:XX)?(\d{2,6})`)
	hdfcDateRe           = regexp.MustCompile(`Date:\s*([\d]{1,2}-[A-Za-z]{3}-\d{4})`)
	hdfcRefDetailsRe     = regexp.MustCompile(`Reference Details:\s*([^\n]+)`)
	hdfcAvailBalRe       = regexp.MustCompile(`Available Balance:\s*INR\s*([\d,]+\.\d{2})`)
)

func parseAmountCommas(s string) float64 {
	s = strings.ReplaceAll(s, ",", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseFlexDate normalizes an alert's date to YYYY-MM-DD, or returns "" when
// none of the known layouts fit. Returning "" rather than the raw string is
// deliberate: a half-parsed date written into transactions.txn_date sorts
// nowhere and breaks every date-scoped query, so callers substitute the
// message's own received date instead.
func parseFlexDate(s string) string {
	return ParseAlertDate(s)
}

// ParseHDFCEmail attempts to extract a transaction from an HDFC alert email
// body. Returns (nil, false) if the email doesn't match a known template
// (e.g. it's a balance snapshot, or an unrelated HDFC email).
func ParseHDFCEmail(subject, body string) (*models.ParsedEmailTransaction, bool) {
	if m := hdfcDebitRe.FindStringSubmatch(body); m != nil {
		t := &models.ParsedEmailTransaction{
			Amount:        parseAmountCommas(m[1]),
			AccountLast4:  m[2],
			Merchant:      strings.TrimSpace(m[4]),
			PaymentMethod: "UPI",
			Type:          "expense",
			TxnDate:       parseFlexDate(m[5]),
			Narration:     "UPI-" + strings.TrimSpace(m[4]) + "-" + m[3],
		}
		if ref := hdfcRefRe.FindStringSubmatch(body); ref != nil {
			t.RefNo = ref[1]
		}
		return t, true
	}

	if m := hdfcAmountReceivedRe.FindStringSubmatch(body); m != nil {
		t := &models.ParsedEmailTransaction{
			Amount:        parseAmountCommas(m[1]),
			PaymentMethod: "NEFT",
			Type:          "income",
			Narration:     "Deposit alert",
		}
		if acc := hdfcAccountRe.FindStringSubmatch(body); acc != nil {
			t.AccountLast4 = acc[1]
		}
		if d := hdfcDateRe.FindStringSubmatch(body); d != nil {
			t.TxnDate = parseFlexDate(d[1])
		}
		if ref := hdfcRefDetailsRe.FindStringSubmatch(body); ref != nil {
			details := strings.TrimSpace(ref[1])
			t.Narration = details
			parts := strings.Split(details, "-")
			if len(parts) >= 3 {
				t.Merchant = strings.TrimSpace(parts[len(parts)-2])
			}
		}
		if bal := hdfcAvailBalRe.FindStringSubmatch(body); bal != nil {
			t.ClosingBalance = parseAmountCommas(bal[1])
		}
		return t, true
	}

	return nil, false
}
