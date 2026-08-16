package emailparse

import (
	"regexp"
	"strings"

	"wollow/backend/internal/money/models"
)

// Reading Zerodha's own mail: Coin mutual fund allotments, equity contract
// notes, and monthly demat holding statements.
//
// Two of these arrive as password-protected PDF attachments rather than plain
// text, which the ordinary alert readers never have to deal with — decryption
// and text extraction is the caller's job (see ingest.go), and the functions
// here work on the plain text that comes out of it.

// IsZerodhaFundingNoticeEmail reports whether a message is one of Zerodha's
// own notices about money moving in or out of the trading account balance —
// "Payment success", "Settlement of unused funds" — rather than a trade or a
// statement.
//
// These deliberately produce no transaction of their own. The real movement of
// money is already recorded on the bank side (an HDFC UPI debit, a credit),
// and that bank alert is what ingest.Persist reclassifies as an investment
// transfer — see ledger.DetectInvestmentBroker. Letting this email through to
// the generic alert reader is actively harmful: "₹2000.00 has been deposited
// to Zerodha equity from account 4125" contains both a credit verb and
// something that reads as an account tail, and would otherwise be booked as a
// phantom ₹2000 credit to an account that doesn't exist under Zerodha's name.
func IsZerodhaFundingNoticeEmail(subject string) bool {
	subject = strings.ToLower(strings.TrimSpace(subject))
	switch subject {
	case "payment success", "settlement of unused funds":
		return true
	}
	return false
}

func IsZerodhaContractNoteEmail(subject string) bool {
	return strings.Contains(strings.ToLower(subject), "contract note")
}

func IsZerodhaHoldingStatementEmail(subject string) bool {
	lower := strings.ToLower(subject)
	return strings.Contains(lower, "holding statement") || strings.Contains(lower, "transaction with holding")
}

// coinAllotmentRe matches one fund block in a Coin allotment report. A report
// can list several funds; FindAllStringSubmatch walks them in order.
//
// Layout (real sample, one block):
//
//	MOTILAL OSWAL MIDCAP FUND - DIRECT GROWTH
//	Folio no.: 910102601403, NAV: ₹118.27  Stamp Duty: ₹0.5
//	₹9999.5
//	84.547 units
var coinAllotmentRe = regexp.MustCompile(
	`(?im)^([A-Z][A-Za-z0-9 &().,/-]{2,90})\r?\n` +
		`Folio no\.:\s*([A-Za-z0-9]+),\s*NAV:\s*₹\s*([\d,]+\.?\d*)[^\r\n]*\r?\n` +
		`₹\s*([\d,]+\.?\d*)\r?\n` +
		`([\d,]+\.?\d*)\s*units`)

// ParseCoinAllotment extracts one buy trade per fund in a Coin allotment
// report. The folio number is the identifier: the same fund, added to over
// several months, must accumulate onto one holding rather than opening a new
// one per allotment.
func ParseCoinAllotment(subject, body string) []models.ParsedTrade {
	date := parseFlexDate(subjectDateRe.FindString(subject))

	var out []models.ParsedTrade
	for _, m := range coinAllotmentRe.FindAllStringSubmatch(body, -1) {
		amount := parseAmountCommas(m[4])
		units := parseAmountCommas(m[5])
		if amount == 0 || units == 0 {
			continue // no cost or no units is not a usable trade
		}
		out = append(out, models.ParsedTrade{
			Side:       "buy",
			Symbol:     strings.TrimSpace(m[1]),
			Identifier: strings.TrimSpace(m[2]), // folio number
			Shares:     units,
			Price:      amount / units,
			Amount:     amount,
			Currency:   "INR",
			OrderType:  "allotment",
			TradeDate:  date,
			Broker:     "Zerodha",
			Kind:       "mutual_fund",
		})
	}
	return out
}

// subjectDateRe pulls a DD-MM-YYYY date off a subject line such as
// "Coin by Zerodha - Allotment report - 10-08-2026".
var subjectDateRe = regexp.MustCompile(`\d{2}-\d{2}-\d{4}`)

// isinRe matches a bare ISIN: two letters, nine alphanumerics, one check
// digit — e.g. INE155A01022. Used as a line-anchor in both the contract note
// and the holdings table, which otherwise share almost no formatting.
var isinRe = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)

// numericLineRe matches a lone number as its own line — the shape every
// column in these PDF-extracted tables takes once the surrounding table
// borders are gone. The decimal part is optional: quantity columns print as
// bare integers ("6", "0") while WAP/value columns always carry one ("332.0000").
// The Unicode minus sign (−, U+2212) shows up in the contract note's
// net-obligation column, so it is accepted alongside the ASCII hyphen.
var numericLineRe = regexp.MustCompile(`^[-−]?[\d,]+(\.\d+)?$`)

// tradeDateRe reads a contract note's "Trade Date:\n14/08/2026" pair.
var tradeDateRe = regexp.MustCompile(`(?i)Trade Date:\s*\r?\n?\s*(\d{2})/(\d{2})/(\d{4})`)

