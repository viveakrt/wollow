package moneyapi

import (
	"database/sql"
	"net/http"
	"time"

	"wollow/backend/internal/money/ledger"
	"wollow/backend/internal/money/models"
	"wollow/backend/internal/platform/httpx"
)

type categoryBreakdownItem struct {
	CategoryName string  `json:"categoryName"`
	Color        string  `json:"color"`
	Amount       float64 `json:"amount"`
}

type accountSummaryItem struct {
	ID                int64   `json:"id"`
	Name              string  `json:"name"`
	AccountType       string  `json:"accountType"`
	Bank              string  `json:"bank"`
	CurrentBalance    float64 `json:"currentBalance"`
	CreditLimit       float64 `json:"creditLimit"`
	IncludeInNetWorth bool    `json:"includeInNetworth"`
}

// liabilityTypes are the account types whose balance is money owed rather than
// money held. Summing every account's balance into one "net worth" figure —
// which is what this used to do — counts a card's outstanding spend as savings
// whenever its balance happens to be positive.
var liabilityTypes = map[string]bool{
	"credit_card": true,
	"loan":        true,
}

// liquidTypes are the accounts whose money is spendable tomorrow: bank
// balances, wallets, cash. Deposits, investments and anything owed are not
// liquid. This is the screenshot's "Liquid Net Worth" figure.
var liquidTypes = map[string]bool{
	"bank":   true,
	"wallet": true,
	"cash":   true,
}

// foreignHoldingTotal is the portfolio held in one non-rupee currency, with
// the rate used to bring it into net worth and where that rate came from.
type foreignHoldingTotal struct {
	Currency string  `json:"currency"`
	Value    float64 `json:"value"`
	Invested float64 `json:"invested"`
	Count    int     `json:"count"`
	// Rate is rupees per unit; 0 when none is known, in which case ValueINR is
	// 0 and the currency is listed in UnconvertedCurrencies.
	Rate       float64 `json:"rate"`
	ValueINR   float64 `json:"valueInr"`
	RateSource string  `json:"rateSource"`
	RateNote   string  `json:"rateNote"`
	RateAsOf   string  `json:"rateAsOf"`
}

type merchantSummaryItem struct {
	Merchant string  `json:"merchant"`
	Amount   float64 `json:"amount"`
	Count    int     `json:"count"`
}

// upcomingBillItem is a bill discovered in the user's mail, carried onto the
// dashboard so money owed shows up next to money held. SourceEmail is set when
// the bill came from a statement email, which is most of them.
type upcomingBillItem struct {
	ID          int64               `json:"id"`
	Issuer      string              `json:"issuer"`
	CardLast4   string              `json:"cardLast4"`
	TotalDue    *float64            `json:"totalDue,omitempty"`
	MinimumDue  *float64            `json:"minimumDue,omitempty"`
	DueDate     string              `json:"dueDate"`
	Status      string              `json:"status"`
	SourceEmail *models.SourceEmail `json:"sourceEmail,omitempty"`
}

// netWorthPoint is one month-end sample of the net worth trend. NetWorth is
// derived by walking the transaction history backwards from today's balances;
// investments are carried at their current value since holdings don't keep a
// price history here.
type netWorthPoint struct {
	Month    string  `json:"month"` // YYYY-MM
	NetWorth float64 `json:"netWorth"`
}

// cashFlowSummary breaks the period's money movement into the lines the
// screenshots show: what came in, what was spent, and what merely moved —
// split by where it moved to, because "sent to Zerodha" and "sent to Mom"
// mean different things even though neither is an expense.
type cashFlowSummary struct {
	Income        float64 `json:"income"`
	Expenses      float64 `json:"expenses"`
	SelfTransfers float64 `json:"selfTransfers"`
	ToInvestments float64 `json:"toInvestments"`
	ToFamily      float64 `json:"toFamily"`
}

