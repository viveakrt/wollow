package emailparse

import (
	"regexp"
	"strings"

	"wollow/backend/internal/money/models"
)

// Reading broker order confirmations.
//
// A trade mail is not a transaction alert and must not be read as one. "BUY
// order ... for $245.73 is successful" carries an amount and a direction word,
// so the generic alert reader would happily book it as a $245.73 expense
// against a bank account — money that never left any account Money tracks, and
// a holding that never appears. Trades are matched first, and produce a
// position instead.

var (
	// INDmoney labels its order details as "Label: value" pairs. The trailing
	// lookahead stops a value at the next label rather than running on through
	// the rest of the line, which matters because the mail arrives as one
	// flowed paragraph.
	indTickerRe = regexp.MustCompile(`(?i)\bTicker\s*:\s*(.+?)\s*(?:Amount\s*:|Price\s*:|Shares\s*:|Order Type\s*:|US a/?c|$)`)
	indAmountRe = regexp.MustCompile(`(?i)\bAmount\s*:\s*([$₹]?)\s*([\d,]+(?:\.\d{1,4})?)`)
	indPriceRe  = regexp.MustCompile(`(?i)\bPrice\s*:\s*([$₹]?)\s*([\d,]+(?:\.\d{1,4})?)`)
	indSharesRe = regexp.MustCompile(`(?i)\bShares?\s*:\s*([\d,]+(?:\.\d{1,8})?)`)
	indOrderRe  = regexp.MustCompile(`(?i)\bOrder Type\s*:\s*([A-Za-z ]+?)\s*(?:US a/?c|Explore|Get the|$)`)
	indAcctRe   = regexp.MustCompile(`(?i)\bUS a/?c(?:\s*no)?\.?\s*:?\s*([A-Za-z0-9]{2,20})`)

	// The subject states the side and usually the instrument and amount, and
	// survives even when the body is image-only.
	tradeSideRe    = regexp.MustCompile(`(?i)\b(BUY|SELL)\b[^.\n]{0,20}\border\b`)
	tradeSubjectRe = regexp.MustCompile(`(?i)\b(BUY|SELL)\s+order\s+(?:of|for)\s+(.+?)\s+for\s+([$₹]?)\s*([\d,]+(?:\.\d{1,4})?)`)
)

// IsTradeEmail reports whether a message is a broker order confirmation, so
// ingest can route it to the holdings path instead of the ledger.
func IsTradeEmail(inst *Institution, subject, body string) bool {
	if inst == nil || inst.DefaultKind != KindInvestment {
		return false
	}
	scan := subject + "\n" + body
	if !tradeSideRe.MatchString(scan) && !tradeSubjectRe.MatchString(subject) {
		return false
	}
	// An order that did not go through is not a holding.
	lower := strings.ToLower(scan)
	for _, dead := range []string{"cancelled", "canceled", "rejected", "failed", "expired"} {
		if strings.Contains(lower, dead) {
			return false
		}
	}
	return true
}

// ParseTradeEmail extracts one securities order. Returns (nil, false) when the
// message does not describe a completed trade with a usable amount.
func ParseTradeEmail(inst *Institution, subject, body string) (*models.ParsedTrade, bool) {
	if !IsTradeEmail(inst, subject, body) {
		return nil, false
	}
	scan := subject + "\n" + truncateForScan(body)

	trade := &models.ParsedTrade{Side: "buy", Broker: inst.Issuer}
	if m := tradeSideRe.FindStringSubmatch(scan); m != nil {
		trade.Side = strings.ToLower(m[1])
	}

	// Body labels are authoritative; the subject fills whatever they omit.
	if m := indTickerRe.FindStringSubmatch(scan); m != nil {
		trade.Symbol = cleanSymbol(m[1])
	}
	if m := indAmountRe.FindStringSubmatch(scan); m != nil {
		trade.Currency = currencyFor(m[1])
		trade.Amount = parseAmountCommas(m[2])
	}
	if m := indPriceRe.FindStringSubmatch(scan); m != nil {
		if trade.Currency == "" {
			trade.Currency = currencyFor(m[1])
		}
		trade.Price = parseAmountCommas(m[2])
	}
	if m := indSharesRe.FindStringSubmatch(scan); m != nil {
		trade.Shares = parseAmountCommas(m[1])
	}
	if m := indOrderRe.FindStringSubmatch(scan); m != nil {
		trade.OrderType = strings.TrimSpace(m[1])
	}
	if m := indAcctRe.FindStringSubmatch(scan); m != nil {
		trade.AccountRef = strings.TrimSpace(m[1])
	}

	if m := tradeSubjectRe.FindStringSubmatch(subject); m != nil {
		if trade.Symbol == "" {
			trade.Symbol = cleanSymbol(m[2])
		}
		if trade.Amount == 0 {
			trade.Currency = currencyFor(m[3])
			trade.Amount = parseAmountCommas(m[4])
		}
	}

	// Amount is the one figure that cannot be inferred: without it there is no
	// cost basis, and a holding with units but no cost silently reports an
	// infinite gain.
	if trade.Amount == 0 {
		if trade.Shares == 0 || trade.Price == 0 {
			return nil, false
		}
		trade.Amount = trade.Shares * trade.Price
	}
	if trade.Symbol == "" {
		return nil, false
	}
	if trade.Shares == 0 && trade.Price > 0 {
		trade.Shares = trade.Amount / trade.Price
	}
	if trade.Price == 0 && trade.Shares > 0 {
		trade.Price = trade.Amount / trade.Shares
	}
	if trade.Currency == "" {
		trade.Currency = "INR"
	}
	trade.Kind = kindForTrade(inst, trade.Currency, scan)

	return trade, true
}

// kindForTrade decides which asset class a trade belongs to. Currency is the
// reliable signal for the split that matters here — a US brokerage order is
// priced in dollars — with the mail's own wording as a secondary hint.
func kindForTrade(inst *Institution, currency, scan string) string {
	lower := strings.ToLower(scan)
	if currency == "USD" || strings.Contains(lower, "us stock") || strings.Contains(lower, "us brokerage") {
		return "us_stock"
	}
	if strings.Contains(lower, "mutual fund") || strings.Contains(lower, "nav") {
		return "mutual_fund"
	}
	return "stock"
}

func currencyFor(marker string) string {
	switch strings.TrimSpace(marker) {
	case "$":
		return "USD"
	case "₹":
		return "INR"
	}
	return ""
}

// cleanSymbol trims the punctuation and stray markup that survive HTML-to-text
// conversion, without touching the name itself — "Take-Two Interactive
// Software Inc." has to come through whole, hyphen and full stop included.
func cleanSymbol(raw string) string {
	symbol := strings.Join(strings.Fields(raw), " ")
	symbol = strings.Trim(symbol, " \t,;:|")
	if len(symbol) > 120 {
		symbol = symbol[:120]
	}
	return symbol
}
