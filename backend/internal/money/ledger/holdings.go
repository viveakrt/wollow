package ledger

import (
	"database/sql"
	"fmt"
	"strings"

	"wollow/backend/internal/money/models"
)

// Turning broker order confirmations into holdings.
//
// The rule that matters: a position is derived from its trades, never written
// directly. Buying the same stock in three different months has to produce one
// holding whose cost is the sum of what was paid, and re-reading the mailbox
// must not buy it again — both fall out of storing trades and recomputing.

// ResolveHolding finds the position a trade belongs to, creating it if this is
// the first trade for that instrument.
//
// Identifier (an ISIN, a mutual fund folio number) is tried first when the
// trade carries one, because it is the one thing a broker never spells two
// ways. Name matching is the fallback for brokers that don't state an
// identifier (INDmoney's order mails don't), and stays normalised for the
// same reason it always has: "Take-Two Interactive Software Inc." and
// "Take-Two Interactive Software Inc" are one company, not two, and an exact
// comparison would open a second position and split the cost basis between
// them.
func ResolveHolding(db *sql.DB, trade *models.ParsedTrade) (int64, error) {
	symbol := strings.TrimSpace(trade.Symbol)
	identifier := strings.TrimSpace(trade.Identifier)
	if symbol == "" && identifier == "" {
		return 0, fmt.Errorf("trade has no instrument")
	}

	if identifier != "" {
		var id int64
		err := db.QueryRow(
			`SELECT id FROM investments WHERE institution = ? AND identifier = ? ORDER BY id LIMIT 1`,
			trade.Broker, identifier,
		).Scan(&id)
		if err == nil {
			return id, nil
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
	}

	if symbol != "" {
		rows, err := db.Query(`SELECT id, name FROM investments WHERE institution = ? ORDER BY id`, trade.Broker)
		if err != nil {
			return 0, err
		}
		want := normalizeSymbol(symbol)
		var match int64
		for rows.Next() {
			var id int64
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return 0, err
			}
			if match == 0 && normalizeSymbol(name) == want {
				match = id
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
		if match != 0 {
			return match, nil
		}
	}

	kind := trade.Kind
	if kind == "" {
		kind = "stock"
	}
	currency := trade.Currency
	if currency == "" {
		currency = "INR"
	}
	name := symbol
	if name == "" {
		name = identifier
	}
	res, err := db.Exec(`
		INSERT INTO investments
			(kind, institution, name, identifier, currency, invested_amount, current_value,
			 start_date, status, source, notes)
		VALUES (?, ?, ?, ?, ?, 0, 0, ?, 'active', 'email', '')`,
		kind, trade.Broker, name, identifier, currency, trade.TradeDate)
	if err != nil {
		return 0, fmt.Errorf("creating holding for %s: %w", name, err)
	}
	return res.LastInsertId()
}

// normalizeSymbol reduces an instrument name to the letters and digits in it,
// lowercased. That is enough to unify the punctuation and capitalisation a
// broker varies between mails, while still keeping genuinely different
// instruments apart.
func normalizeSymbol(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RecordTrade stores one trade and refreshes the position it belongs to.
//
// dedupeKey is the source email's Message-ID. It is what makes a re-read of
// the mailbox harmless: the insert is ignored and the position is recomputed
// to the same figures rather than accumulating a second purchase.
func RecordTrade(db *sql.DB, investmentID int64, trade *models.ParsedTrade, dedupeKey string) (bool, error) {
	res, err := db.Exec(`
		INSERT OR IGNORE INTO investment_trades
			(investment_id, side, shares, price, amount, currency, trade_date, order_type, source, dedupe_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'email', ?)`,
		investmentID, trade.Side, trade.Shares, trade.Price, trade.Amount,
		trade.Currency, trade.TradeDate, trade.OrderType, dedupeKey)
	if err != nil {
		return false, fmt.Errorf("recording trade: %w", err)
	}
	added, _ := res.RowsAffected()
	if err := RecomputeHolding(db, investmentID); err != nil {
		return added > 0, err
	}
	return added > 0, nil
}

// RecomputeHolding rebuilds a position from its trades.
//
// Units and cost are sums over buys minus sells. Current value uses the last
// known price when there is one and falls back to cost otherwise — a holding
// nobody has priced yet is worth what was paid for it as far as net worth is
// concerned, which is honest, whereas zero would quietly delete it from the
// portfolio.
func RecomputeHolding(db Queryer, investmentID int64) error {
	var buyShares, sellShares, buyAmount, sellAmount sql.NullFloat64
	err := db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN side = 'buy'  THEN shares END), 0),
			COALESCE(SUM(CASE WHEN side = 'sell' THEN shares END), 0),
			COALESCE(SUM(CASE WHEN side = 'buy'  THEN amount END), 0),
			COALESCE(SUM(CASE WHEN side = 'sell' THEN amount END), 0)
		FROM investment_trades WHERE investment_id = ?`, investmentID).
		Scan(&buyShares, &sellShares, &buyAmount, &sellAmount)
	if err != nil {
		return fmt.Errorf("summing trades: %w", err)
	}

	units := buyShares.Float64 - sellShares.Float64
	// Cost of what is still held. Selling returns a proportional slice of the
	// cost basis rather than the sale proceeds, so a profitable sale does not
	// leave the remaining shares looking free.
	invested := buyAmount.Float64
	if buyShares.Float64 > 0 && sellShares.Float64 > 0 {
		avgCost := buyAmount.Float64 / buyShares.Float64
		invested = buyAmount.Float64 - (avgCost * sellShares.Float64)
	}
	if invested < 0 {
		invested = 0
	}

	var lastPrice sql.NullFloat64
	db.QueryRow(`SELECT last_price FROM investments WHERE id = ?`, investmentID).Scan(&lastPrice)
	currentValue := invested
	if lastPrice.Valid && lastPrice.Float64 > 0 && units > 0 {
		currentValue = lastPrice.Float64 * units
	}

	status := "active"
	if units <= 0 && sellShares.Float64 > 0 {
		status = "closed"
	}

	_, err = db.Exec(`
		UPDATE investments
		SET units = ?, invested_amount = ?, current_value = ?, status = ?,
		    updated_at = datetime('now')
		WHERE id = ?`, units, invested, currentValue, status, investmentID)
	if err != nil {
		return fmt.Errorf("updating holding %d: %w", investmentID, err)
	}
	return nil
}

// RecordHoldingSnapshot brings a holding to a known market price and, only if
// it has no trade history at all yet, seeds its opening position from the
// snapshot quantity.
//
// A monthly statement is a valuation, not a purchase record: treating every
// line of every statement as a fresh buy would re-add the same shares on
// every statement processed. So the snapshot seeds a position at most ONCE —
// dedupeKey is keyed on the instrument alone, not the statement period, so a
// second or third monthly statement for the same holding updates its price
// but never re-seeds its quantity. A holding that already has real trades
// (from a contract note, say) is never seeded at all; the snapshot then only
// ever moves its price.
//
// The seeded quantity's cost is unknown — a demat statement states a
// valuation, not what was paid — so it is recorded at the snapshot's own
// market value, which reports a gain of exactly zero for that portion until a
// real trade narrows it. The holding's notes say so explicitly rather than
// leaving a silent zero for the user to wonder about.
func RecordHoldingSnapshot(db *sql.DB, trade models.ParsedTrade, units, rate, value float64, asOf string) (int64, error) {
	investmentID, err := ResolveHolding(db, &trade)
	if err != nil {
		return 0, err
	}

	var existingTrades int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM investment_trades WHERE investment_id = ?`, investmentID,
	).Scan(&existingTrades); err != nil {
		return investmentID, err
	}
	if existingTrades == 0 && units > 0 {
		seed := &models.ParsedTrade{
			Side: "buy", Shares: units, Price: rate, Amount: value,
			Currency: trade.Currency, TradeDate: asOf, OrderType: "opening_balance", Broker: trade.Broker,
		}
		dedupeKey := "seed:" + trade.Broker + ":" + trade.Identifier
		if _, err := RecordTrade(db, investmentID, seed, dedupeKey); err != nil {
			return investmentID, err
		}
		db.Exec(`UPDATE investments SET notes = ? WHERE id = ? AND notes = ''`,
			"Opening quantity taken from a Zerodha holding statement dated "+asOf+
				"; cost basis approximated at that date's market value because the "+
				"statement states a valuation, not a purchase price. Later trades narrow this.",
			investmentID)
	}

	// Price moves forward only. Mailbox order doesn't guarantee statement-period
	// order, so an older statement arriving after a newer one must not drag the
	// reported price backwards.
	var lastPriceAt string
	db.QueryRow(`SELECT last_price_at FROM investments WHERE id = ?`, investmentID).Scan(&lastPriceAt)
	if rate <= 0 || (lastPriceAt != "" && asOf < lastPriceAt) {
		return investmentID, nil
	}
	return investmentID, SetHoldingPrice(db, investmentID, rate, asOf)
}

// SetHoldingPrice records a per-unit price and re-values the position.
func SetHoldingPrice(db Queryer, investmentID int64, price float64, asOf string) error {
	if _, err := db.Exec(`
		UPDATE investments SET last_price = ?, last_price_at = ?, updated_at = datetime('now')
		WHERE id = ?`, price, asOf, investmentID); err != nil {
		return fmt.Errorf("setting price: %w", err)
	}
	return RecomputeHolding(db, investmentID)
}