type dashboardSummary struct {
	// NetWorth is assets minus liabilities over the accounts the user counts,
	// with investments counted as assets — not the sum of every balance.
	NetWorth         float64 `json:"netWorth"`
	TotalAssets      float64 `json:"totalAssets"`
	TotalLiabilities float64 `json:"totalLiabilities"`
	// LiquidAssets is the subset of assets that is spendable now: bank,
	// wallet and cash balances (positive ones) of counted accounts.
	LiquidAssets float64 `json:"liquidAssets"`
	// InvestmentValue counts rupee holdings only; see ForeignHoldings.
	InvestmentValue float64 `json:"investmentValue"`
	// ForeignHoldings are holdings priced in another currency, each with the
	// rate used to bring it into InvestmentValue.
	ForeignHoldings []foreignHoldingTotal `json:"foreignHoldings"`
	// ForeignConvertedINR is how much of InvestmentValue came from converting
	// them, so the UI can show what rests on a rate rather than on rupees.
	ForeignConvertedINR float64 `json:"foreignConvertedInr"`
	// UnconvertedCurrencies are held but excluded for want of a rate.
	UnconvertedCurrencies []string `json:"unconvertedCurrencies"`
	TotalIncome           float64  `json:"totalIncome"`
	TotalExpenses         float64  `json:"totalExpenses"`
	TotalSavings          float64  `json:"totalSavings"`
	// ExcludedAccounts is how many accounts exist but are switched out of the
	// totals, so the UI can say "3 accounts not counted" instead of silently
	// showing smaller numbers.
	ExcludedAccounts int `json:"excludedAccounts"`
	// FamilyNetWorth/-Assets/-Liabilities sum every account_type='family'
	// account's own balance — independent of NetWorth above, which those
	// accounts are normally excluded from. Without this, marking an account
	// "family" makes its money disappear from the dashboard entirely rather
	// than showing up as its own total.
	FamilyNetWorth     float64                 `json:"familyNetWorth"`
	FamilyAssets       float64                 `json:"familyAssets"`
	FamilyLiabilities  float64                 `json:"familyLiabilities"`
	FamilyAccountCount int                     `json:"familyAccountCount"`
	TransactionCount   int                     `json:"transactionCount"`
	CashFlow           cashFlowSummary         `json:"cashFlow"`
	NetWorthTrend      []netWorthPoint         `json:"netWorthTrend"`
	Accounts           []accountSummaryItem    `json:"accounts"`
	ExpenseBreakdown   []categoryBreakdownItem `json:"expenseBreakdown"`
	TopMerchants       []merchantSummaryItem   `json:"topMerchants"`
	UpcomingBills      []upcomingBillItem      `json:"upcomingBills"`
}

