package txnclassify

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"wollow/backend/internal/mail/ai"
)

// defaultConcurrency caps simultaneous model calls. It matches Mail's
// classifier for the same measured reason: a local runtime (LM Studio, Ollama)
// serves one model serially, so extra parallel requests add queueing
// contention rather than throughput. Cloud providers do benefit from more,
// hence the shared env override.
const defaultConcurrency = 3

func concurrencyLimit() int {
	if raw := os.Getenv("WOLLOW_CLASSIFY_CONCURRENCY"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return defaultConcurrency
}

type Status struct {
	Total       int `json:"total"`
	Classified  int `json:"classified"`
	Pending     int `json:"pending"`
	NeedsReview int `json:"needsReview"`
}

// Result is what one pass reports back through the job's Detail.
type Result struct {
	Classified int `json:"classified"`
	Applied    int `json:"applied"`
	Failed     int `json:"failed"`
	// Narrations is how many distinct narrations were sent to the model. One
	// answer covers every transaction sharing that narration, so this is also
	// the number of model calls made — usually about half the transaction
	// count, and the gap widens as history grows.
	Narrations int `json:"narrations"`
}

// group is every pending transaction that shares one narration. A narration
// names the payee and the rail it arrived on ("UPI-Vyapar-Vyapar"), so two
// transactions carrying the same one are the same kind of payment and cannot
// sensibly land in different categories. Classifying the group once is both
// cheaper and more consistent than asking about each occurrence and hoping the
// answers agree.
type group struct {
	key     string
	members []Txn
}

// groupKey folds the case and spacing a narration may vary in without changing
// what it identifies. Transactions with no narration fall back to the
// merchant, and anything with neither is its own group so it is still read.
func groupKey(t Txn) string {
	if narration := strings.Join(strings.Fields(strings.ToLower(t.Narration)), " "); narration != "" {
		return "n:" + narration
	}
	if merchant := strings.Join(strings.Fields(strings.ToLower(t.Merchant)), " "); merchant != "" {
		return "m:" + merchant
	}
	return fmt.Sprintf("id:%d", t.ID)
}

func groupByNarration(txns []Txn) []group {
	order := []string{}
	byKey := map[string][]Txn{}
	for _, t := range txns {
		k := groupKey(t)
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], t)
	}
	groups := make([]group, 0, len(order))
	for _, k := range order {
		groups = append(groups, group{key: k, members: byKey[k]})
	}
	return groups
}

// ProgressFunc is called as the pass advances so the caller can publish it.
type ProgressFunc func(done, total int)

// Run classifies transactions that have no classification yet, or the
// specific ids given. It returns what it managed to do; individual failures
// are logged and skipped so one unparseable answer never aborts the pass —
// that transaction is simply picked up by the next run.
func Run(
	ctx context.Context,
	database *sql.DB,
	provider ai.Provider,
	model string,
	ids []int64,
	report ProgressFunc,
) (Result, error) {
	expenseNames, incomeNames, allowed, err := loadCategories(database)
	if err != nil {
		return Result{}, fmt.Errorf("loading categories: %w", err)
	}
	if len(allowed) == 0 {
		return Result{}, fmt.Errorf("no categories exist to classify into")
	}

	pending, err := loadPending(database, ids)
	if err != nil {
		return Result{}, fmt.Errorf("loading unclassified transactions: %w", err)
	}
	if len(pending) == 0 {
		return Result{}, nil
	}

	groups := groupByNarration(pending)

	var (
		mu     sync.Mutex
		cursor int
		done   int
		result Result
		wg     sync.WaitGroup
	)

	// fail marks a whole group as failed: its members keep no classification
	// and are picked up again by the next pass.
	fail := func(g group, format string, args ...any) {
		log.Printf("txnclassify: "+format, args...)
		mu.Lock()
		result.Failed += len(g.members)
		done += len(g.members)
		at := done
		mu.Unlock()
		if report != nil {
			report(at, len(pending))
		}
	}

	worker := func() {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}

			mu.Lock()
			if cursor >= len(groups) {
				mu.Unlock()
				return
			}
			g := groups[cursor]
			cursor++
			mu.Unlock()

			// One member speaks for the group. It carries the narration they
			// all share; its amount and date are its own, which is why the
			// prompt leans on the narration rather than the figures.
			representative := g.members[0]

			raw, err := provider.Complete(ctx,
				BuildPrompt(representative, expenseNames, incomeNames), MaxTokens)
			if err != nil {
				fail(g, "narration %q failed: %v", g.key, err)
				continue
			}

			classification, err := Parse(raw, allowed, representative.Type)
			if err != nil {
				fail(g, "narration %q unparseable: %v", g.key, err)
				continue
			}

			// The same reading is recorded against every member, so each row
			// carries its own auditable classification rather than pointing at
			// a shared one that a later edit could contradict.
			var classified, applied, failed int
			for _, member := range g.members {
				wroteThrough, err := storeAndApply(database, member, classification, model)
				if err != nil {
					log.Printf("txnclassify: storing transaction %d failed: %v", member.ID, err)
					failed++
					continue
				}
				classified++
				if wroteThrough {
					applied++
				}
			}

			mu.Lock()
			result.Narrations++
			result.Classified += classified
			result.Applied += applied
			result.Failed += failed
			done += len(g.members)
			at := done
			mu.Unlock()
			if report != nil {
				report(at, len(pending))
			}
		}
	}

	n := min(concurrencyLimit(), len(groups))
	wg.Add(n)
	for range n {
		go worker()
	}
	wg.Wait()

	return result, ctx.Err()
}

