import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  PieChart,
  Pie,
  Cell,
  ResponsiveContainer,
  Tooltip,
} from 'recharts'
import { Wallet, TrendingUp, TrendingDown, PiggyBank, Landmark, Upload } from 'lucide-react'
import { api } from '../lib/api'
import { formatINR } from '../lib/format'
import { Card } from '../components/Card'
import { StatTile } from '../components/StatTile'
import type { DashboardSummary } from '../lib/types'

export function Dashboard() {
  const [summary, setSummary] = useState<DashboardSummary | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.dashboard
      .summary()
      .then(setSummary)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) {
    return <div className="p-8 text-[var(--color-text-muted)]">Loading dashboard…</div>
  }
  if (error) {
    return <div className="p-8 text-[var(--color-negative)]">Failed to load: {error}</div>
  }
  if (!summary) return null

  const hasAccounts = summary.accounts.length > 0

  return (
    <div className="p-8 max-w-[1500px] mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
          <p className="text-[var(--color-text-muted)] text-sm mt-1">
            Here's your financial overview for this month.
          </p>
        </div>
        <Link
          to="/import"
          className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 transition-colors"
        >
          <Upload size={16} />
          Import Statement
        </Link>
      </div>

      {!hasAccounts ? (
        <EmptyState />
      ) : (
        <>
          <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-5 gap-4 mb-6">
            <StatTile
              icon={Wallet}
              label="Net Worth"
              value={formatINR(summary.netWorth, true)}
              iconColor="text-violet-400"
              iconBg="bg-violet-500/15"
            />
            <StatTile
              icon={TrendingUp}
              label="Income (this month)"
              value={formatINR(summary.totalIncome, true)}
              iconColor="text-emerald-400"
              iconBg="bg-emerald-500/15"
            />
            <StatTile
              icon={TrendingDown}
              label="Expenses (this month)"
              value={formatINR(summary.totalExpenses, true)}
              iconColor="text-orange-400"
              iconBg="bg-orange-500/15"
            />
            <StatTile
              icon={PiggyBank}
              label="Savings (this month)"
              value={formatINR(summary.totalSavings, true)}
              iconColor="text-cyan-400"
              iconBg="bg-cyan-500/15"
              sublabel={{
                text: summary.totalSavings >= 0 ? 'Positive cash flow' : 'Spending more than earning',
                positive: summary.totalSavings >= 0,
              }}
            />
            <StatTile
              icon={Landmark}
              label="Transactions"
              value={String(summary.transactionCount)}
              iconColor="text-pink-400"
              iconBg="bg-pink-500/15"
            />
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
            <Card title="Expense Breakdown" className="lg:col-span-1">
              {summary.expenseBreakdown.length === 0 ? (
                <p className="text-sm text-[var(--color-text-muted)] py-8 text-center">
                  No expenses recorded this month yet.
                </p>
              ) : (
                <ExpenseDonut data={summary.expenseBreakdown} />
              )}
            </Card>

            <Card title="Top Spending Merchants" className="lg:col-span-1">
              {summary.topMerchants.length === 0 ? (
                <p className="text-sm text-[var(--color-text-muted)] py-8 text-center">
                  No merchant spending yet.
                </p>
              ) : (
                <div className="space-y-3">
                  {summary.topMerchants.map((m) => (
                    <div key={m.merchant} className="flex items-center justify-between text-sm">
                      <div className="min-w-0">
                        <div className="truncate font-medium">{m.merchant}</div>
                        <div className="text-xs text-[var(--color-text-muted)]">{m.count} transactions</div>
                      </div>
                      <div className="font-semibold shrink-0 ml-3">{formatINR(m.amount)}</div>
                    </div>
                  ))}
                </div>
              )}
            </Card>

            <Card title="Accounts Overview" className="lg:col-span-1">
              <div className="space-y-3">
                {summary.accounts.map((a) => (
                  <div key={a.id} className="flex items-center justify-between text-sm">
                    <div className="min-w-0">
                      <div className="truncate font-medium">{a.name}</div>
                      <div className="text-xs text-[var(--color-text-muted)] capitalize">
                        {a.bank} · {a.accountType}
                      </div>
                    </div>
                    <div
                      className={`font-semibold shrink-0 ml-3 ${a.currentBalance < 0 ? 'text-[var(--color-negative)]' : ''}`}
                    >
                      {formatINR(a.currentBalance)}
                    </div>
                  </div>
                ))}
              </div>
              <Link
                to="/accounts"
                className="block text-center mt-4 text-sm text-[var(--color-accent-2)] hover:underline"
              >
                View all accounts →
              </Link>
            </Card>
          </div>
        </>
      )}
    </div>
  )
}

function ExpenseDonut({ data }: { data: DashboardSummary['expenseBreakdown'] }) {
  const total = data.reduce((s, d) => s + d.amount, 0)
  return (
    <div>
      <div className="relative h-56">
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie
              data={data}
              dataKey="amount"
              nameKey="categoryName"
              innerRadius={62}
              outerRadius={90}
              paddingAngle={2}
              stroke="none"
            >
              {data.map((d) => (
                <Cell key={d.categoryName} fill={d.color} />
              ))}
            </Pie>
            <Tooltip
              formatter={(v: number) => formatINR(v)}
              contentStyle={{
                background: 'var(--color-surface-2)',
                border: '1px solid var(--color-border)',
                borderRadius: 8,
                fontSize: 13,
              }}
            />
          </PieChart>
        </ResponsiveContainer>
        <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
          <div className="text-lg font-semibold">{formatINR(total, true)}</div>
          <div className="text-xs text-[var(--color-text-muted)]">Total</div>
        </div>
      </div>
      <div className="space-y-2 mt-4 max-h-40 overflow-y-auto pr-1">
        {data.map((d) => (
          <div key={d.categoryName} className="flex items-center justify-between text-xs">
            <div className="flex items-center gap-2 min-w-0">
              <span className="w-2 h-2 rounded-full shrink-0" style={{ background: d.color }} />
              <span className="truncate text-[var(--color-text-muted)]">{d.categoryName}</span>
            </div>
            <span className="shrink-0 ml-2 font-medium">{formatINR(d.amount)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center py-24 border border-dashed border-[var(--color-border)] rounded-xl">
      <Wallet size={40} className="text-[var(--color-text-muted)] mb-4" />
      <h2 className="text-lg font-semibold mb-1">No accounts yet</h2>
      <p className="text-sm text-[var(--color-text-muted)] mb-5 max-w-sm text-center">
        Import a bank statement to get started — your accounts, transactions and spending
        breakdown will show up here automatically.
      </p>
      <Link
        to="/import"
        className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 transition-colors"
      >
        <Upload size={16} />
        Import Statement
      </Link>
    </div>
  )
}
