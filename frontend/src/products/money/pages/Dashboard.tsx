import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import {
  PieChart,
  Pie,
  Cell,
  ResponsiveContainer,
  Tooltip,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
} from 'recharts'
import {
  Wallet,
  TrendingUp,
  TrendingDown,
  PiggyBank,
  LineChart,
  Upload,
  Mail,
  Droplets,
  Users,
  EyeOff,
  ArrowLeftRight,
} from 'lucide-react'
import { api } from '../api'
import { formatINR, formatMoney } from '../lib/format'
import { Card } from '../components/Card'
import { StatTile } from '../components/StatTile'
import { LIABILITY_TYPES } from '../types'
import type { DashboardSummary } from '../types'

/**
 * Fills in fields a backend older than this build doesn't send yet.
 *
 * A running server that hasn't been restarted since the net-worth fields
 * landed returns a summary without them, and the page would otherwise die on
 * `undefined.map` — a blank dashboard for what is really a stale process.
 * Missing numbers read as zero and a missing include flag counts the account,
 * both matching the server's own defaults.
 */
function withDefaults(s: DashboardSummary): DashboardSummary {
  return {
    ...s,
    liquidAssets: s.liquidAssets ?? 0,
    investmentValue: s.investmentValue ?? 0,
    excludedAccounts: s.excludedAccounts ?? 0,
    netWorthTrend: s.netWorthTrend ?? [],
    foreignHoldings: s.foreignHoldings ?? [],
    foreignConvertedInr: s.foreignConvertedInr ?? 0,
    unconvertedCurrencies: s.unconvertedCurrencies ?? [],
    cashFlow: s.cashFlow ?? {
      income: s.totalIncome ?? 0,
      expenses: s.totalExpenses ?? 0,
      selfTransfers: 0,
      toInvestments: 0,
      toFamily: 0,
    },
    accounts: (s.accounts ?? []).map((a) => ({
      ...a,
      includeInNetworth: a.includeInNetworth !== false,
    })),
    expenseBreakdown: s.expenseBreakdown ?? [],
    topMerchants: s.topMerchants ?? [],
    upcomingBills: s.upcomingBills ?? [],
  }
}