// CurrentStatus reports classification progress across all transactions.
func CurrentStatus(database *sql.DB) (*Status, error) {
	var s Status
	if err := database.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&s.Total); err != nil {
		return nil, err
	}
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM transaction_classifications`,
	).Scan(&s.Classified); err != nil {
		return nil, err
	}
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM transaction_classifications WHERE needs_review = 1`,
	).Scan(&s.NeedsReview); err != nil {
		return nil, err
	}
	s.Pending = s.Total - s.Classified
	return &s, nil
}

// loadCategories returns the expense and income category names for the prompt,
// plus a lowercase->canonical lookup for validating what comes back.
func loadCategories(database *sql.DB) (expense, income []string, allowed map[string]string, err error) {
	rows, err := database.Query(`SELECT name, type FROM categories ORDER BY sort_order, id`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	allowed = map[string]string{}
	for rows.Next() {
		var name, ctype string
		if err := rows.Scan(&name, &ctype); err != nil {
			return nil, nil, nil, err
		}
		allowed[strings.ToLower(name)] = name
		if ctype == "income" {
			income = append(income, name)
		} else {
			expense = append(expense, name)
		}
	}
	return expense, income, allowed, rows.Err()
}

// loadPending returns transactions awaiting classification. With no ids it is
// everything unclassified, newest first; with ids it is exactly those, so a
// user can re-run the model over a specific selection.
func loadPending(database *sql.DB, ids []int64) ([]Txn, error) {
	query := `
		SELECT t.id, t.txn_date, a.name, a.account_type, t.withdrawal_amt, t.deposit_amt,
		       t.narration, t.ref_no, t.merchant, t.payment_method, t.type, t.closing_balance, t.notes
		FROM transactions t
		JOIN finance_accounts a ON a.id = t.account_id
		LEFT JOIN transaction_classifications tc ON tc.transaction_id = t.id
		WHERE tc.transaction_id IS NULL
		ORDER BY t.txn_date DESC, t.id DESC`
	var args []any

	if len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}
		// An explicit selection re-classifies even if it was done before: the
		// user asking again is a request to redo it, not to skip it.
		query = fmt.Sprintf(`
			SELECT t.id, t.txn_date, a.name, a.account_type, t.withdrawal_amt, t.deposit_amt,
			       t.narration, t.ref_no, t.merchant, t.payment_method, t.type, t.closing_balance
			FROM transactions t
			JOIN finance_accounts a ON a.id = t.account_id
			WHERE t.id IN (%s)
			ORDER BY t.txn_date DESC, t.id DESC`, strings.Join(placeholders, ","))
	}

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Txn
	for rows.Next() {
		var t Txn
		var withdrawal, deposit float64
		var closing sql.NullFloat64
		if err := rows.Scan(&t.ID, &t.Date, &t.AccountName, &t.AccountType, &withdrawal, &deposit,
			&t.Narration, &t.RefNo, &t.Merchant, &t.PaymentMethod, &t.Type, &closing, &t.Notes); err != nil {
			return nil, err
		}
		if deposit > 0 {
			t.Direction = "credit"
			t.Amount = deposit
		} else {
			t.Direction = "debit"
			t.Amount = withdrawal
		}
		if closing.Valid {
			t.ClosingBalance = &closing.Float64
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// storeAndApply persists the classification and writes through only what is
// safe to write.
//
// The apply rules are the whole reason this is trustworthy:
//   - category, merchant and payment method are filled ONLY where the
//     transaction has none. A value the user typed is never overwritten.
//   - the transaction's type is NEVER changed here. Reclassifying a row as a
//     transfer moves money out of the income/expense totals, which is too
//     destructive to do unattended — it is stored as a suggestion and applied
//     from the UI when the user accepts it.
//
// The stored `applied` flag means "nothing left for a person to decide", NOT
// "some field was filled". Those come apart exactly where it matters: filling
// the merchant on a row the model also thinks is really a transfer writes
// something through while leaving the transfer itself outstanding, and
// recording that as applied would hide the suggestion the user needs to see.
//
// It reports whether anything was written through to the transaction.
func storeAndApply(database *sql.DB, txn Txn, c *Classification, model string) (bool, error) {
	wroteThrough, err := applyToTransaction(database, txn, c)
	if err != nil {
		return false, err
	}
	// Outstanding when the model read the transaction as something other than
	// what it is, or admitted it was unsure.
	applied := c.Nature == txn.Type && !c.NeedsReview

	_, err = database.Exec(`
		INSERT INTO transaction_classifications (
			transaction_id, category, subcategory, merchant, payment_method, nature,
			transfer_kind, counterparty, is_recurring, is_bill, is_refund, needs_review,
			confidence, summary, model, classified_at, applied
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(transaction_id) DO UPDATE SET
			category = excluded.category,
			subcategory = excluded.subcategory,
			merchant = excluded.merchant,
			payment_method = excluded.payment_method,
			nature = excluded.nature,
			transfer_kind = excluded.transfer_kind,
			counterparty = excluded.counterparty,
			is_recurring = excluded.is_recurring,
			is_bill = excluded.is_bill,
			is_refund = excluded.is_refund,
			needs_review = excluded.needs_review,
			confidence = excluded.confidence,
			summary = excluded.summary,
			model = excluded.model,
			classified_at = excluded.classified_at,
			applied = excluded.applied`,
		txn.ID, c.Category, c.Subcategory, c.Merchant, c.PaymentMethod, c.Nature,
		c.TransferKind, c.Counterparty, boolToInt(c.IsRecurring), boolToInt(c.IsBill),
		boolToInt(c.IsRefund), boolToInt(c.NeedsReview), c.Confidence, c.Summary,
		model, time.Now().UTC().Format(time.RFC3339), boolToInt(applied))
	return wroteThrough, err
}

func applyToTransaction(database *sql.DB, txn Txn, c *Classification) (bool, error) {
	sets := []string{}
	args := []any{}

	if c.Category != "" {
		// Only where the row is still uncategorized — the SQL condition, not
		// just the Go check, so a category set between load and write survives.
		sets = append(sets, `category_id = COALESCE(category_id, (SELECT id FROM categories WHERE name = ?))`)
		args = append(args, c.Category)
	}
	if c.Merchant != "" && txn.Merchant == "" {
		sets = append(sets, `merchant = CASE WHEN merchant = '' THEN ? ELSE merchant END`)
		args = append(args, c.Merchant)
	}
	if c.PaymentMethod != "" && txn.PaymentMethod == "" {
		sets = append(sets, `payment_method = CASE WHEN payment_method = '' THEN ? ELSE payment_method END`)
		args = append(args, c.PaymentMethod)
	}
	if len(sets) == 0 {
		return false, nil
	}

	args = append(args, txn.ID)
	res, err := database.Exec(
		fmt.Sprintf(`UPDATE transactions SET %s WHERE id = ?`, strings.Join(sets, ", ")), args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
