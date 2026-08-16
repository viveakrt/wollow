package emailparse

import (
	"regexp"
	"strings"
)

// Balance and limit facts carried by alert mail.
//
// These used to be thrown away: a balance-update email was classified as "not
// actionable" and dropped, so an account whose alerts were all balance
// snapshots showed a balance of zero forever. They are not transactions, but
// they are the most authoritative figure the bank ever sends — a snapshot
// anchors the running balance, and everything after it is derived from
// transactions.

// AccountFacts is what an alert says about the account itself, as opposed to
// about a transaction on it.
type AccountFacts struct {
	AccountLast4 string
	// Balance is the bank-reported balance; BalanceKnown distinguishes "the
	// bank said zero" from "no balance in this mail".
	Balance      float64
	BalanceKnown bool
	// AsOf is the date the balance was stated for, YYYY-MM-DD, or "" when the
	// mail didn't say (callers fall back to the message's own date).
	AsOf string
	// CreditLimit is the card's total sanctioned limit, reported by card spend
	// alerts alongside the remaining limit.
	CreditLimit      float64
	CreditLimitKnown bool
	AvailableLimit   float64
	AvailableKnown   bool
}

var (
	// "The available balance in your account ending XX4125 is Rs. INR
	// 2,08,870.09 as of 12-AUG-26." — and the many rewordings of it.
	availableBalanceRe = regexp.MustCompile(`(?i)\b(?:available|current|closing)?\s*balance\b[^.\n]{0,60}?` +
		`(?:rs\.?|inr|₹)[\s\x{00a0}]*(?:rs\.?|inr|₹)?[\s\x{00a0}]*([\d,]+(?:\.\d{1,2})?)`)
	balanceAsOfRe = regexp.MustCompile(`(?i)\bas\s+(?:of|on)\s+(\d{1,2}[-/](?:\d{1,2}|[A-Za-z]{3})[-/]\d{2,4}|` +
		`\d{1,2}\s+[A-Za-z]{3,9},?\s+\d{4})`)

	totalLimitRe     = regexp.MustCompile(`(?i)\btotal\s+credit\s+limit\*?\s*:?\s*(?:rs\.?|inr|₹)[\s\x{00a0}]*([\d,]+(?:\.\d{1,2})?)`)
	availableLimitRe = regexp.MustCompile(`(?i)\bavailable\s+(?:credit\s+)?limit\*?\s*:?\s*(?:rs\.?|inr|₹)[\s\x{00a0}]*([\d,]+(?:\.\d{1,2})?)`)
)

// ParseAccountFacts pulls balance and limit figures out of any alert email.
//
// It runs on every finance message, transaction alerts included: a spend alert
// that also reports the remaining balance tells us both things at once, and
// throwing the second one away is how balances went stale.
func ParseAccountFacts(subject, body string) AccountFacts {
	scan := subject + "\n" + truncateForScan(body)
	facts := AccountFacts{}

	if m := genericEndingRe.FindStringSubmatch(scan); m != nil {
		facts.AccountLast4 = m[1]
	} else if m := genericLast4Re.FindStringSubmatch(scan); m != nil {
		facts.AccountLast4 = m[1]
	}

	// A credit-limit line contains the word "limit", not "balance", so the two
	// searches don't collide — but "available limit" would match the balance
	// pattern's looser wording if it ran first, so limits are read first and
	// their text excluded from the balance search.
	if m := totalLimitRe.FindStringSubmatch(scan); m != nil {
		facts.CreditLimit = parseAmountCommas(m[1])
		facts.CreditLimitKnown = facts.CreditLimit > 0
	}
	if m := availableLimitRe.FindStringSubmatch(scan); m != nil {
		facts.AvailableLimit = parseAmountCommas(m[1])
		facts.AvailableKnown = true
	}

	balanceScan := availableLimitRe.ReplaceAllString(scan, " ")
	balanceScan = totalLimitRe.ReplaceAllString(balanceScan, " ")
	if m := availableBalanceRe.FindStringSubmatch(balanceScan); m != nil {
		facts.Balance = parseAmountCommas(m[1])
		facts.BalanceKnown = true
	}

	if m := balanceAsOfRe.FindStringSubmatch(scan); m != nil {
		facts.AsOf = ParseAlertDate(m[1])
	}

	return facts
}

// IsBalanceOnlyAlert reports whether a message is a pure balance snapshot with
// no transaction in it. Those are worth recording as a balance, but must not be
// turned into a transaction — a snapshot booked as income would inflate the
// month's totals by the whole account balance.
func IsBalanceOnlyAlert(subject, body string) bool {
	facts := ParseAccountFacts(subject, body)
	if !facts.BalanceKnown {
		return false
	}
	_, isTransaction := ParseGenericAlert(subject, body)
	return !isTransaction
}

// IsHDFCBalanceUpdate reports whether the email is HDFC's balance-snapshot
// template specifically. Kept as its own check because HDFC's wording is exact
// enough to be certain about, where the generic test is a judgement call.
func IsHDFCBalanceUpdate(body string) bool {
	return hdfcBalanceRe.MatchString(body) ||
		(strings.Contains(strings.ToLower(body), "available balance in your account ending") &&
			!genericDebitRe.MatchString(body) && !genericCreditRe.MatchString(body))
}
