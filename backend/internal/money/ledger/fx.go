package ledger

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Bringing foreign holdings into a rupee net worth.
//
// A US stock is priced in dollars. Adding that number to a rupee total
// overstates it by the exchange rate, and leaving it out understates net
// worth — neither is acceptable once the user asks for those holdings to
// count. So a rate is required, and it has to come from somewhere defensible:
// either the user typed it, or it was read off their own bank's forex
// remittances. Nothing here invents one.

// Rate is an exchange rate and where it came from.
type Rate struct {
	Currency   string  `json:"currency"`
	INRPerUnit float64 `json:"inrPerUnit"`
	AsOf       string  `json:"asOf"`
	Source     string  `json:"source"`
	Note       string  `json:"note"`
}

// forexNarrationRe reads the foreign amount out of an outward remittance.
// HDFC writes these as "RFX 110826BTT07660 USD1036.18@96.19" — the rate is
// stated, but the narration is often truncated before it, so the reliable
// figure is the foreign amount divided into what actually left the account.
var forexNarrationRe = regexp.MustCompile(`(?i)\b(USD|EUR|GBP|AED|SGD)\s?([\d,]+(?:\.\d+)?)`)

// RateToINR returns how many rupees one unit of the currency is worth, or 0
// when nothing credible is known. INR is always 1.
func RateToINR(db Queryer, currency string) float64 {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "INR" {
		return 1
	}
	var rate float64
	if err := db.QueryRow(
		`SELECT inr_per_unit FROM fx_rates WHERE currency = ?`, currency,
	).Scan(&rate); err != nil || rate <= 0 {
		return 0
	}
	return rate
}

// SetRate records a rate the user chose.
func SetRate(db Queryer, currency string, inrPerUnit float64, asOf, source, note string) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "INR" {
		return fmt.Errorf("INR needs no rate")
	}
	if inrPerUnit <= 0 {
		return fmt.Errorf("rate must be greater than zero")
	}
	_, err := db.Exec(`
		INSERT INTO fx_rates (currency, inr_per_unit, as_of, source, note, updated_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(currency) DO UPDATE SET
			inr_per_unit = excluded.inr_per_unit,
			as_of = excluded.as_of,
			source = excluded.source,
			note = excluded.note,
			updated_at = excluded.updated_at`,
		currency, inrPerUnit, asOf, source, note)
	return err
}

// DeriveRateFromForex reads a rate off the user's own outward remittances.
//
// A transaction that sent USD 1,036.18 and took INR 99,670.15 out of the
// account states a rate of 96.19 — their bank's actual conversion, including
// its spread, which is a better answer for "what is my dollar holding worth to
// me" than a mid-market quote would be. Returns 0 when no such transaction
// exists.
func DeriveRateFromForex(db *sql.DB, currency string) (float64, string, string) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "INR" {
		return 0, "", ""
	}

	rows, err := db.Query(`
		SELECT txn_date, withdrawal_amt, narration
		FROM transactions
		WHERE withdrawal_amt > 0 AND narration LIKE '%' || ? || '%'
		ORDER BY txn_date DESC LIMIT 25`, currency)
	if err != nil {
		return 0, "", ""
	}
	defer rows.Close()

	for rows.Next() {
		var date, narration string
		var inr float64
		if err := rows.Scan(&date, &inr, &narration); err != nil {
			continue
		}
		m := forexNarrationRe.FindStringSubmatch(narration)
		if m == nil || !strings.EqualFold(m[1], currency) {
			continue
		}
		foreign, err := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64)
		if err != nil || foreign <= 0 {
			continue
		}
		rate := inr / foreign
		// A plausibility band. A truncated narration ("USD1036" for 1036.18)
		// shifts the answer by a fraction of a percent, but a mis-read that
		// grabbed the wrong number entirely would land far outside it, and a
		// wrong rate silently rescales net worth.
		if rate < 20 || rate > 500 {
			continue
		}
		note := fmt.Sprintf("from your forex transaction on %s (%s %s for %.2f INR)",
			date, currency, m[2], inr)
		return rate, date, note
	}
	return 0, "", ""
}

// EnsureRate returns the known rate, deriving and storing one from the user's
// forex history if none has been set. A rate the user typed is never replaced.
func EnsureRate(db *sql.DB, currency string) float64 {
	if rate := RateToINR(db, currency); rate > 0 {
		return rate
	}
	rate, asOf, note := DeriveRateFromForex(db, currency)
	if rate <= 0 {
		return 0
	}
	if err := SetRate(db, currency, rate, asOf, "derived", note); err != nil {
		return rate
	}
	return rate
}