export function Dashboard() {
  const {
    data,
    isPending,
    error,
  } = useQuery({
    queryKey: ['money', 'dashboard'],
    queryFn: () => api.dashboard.summary(),
  })

  if (isPending) {
    return <div className="p-8 text-[var(--color-text-muted)]">Loading dashboard…</div>
  }
  if (error) {
    return (
      <div className="p-8 text-[var(--color-negative)]">
        Failed to load: {error instanceof Error ? error.message : 'unknown error'}
      </div>
    )
  }
  if (!data) return null

  const summary = withDefaults(data)
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
          to="/money/import"
          className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] transition-colors"
        >
          <Upload size={16} />
          Import Statement
        </Link>
      </div>

      {!hasAccounts ? (
        <EmptyState />
      ) : (
        <>
          <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-4 mb-6">
            <StatTile
              icon={Wallet}
              label="Net Worth"
              value={formatINR(summary.netWorth, true)}
              iconColor="text-[var(--color-tint-violet)]"
              iconBg="bg-[var(--color-tint-violet-bg)]"
              sublabel={{
                text: `${formatINR(summary.totalAssets, true)} assets − ${formatINR(summary.totalLiabilities, true)} owed`,
              }}
            />
            <StatTile
              icon={Droplets}
              label="Liquid"
              value={formatINR(summary.liquidAssets, true)}
              iconColor="text-[var(--color-tint-cyan)]"
              iconBg="bg-[var(--color-tint-cyan-bg)]"
              sublabel={{ text: 'Bank, wallet & cash' }}
            />
            <StatTile
              icon={TrendingUp}
              label="Income"
              value={formatINR(summary.totalIncome, true)}
              iconColor="text-[var(--color-positive)]"
              iconBg="bg-[var(--color-positive-tint)]"
            />
            <StatTile
              icon={TrendingDown}
              label="Expenses"
              value={formatINR(summary.totalExpenses, true)}
              iconColor="text-[var(--color-tint-orange)]"
              iconBg="bg-[var(--color-tint-orange-bg)]"
            />
            <StatTile
              icon={PiggyBank}
              label="Savings"
              value={formatINR(summary.totalSavings, true)}
              iconColor="text-[var(--color-tint-cyan)]"
              iconBg="bg-[var(--color-tint-cyan-bg)]"
              sublabel={{
                text: summary.totalSavings >= 0 ? 'Positive cash flow' : 'Spending more than earning',
                positive: summary.totalSavings >= 0,
              }}
            />
            <StatTile
              icon={LineChart}
              label="Investments"
              value={formatINR(summary.investmentValue, true)}
              iconColor="text-[var(--color-tint-violet)]"
              iconBg="bg-[var(--color-tint-violet-bg)]"
              // Foreign holdings are counted here, converted at a rate taken
              // from the user's own forex remittances. Saying so matters: part
              // of this figure rests on that rate rather than on rupees.
              sublabel={
                summary.foreignHoldings.length > 0
                  ? {
                      text: summary.foreignConvertedInr
                        ? `includes ${formatINR(summary.foreignConvertedInr, true)} converted from ${summary.foreignHoldings
                            .filter((f) => f.rate > 0)
                            .map((f) => formatMoney(f.value, f.currency))
                            .join(', ')}`
                        : `${summary.foreignHoldings
                            .map((f) => formatMoney(f.value, f.currency))
                            .join(', ')} not counted — no exchange rate`,
                    }
                  : undefined
              }
            />
          </div>

          <div className="grid grid-cols-1 xl:grid-cols-3 gap-5 mb-5">
            <Card title="Net Worth Trend" className="xl:col-span-2">
              <NetWorthTrendChart data={summary.netWorthTrend} />
              {summary.excludedAccounts > 0 && (
                <p className="mt-2 text-xs text-[var(--color-text-muted)] flex items-center gap-1.5">
                  <EyeOff size={11} />
                  {summary.excludedAccounts} account{summary.excludedAccounts !== 1 ? 's' : ''} excluded
                  from these figures.
                </p>
              )}
            </Card>

            <Card title="Cash Flow This Month">
              <CashFlowSummary cashFlow={summary.cashFlow} />
            </Card>
          </div>

          <div className="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-4 gap-5">
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

            <Card title="Upcoming Bills" className="lg:col-span-1">
              {summary.upcomingBills.length === 0 ? (
                <p className="text-sm text-[var(--color-text-muted)] py-8 text-center">
                  No unpaid bills found in your mail.
                </p>
              ) : (
                <div className="space-y-3">
                  {summary.upcomingBills.map((b) => (
                    <div key={b.id} className="flex items-start justify-between gap-3 text-sm">
                      <div className="min-w-0">
                        <div className="truncate font-medium">
                          {b.issuer}
                          {b.cardLast4 && ` •• ${b.cardLast4}`}
                        </div>
                        <div className="text-xs text-[var(--color-text-muted)]">
                          {b.dueDate ? `Due ${b.dueDate}` : 'No due date in the email'}
                        </div>
                        {b.sourceEmail && (
                          <Link
                            to={`/mail/messages/${b.sourceEmail.mailAccountId}/${b.sourceEmail.uid}`}
                            className="mt-0.5 inline-flex items-center gap-1 text-xs text-[var(--color-accent-2)] hover:underline"
                          >
                            <Mail size={11} />
                            Source email
                          </Link>
                        )}
                      </div>
                      <div className="shrink-0 text-right">
                        {b.totalDue != null ? (
                          <div className="font-semibold">{formatINR(b.totalDue)}</div>
                        ) : (
                          <div className="text-xs text-[var(--color-text-muted)]">In statement PDF</div>
                        )}
                        {b.minimumDue != null && (
                          <div className="text-xs text-[var(--color-text-muted)]">
                            Min {formatINR(b.minimumDue)}
                          </div>
                        )}
                      </div>
                    </div>
                  ))}
                  <Link
                    to="/money/bills"
                    className="block text-center pt-1 text-sm text-[var(--color-accent-2)] hover:underline"
                  >
                    View all bills →
                  </Link>
                </div>
              )}
            </Card>

            <Card title="Accounts Overview" className="lg:col-span-1">
              <AccountsOverview accounts={summary.accounts} investmentValue={summary.investmentValue} />
              <Link
                to="/money/accounts"
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

/** "2026-08" → "Aug 26" for axis ticks. */
function formatMonth(month: string): string {
  const [y, m] = month.split('-').map(Number)
  const names = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
  return `${names[(m ?? 1) - 1]} ${String(y).slice(-2)}`
}

function NetWorthTrendChart({ data }: { data: DashboardSummary['netWorthTrend'] }) {
  if (data.length === 0) {
    return (
      <p className="py-16 text-center text-sm text-[var(--color-text-muted)]">
        No trend yet — it builds from your transaction history.
      </p>
    )
  }

  const points = data.map((d) => ({ ...d, label: formatMonth(d.month) }))
  const latest = points[points.length - 1]
  const first = points[0]
  const change = latest && first ? latest.netWorth - first.netWorth : 0
  return (
    <div>
      <div className="flex items-baseline gap-3 mb-3">
        <span className="text-2xl font-semibold tracking-tight">
          {latest ? formatINR(latest.netWorth, true) : '—'}
        </span>
        {latest && first && (
          <span
            className={`text-xs font-medium ${change >= 0 ? 'text-[var(--color-positive)]' : 'text-[var(--color-negative)]'}`}
          >
            {change >= 0 ? '+' : '−'}
            {formatINR(Math.abs(change), true)} over 12 months
          </span>
        )}
      </div>
      <div className="h-56">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={points} margin={{ top: 4, right: 4, bottom: 0, left: 4 }}>
            <defs>
              <linearGradient id="networthFill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--color-accent)" stopOpacity={0.25} />
                <stop offset="100%" stopColor="var(--color-accent)" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid stroke="var(--color-border)" strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="label"
              tick={{ fontSize: 11, fill: 'var(--color-text-muted)' }}
              axisLine={false}
              tickLine={false}
              interval="preserveStartEnd"
              minTickGap={24}
            />
            <YAxis
              tickFormatter={(v: number) => formatINR(v, true)}
              tick={{ fontSize: 11, fill: 'var(--color-text-muted)' }}
              axisLine={false}
              tickLine={false}
              width={64}
            />
            <Tooltip
              formatter={(v) => [formatINR(Number(v)), 'Net worth']}
              contentStyle={{
                background: 'var(--color-surface-2)',
                border: '1px solid var(--color-border)',
                borderRadius: 8,
                fontSize: 13,
              }}
            />
            <Area
              type="monotone"
              dataKey="netWorth"
              stroke="var(--color-accent)"
              strokeWidth={2}
              fill="url(#networthFill)"
              dot={false}
              activeDot={{ r: 4 }}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <p className="mt-2 text-xs text-[var(--color-text-muted)]">
        Rebuilt from your transaction history; investments carried at current value.
      </p>
    </div>
  )
}

function CashFlowSummary({ cashFlow }: { cashFlow: DashboardSummary['cashFlow'] }) {
  const net = cashFlow.income - cashFlow.expenses - cashFlow.toFamily - cashFlow.toInvestments
  const rows = [
    {
      label: 'Income',
      icon: TrendingUp,
      amount: cashFlow.income,
      className: 'text-[var(--color-positive)]',
      sign: '+',
    },
    {
      label: 'Expenses',
      icon: TrendingDown,
      amount: cashFlow.expenses,
      className: 'text-[var(--color-negative)]',
      sign: '−',
    },
    {
      label: 'To investments',
      icon: LineChart,
      amount: cashFlow.toInvestments,
      className: '',
      sign: '−',
    },
    {
      label: 'To family',
      icon: Users,
      amount: cashFlow.toFamily,
      className: '',
      sign: '−',
    },
    {
      label: 'Between own accounts',
      icon: ArrowLeftRight,
      amount: cashFlow.selfTransfers,
      className: 'text-[var(--color-text-muted)]',
      sign: '',
    },
  ]
  return (
    <div className="space-y-3">
      {rows.map((r) => (
        <div key={r.label} className="flex items-center justify-between text-sm">
          <div className="flex items-center gap-2 text-[var(--color-text-muted)]">
            <r.icon size={14} />
            <span>{r.label}</span>
          </div>
          <span className={`font-semibold ${r.className}`}>
            {r.sign}
            {formatINR(r.amount)}
          </span>
        </div>
      ))}
      <div className="border-t border-[var(--color-border)] pt-3 flex items-center justify-between text-sm">
        <span className="font-medium">Kept this month</span>
        <span
          className={`font-semibold ${net >= 0 ? 'text-[var(--color-positive)]' : 'text-[var(--color-negative)]'}`}
        >
          {formatINR(net)}
        </span>
      </div>
      <p className="text-xs text-[var(--color-text-muted)]">
        Transfers between your own accounts move money without changing what you keep. Money sent
        to investments still counts toward your net worth.
      </p>
    </div>
  )
}

/**
 * Accounts grouped the way the money reads: what's spendable, what's owed,
 * what's invested — mirroring the reference design's grouped overview.
 */
function AccountsOverview({
  accounts,
  investmentValue,
}: {
  accounts: DashboardSummary['accounts']
  investmentValue: number
}) {
  const groups: { title: string; filter: (a: DashboardSummary['accounts'][number]) => boolean }[] = [
    { title: 'Cash & Bank', filter: (a) => ['bank', 'wallet', 'cash'].includes(a.accountType) },
    { title: 'Credit Cards & Loans', filter: (a) => LIABILITY_TYPES.has(a.accountType) },
    {
      title: 'Deposits & Other',
      filter: (a) =>
        !['bank', 'wallet', 'cash'].includes(a.accountType) && !LIABILITY_TYPES.has(a.accountType),
    },
  ]
  return (
    <div className="space-y-4">
      {groups.map((g) => {
        const members = accounts.filter(g.filter)
        if (members.length === 0) return null
        const subtotal = members
          .filter((a) => a.includeInNetworth)
          .reduce((s, a) => s + a.currentBalance, 0)
        return (
          <div key={g.title}>
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
                {g.title}
              </span>
              <span
                className={`text-xs font-semibold ${subtotal < 0 ? 'text-[var(--color-negative)]' : ''}`}
              >
                {formatINR(subtotal, true)}
              </span>
            </div>
            <div className="space-y-2">
              {members.map((a) => (
                <div key={a.id} className="flex items-center justify-between text-sm">
                  <div className="min-w-0 flex items-center gap-1.5">
                    <span className="truncate">{a.name}</span>
                    {!a.includeInNetworth && (
                      <span title="Not counted in net worth">
                        <EyeOff size={11} className="shrink-0 text-[var(--color-text-muted)]" />
                      </span>
                    )}
                  </div>
                  <span
                    className={`shrink-0 ml-3 font-medium ${
                      a.currentBalance < 0
                        ? 'text-[var(--color-negative)]'
                        : !a.includeInNetworth
                          ? 'text-[var(--color-text-muted)]'
                          : ''
                    }`}
                  >
                    {formatINR(a.currentBalance)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )
      })}
      {investmentValue > 0 && (
        <div>
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
              Investments
            </span>
            <span className="text-xs font-semibold">{formatINR(investmentValue, true)}</span>
          </div>
          <Link
            to="/money/investments"
            className="text-sm text-[var(--color-accent-2)] hover:underline"
          >
            View holdings →
          </Link>
        </div>
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
              // A gap between slices only makes sense when there is more than
              // one; recharts renders a single slice as a sliver otherwise.
              paddingAngle={data.length > 1 ? 2 : 0}
              stroke="none"
            >
              {data.map((d) => (
                <Cell key={d.categoryName} fill={d.color} />
              ))}
            </Pie>
            <Tooltip
              formatter={(v) => formatINR(Number(v))}
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
        to="/money/import"
        className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] transition-colors"
      >
        <Upload size={16} />
        Import Statement
      </Link>
    </div>
  )
}
