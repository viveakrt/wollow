package api

import (
	"net/http"
)

type categoryBreakdownItem struct {
	CategoryName string  `json:"categoryName"`
	Color        string  `json:"color"`
	Amount       float64 `json:"amount"`
}

type accountSummaryItem struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	AccountType    string  `json:"accountType"`
	Bank           string  `json:"bank"`
	CurrentBalance float64 `json:"currentBalance"`
}

type merchantSummaryItem struct {
	Merchant string  `json:"merchant"`
	Amount   float64 `json:"amount"`
	Count    int     `json:"count"`
}

type dashboardSummary struct {
	NetWorth         float64                 `json:"netWorth"`
	TotalIncome      float64                 `json:"totalIncome"`
	TotalExpenses    float64                 `json:"totalExpenses"`
	TotalSavings     float64                 `json:"totalSavings"`
	TransactionCount int                     `json:"transactionCount"`
	Accounts         []accountSummaryItem    `json:"accounts"`
	ExpenseBreakdown []categoryBreakdownItem `json:"expenseBreakdown"`
	TopMerchants     []merchantSummaryItem   `json:"topMerchants"`
}

// handleDashboardSummary aggregates figures for the dashboard. Accepts
// optional ?from=YYYY-MM-DD&to=YYYY-MM-DD query params to scope the period;
// defaults to the current calendar month.
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
	}

	// Net worth = sum of all account current balances.
	if err := s.DB.QueryRow(`SELECT COALESCE(SUM(current_balance),0) FROM accounts`).Scan(&summary.NetWorth); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	row := s.DB.QueryRow(`
		SELECT COALESCE(SUM(deposit_amt),0), COALESCE(SUM(withdrawal_amt),0), COUNT(*)
		FROM transactions
		WHERE txn_date >= ` + from + ` AND txn_date <= ` + to + ` AND type != 'transfer'`)
	if err := row.Scan(&summary.TotalIncome, &summary.TotalExpenses, &summary.TransactionCount); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	summary.TotalSavings = summary.TotalIncome - summary.TotalExpenses

	rows, err := s.DB.Query(`
		SELECT id, name, account_type, bank, current_balance FROM accounts ORDER BY id`)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for rows.Next() {
		var a accountSummaryItem
		if err := rows.Scan(&a.ID, &a.Name, &a.AccountType, &a.Bank, &a.CurrentBalance); err != nil {
			rows.Close()
			writeError(w, 500, err.Error())
			return
		}
		summary.Accounts = append(summary.Accounts, a)
	}
	rows.Close()

	catRows, err := s.DB.Query(`
		SELECT COALESCE(c.name, 'Others'), COALESCE(c.color, '#6b7280'), SUM(t.withdrawal_amt) as amt
		FROM transactions t
		LEFT JOIN categories c ON c.id = t.category_id
		WHERE t.txn_date >= ` + from + ` AND t.txn_date <= ` + to + ` AND t.type = 'expense'
		GROUP BY c.id
		ORDER BY amt DESC`)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for catRows.Next() {
		var c categoryBreakdownItem
		if err := catRows.Scan(&c.CategoryName, &c.Color, &c.Amount); err != nil {
			catRows.Close()
			writeError(w, 500, err.Error())
			return
		}
		summary.ExpenseBreakdown = append(summary.ExpenseBreakdown, c)
	}
	catRows.Close()

	merchRows, err := s.DB.Query(`
		SELECT merchant, SUM(withdrawal_amt) as amt, COUNT(*)
		FROM transactions
		WHERE txn_date >= ` + from + ` AND txn_date <= ` + to + ` AND type = 'expense' AND merchant != ''
		GROUP BY merchant
		ORDER BY amt DESC
		LIMIT 5`)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	for merchRows.Next() {
		var m merchantSummaryItem
		if err := merchRows.Scan(&m.Merchant, &m.Amount, &m.Count); err != nil {
			merchRows.Close()
			writeError(w, 500, err.Error())
			return
		}
		summary.TopMerchants = append(summary.TopMerchants, m)
	}
	merchRows.Close()

	writeJSON(w, 200, summary)
}