// handleDashboardSummary aggregates figures for the dashboard. Accepts
// optional ?from=YYYY-MM-DD&to=YYYY-MM-DD query params to scope the period;
// defaults to the current calendar month.
//
// Every aggregate honors finance_accounts.include_in_networth: an account
// switched out of the totals stops moving net worth AND stops contributing
// income/expense rows — a tracked family account's groceries are not the
// owner's groceries.
func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from := q.Get("from")
	to := q.Get("to")
	if from == "" {
		from = "date('now','start of month')"
	} else {
		from = "'" + from + "'"
	}
	if to == "" {
		to = "date('now','start of month','+1 month','-1 day')"
	} else {
		to = "'" + to + "'"
	}

	summary := dashboardSummary{
		Accounts:         []accountSummaryItem{},
		ExpenseBreakdown: []categoryBreakdownItem{},
		TopMerchants:     []merchantSummaryItem{},
		NetWorthTrend:    []netWorthPoint{},
	}

	// counted joins are shared by every transaction aggregate below.
	const countedJoin = ` JOIN finance_accounts fa ON fa.id = t.account_id AND fa.include_in_networth = 1 `

	row := s.DB.QueryRow(`
		SELECT COALESCE(SUM(t.deposit_amt),0), COALESCE(SUM(t.withdrawal_amt),0), COUNT(*)
		FROM transactions t` + countedJoin + `
		WHERE t.txn_date >= ` + from + ` AND t.txn_date <= ` + to + ` AND t.type != 'transfer'`)
	if err := row.Scan(&summary.TotalIncome, &summary.TotalExpenses, &summary.TransactionCount); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	summary.TotalSavings = summary.TotalIncome - summary.TotalExpenses
	summary.CashFlow.Income = summary.TotalIncome
	summary.CashFlow.Expenses = summary.TotalExpenses

	// Transfers by kind: money that moved without being earned or spent.
	// Self-transfers count each pair's outflow leg only (the deposit leg of a
	// linked pair would double it).
	kindRows, err := s.DB.Query(`
		SELECT t.transfer_kind, COALESCE(SUM(t.withdrawal_amt),0)
		FROM transactions t` + countedJoin + `
		WHERE t.txn_date >= ` + from + ` AND t.txn_date <= ` + to + `
		  AND t.type = 'transfer' AND t.withdrawal_amt > 0
		GROUP BY t.transfer_kind`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for kindRows.Next() {
		var kind string
		var amt float64
		if err := kindRows.Scan(&kind, &amt); err != nil {
			kindRows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		switch kind {
		case "investment":
			summary.CashFlow.ToInvestments += amt
		case "family":
			summary.CashFlow.ToFamily += amt
		default:
			summary.CashFlow.SelfTransfers += amt
		}
	}
	kindRows.Close()

	rows, err := s.DB.Query(`
		SELECT id, name, account_type, bank, current_balance, credit_limit, include_in_networth
		FROM finance_accounts ORDER BY id`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for rows.Next() {
		var a accountSummaryItem
		if err := rows.Scan(&a.ID, &a.Name, &a.AccountType, &a.Bank, &a.CurrentBalance, &a.CreditLimit, &a.IncludeInNetWorth); err != nil {
			rows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		summary.Accounts = append(summary.Accounts, a)

		// Family accounts are tracked in their own total regardless of
		// IncludeInNetWorth — that flag controls the *personal* figure below,
		// but a family account's balance should still be visible somewhere.
		if a.AccountType == "family" {
			summary.FamilyAccountCount++
			if a.CurrentBalance < 0 {
				summary.FamilyLiabilities += -a.CurrentBalance
			} else {
				summary.FamilyAssets += a.CurrentBalance
			}
		}

		if !a.IncludeInNetWorth {
			summary.ExcludedAccounts++
			continue
		}

		// A card or loan carries its debt as a negative balance (spending is a
		// withdrawal). Either side of zero is possible on a card — an overpaid
		// card really is a small asset — so the sign decides, not the type
		// alone.
		switch {
		case liabilityTypes[a.AccountType] && a.CurrentBalance < 0:
			summary.TotalLiabilities += -a.CurrentBalance
		case a.CurrentBalance < 0:
			// An overdrawn bank account is a liability too.
			summary.TotalLiabilities += -a.CurrentBalance
		default:
			summary.TotalAssets += a.CurrentBalance
			if liquidTypes[a.AccountType] {
				summary.LiquidAssets += a.CurrentBalance
			}
		}
	}
	rows.Close()
	summary.FamilyNetWorth = summary.FamilyAssets - summary.FamilyLiabilities

	// Deposits and holdings are assets the account tables know nothing about;
	// leaving them out is why net worth read low for anyone with an FD or PPF.
	//
	// Rupee holdings first, then foreign ones converted at a known rate.
	//
	// The conversion is not optional decoration: a US stock the user owns is
	// part of their net worth, and leaving it out understates it as surely as
	// adding dollars to rupees would overstate it. What the code refuses to do
	// is invent the rate — it uses one the user set, or one read off their own
	// bank's forex remittances, and reports which.
	if err := s.DB.QueryRow(
		`SELECT COALESCE(SUM(current_value), 0) FROM investments
		 WHERE status = 'active' AND currency = 'INR'`,
	).Scan(&summary.InvestmentValue); err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}

	foreign, err := s.DB.Query(`
		SELECT currency, COALESCE(SUM(current_value), 0), COALESCE(SUM(invested_amount), 0), COUNT(*)
		FROM investments WHERE status = 'active' AND currency != 'INR'
		GROUP BY currency ORDER BY SUM(current_value) DESC`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	summary.ForeignHoldings = []foreignHoldingTotal{}
	for foreign.Next() {
		var f foreignHoldingTotal
		if err := foreign.Scan(&f.Currency, &f.Value, &f.Invested, &f.Count); err != nil {
			foreign.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		summary.ForeignHoldings = append(summary.ForeignHoldings, f)
	}
	foreign.Close()

	// Rates are resolved after the cursor closes: the pool is capped at one
	// connection, so deriving a rate mid-iteration would deadlock against the
	// cursor still holding it.
	for i := range summary.ForeignHoldings {
		f := &summary.ForeignHoldings[i]
		f.Rate = ledger.EnsureRate(s.DB, f.Currency)
		if f.Rate > 0 {
			f.ValueINR = f.Value * f.Rate
			summary.InvestmentValue += f.ValueINR
			summary.ForeignConvertedINR += f.ValueINR
			var rate ledger.Rate
			if err := s.DB.QueryRow(
				`SELECT source, note, as_of FROM fx_rates WHERE currency = ?`, f.Currency,
			).Scan(&rate.Source, &rate.Note, &rate.AsOf); err == nil {
				f.RateSource, f.RateNote, f.RateAsOf = rate.Source, rate.Note, rate.AsOf
			}
		} else {
			// No rate anywhere: better to leave it out of the total and say so
			// than to fold in a number that means nothing.
			summary.UnconvertedCurrencies = append(summary.UnconvertedCurrencies, f.Currency)
		}
	}
	summary.TotalAssets += summary.InvestmentValue
	summary.NetWorth = summary.TotalAssets - summary.TotalLiabilities

	trend, err := s.netWorthTrend(summary.NetWorth)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	summary.NetWorthTrend = trend

	catRows, err := s.DB.Query(`
		SELECT COALESCE(c.name, 'Others'), COALESCE(c.color, '#6b7280'), SUM(t.withdrawal_amt) as amt
		FROM transactions t` + countedJoin + `
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.txn_date >= ` + from + ` AND t.txn_date <= ` + to + ` AND t.type = 'expense'
		GROUP BY c.id
		ORDER BY amt DESC`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for catRows.Next() {
		var c categoryBreakdownItem
		if err := catRows.Scan(&c.CategoryName, &c.Color, &c.Amount); err != nil {
			catRows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		summary.ExpenseBreakdown = append(summary.ExpenseBreakdown, c)
	}
	catRows.Close()

	merchRows, err := s.DB.Query(`
		SELECT t.merchant, SUM(t.withdrawal_amt) as amt, COUNT(*)
		FROM transactions t` + countedJoin + `
		WHERE t.txn_date >= ` + from + ` AND t.txn_date <= ` + to + ` AND t.type = 'expense' AND t.merchant != ''
		GROUP BY t.merchant
		ORDER BY amt DESC
		LIMIT 5`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	for merchRows.Next() {
		var m merchantSummaryItem
		if err := merchRows.Scan(&m.Merchant, &m.Amount, &m.Count); err != nil {
			merchRows.Close()
			httpx.WriteError(w, 500, err.Error())
			return
		}
		summary.TopMerchants = append(summary.TopMerchants, m)
	}
	merchRows.Close()

	// Unpaid bills, soonest due first. Deliberately not scoped to the selected
	// period: a bill due next week matters regardless of which month is being
	// viewed. Bills with no parsed due date sort last rather than disappearing.
	summary.UpcomingBills = []upcomingBillItem{}
	billRows, err := s.DB.Query(`
		SELECT b.id, b.issuer, b.card_last4, b.total_due, b.minimum_due, b.due_date, b.status,
		       l.mail_account_id, l.uid, COALESCE(l.subject, ''), COALESCE(l.sender, ''),
		       COALESCE(l.received_at, '')
		FROM bills b
		LEFT JOIN message_links l ON l.bill_id = b.id
		WHERE b.status = 'unpaid'
		ORDER BY CASE WHEN b.due_date = '' THEN 1 ELSE 0 END, b.due_date ASC
		LIMIT 8`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer billRows.Close()
	for billRows.Next() {
		var b upcomingBillItem
		var totalDue, minDue sql.NullFloat64
		var mailAccountID, mailUID sql.NullInt64
		var subject, sender, receivedAt string
		if err := billRows.Scan(&b.ID, &b.Issuer, &b.CardLast4, &totalDue, &minDue,
			&b.DueDate, &b.Status, &mailAccountID, &mailUID, &subject, &sender, &receivedAt); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		if totalDue.Valid {
			b.TotalDue = &totalDue.Float64
		}
		if minDue.Valid {
			b.MinimumDue = &minDue.Float64
		}
		if mailAccountID.Valid {
			b.SourceEmail = &models.SourceEmail{
				MailAccountID: mailAccountID.Int64,
				UID:           uint32(mailUID.Int64),
				Subject:       subject,
				Sender:        sender,
				ReceivedAt:    receivedAt,
			}
		}
		summary.UpcomingBills = append(summary.UpcomingBills, b)
	}

	httpx.WriteJSON(w, 200, summary)
}

// netWorthTrend reconstructs a month-end net worth series for the last 12
// months by walking backwards from today's figure: each earlier month-end is
// the current net worth minus every transaction that happened after it.
//
// This leans on the transaction history being reasonably complete for the
// counted accounts. Investments are carried flat at today's value — holdings
// have no stored price history — so the curve moves on cash facts only.
func (s *Server) netWorthTrend(currentNetWorth float64) ([]netWorthPoint, error) {
	// Net movement per month over counted accounts, most recent 13 months —
	// one extra so the oldest point has a delta to subtract through.
	rows, err := s.DB.Query(`
		SELECT strftime('%Y-%m', t.txn_date) AS month, COALESCE(SUM(t.deposit_amt - t.withdrawal_amt), 0)
		FROM transactions t
		JOIN finance_accounts fa ON fa.id = t.account_id AND fa.include_in_networth = 1
		WHERE t.txn_date >= date('now', 'start of month', '-12 months')
		GROUP BY month`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deltaByMonth := map[string]float64{}
	for rows.Next() {
		var month string
		var delta float64
		if err := rows.Scan(&month, &delta); err != nil {
			return nil, err
		}
		deltaByMonth[month] = delta
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// First-of-month anchor sidesteps AddDate's end-of-month normalization
	// (May 31 minus one month is not April).
	now := time.Now()
	cursor := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	points := make([]netWorthPoint, 12)
	running := currentNetWorth
	for i := 0; i < 12; i++ {
		month := cursor.Format("2006-01")
		points[11-i] = netWorthPoint{Month: month, NetWorth: running}
		// Step back past this month: remove its net movement to get the
		// previous month-end position.
		running -= deltaByMonth[month]
		cursor = cursor.AddDate(0, -1, 0)
	}
	return points, nil
}