// ParseZerodhaContractNoteText extracts one trade per ISIN traded that day
// from a decrypted contract note's plain text.
//
// It reads the per-ISIN summary block ("Equity / Security Description" table)
// rather than the Annexure's per-order rows: the summary already nets same-day
// orders in the same instrument down to a single buy total and sell total,
// which is exactly the one-or-two-trades-per-ISIN shape the ledger wants,
// without the ledger having to do that netting itself.
func ParseZerodhaContractNoteText(text string) []models.ParsedTrade {
	tradeDate := ""
	if m := tradeDateRe.FindStringSubmatch(text); m != nil {
		tradeDate = m[3] + "-" + m[2] + "-" + m[1]
	}

	lines := splitLines(text)
	var out []models.ParsedTrade
	for i := 0; i < len(lines); i++ {
		if !isinRe.MatchString(lines[i]) {
			continue
		}
		// Layout, 14 lines starting at the ISIN (see the sample this was
		// built from): ISIN, Symbol, then 11 numeric columns —
		// [BuyQty, BuyWAP, BuyBrokerage/Share, BuyWAPAfterBrokerage,
		//  TotalBuyValueAfterBrokerage, SellQty, SellWAP,
		//  SellBrokerage/Share, SellWAPAfterBrokerage,
		//  TotalSellValueAfterBrokerage, NetQty, NetObligation].
		if i+13 >= len(lines) {
			continue
		}
		isin := lines[i]
		symbol := lines[i+1]
		buyQty := parseAmountCommas(lines[i+2])
		buyValue := parseAmountCommas(lines[i+6])
		sellQty := parseAmountCommas(lines[i+7])
		sellValue := parseAmountCommas(lines[i+11])

		// A genuine ISIN block is all-numeric from i+2 onward; if it isn't,
		// this "ISIN-shaped" line was something else (a PAN, a GSTIN) and the
		// block is skipped rather than guessed at.
		if !allNumericLines(lines[i+2 : i+14]) {
			continue
		}

		if buyQty > 0 && buyValue > 0 {
			out = append(out, models.ParsedTrade{
				Side: "buy", Symbol: symbol, Identifier: isin,
				Shares: buyQty, Price: buyValue / buyQty, Amount: buyValue,
				Currency: "INR", TradeDate: tradeDate, Broker: "Zerodha", Kind: "stock",
			})
		}
		if sellQty > 0 && sellValue > 0 {
			out = append(out, models.ParsedTrade{
				Side: "sell", Symbol: symbol, Identifier: isin,
				Shares: sellQty, Price: sellValue / sellQty, Amount: sellValue,
				Currency: "INR", TradeDate: tradeDate, Broker: "Zerodha", Kind: "stock",
			})
		}
		i += 13 // skip past this block
	}
	return out
}

// ZerodhaHolding is one row of a demat "Holdings as on <date>" table: a
// market snapshot, not a trade. Rate/Value are as of AsOf, not as of purchase.
type ZerodhaHolding struct {
	ISIN  string
	Name  string
	Units float64
	Rate  float64
	Value float64
}

var holdingsAsOfRe = regexp.MustCompile(`(?i)Holdings as on\s+(\d{4}-\d{2}-\d{2})`)

// ParseZerodhaHoldingsSnapshot reads the point-in-time holdings table at the
// end of a monthly demat statement.
//
// This is a valuation, not a purchase record — the ledger side (ingest.go)
// uses it two ways: to move every listed holding's price forward (so the
// portfolio's gain reflects the market rather than sitting frozen at whatever
// it last cost), and, for a holding with no trade history at all, as a
// one-time opening balance — see persistZerodhaHoldings.
func ParseZerodhaHoldingsSnapshot(text string) (asOf string, holdings []ZerodhaHolding) {
	loc := holdingsAsOfRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", nil
	}
	asOf = text[loc[2]:loc[3]]
	table := text[loc[1]:]
	if end := strings.Index(table, "\nTotal:"); end != -1 {
		table = table[:end]
	}

	lines := splitLines(table)
	for i := 0; i < len(lines); i++ {
		if !isinRe.MatchString(lines[i]) {
			continue
		}
		isin := lines[i]

		// The company name spans a variable number of lines ("HDFC BANK-EQ1/-"
		// is one line; "BANK OF" / "MAHARASHTRA" is two) — everything between
		// the ISIN and the first numeric-looking line is the name.
		j := i + 1
		for j < len(lines) && !numericLineRe.MatchString(lines[j]) {
			j++
		}
		name := strings.TrimSpace(strings.Join(lines[i+1:j], " "))

		// Nine numeric columns follow the name: Curr.Bal, Free Bal, Pldg.Bal,
		// Earmark Bal, Demat, Remat, Lockin, Rate, Value. Only the first
		// (quantity) and the last two (price and its total) are needed.
		if j+8 >= len(lines) || !allNumericLines(lines[j:j+9]) {
			i = j
			continue
		}
		units := parseAmountCommas(lines[j])
		rate := parseAmountCommas(lines[j+7])
		value := parseAmountCommas(lines[j+8])
		if units > 0 {
			holdings = append(holdings, ZerodhaHolding{ISIN: isin, Name: name, Units: units, Rate: rate, Value: value})
		}
		i = j + 8
	}
	return asOf, holdings
}

func splitLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, strings.TrimSpace(strings.TrimRight(l, "\r")))
	}
	return out
}

// KindForISIN reads India's ISIN convention to tell an equity from a mutual
// fund unit: scheme ISINs are issued under the "INF" prefix, ordinary listed
// securities under "INE" (and other two-letter-plus-F/E variants generally).
// This is what routes a demat holding statement's mixed list of stocks and
// demat-held funds to the right dashboard tab without needing a lookup table.
func KindForISIN(isin string) string {
	if strings.HasPrefix(isin, "INF") {
		return "mutual_fund"
	}
	return "stock"
}

func allNumericLines(lines []string) bool {
	for _, l := range lines {
		if !numericLineRe.MatchString(l) {
			return false
		}
	}
	return true
}
