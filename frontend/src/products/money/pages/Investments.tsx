import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CalendarClock,
  Coins,
  Plus,
  TrendingUp,
  TrendingDown,
  Trash2,
  Upload,
  Wallet,
  Tag,
  Pencil,
  Check,
  X,
} from 'lucide-react'
import { api } from '../api'
import { formatINR, formatMoney, formatDate } from '../lib/format'
import { Card } from '../components/Card'
import { StatTile } from '../components/StatTile'
import { INVESTMENT_KIND_LABELS, INVESTMENT_TABS } from '../types'
import type { Investment, InvestmentTrade, TradeInput } from '../types'

/** Kinds whose holdings are priced per unit rather than by maturity. */
const SECURITY_KINDS = new Set(['us_stock', 'stock', 'mutual_fund'])

export function Investments() {
  const queryClient = useQueryClient()
  const [showClosed, setShowClosed] = useState(false)
  const [adding, setAdding] = useState(false)
  const [tab, setTab] = useState('all')
  const [editing, setEditing] = useState<Investment | null>(null)

  const status = showClosed ? 'all' : 'active'

  const listQuery = useQuery({
    queryKey: ['money', 'investments', status],
    queryFn: () => api.investments.list({ status }),
  })
  const summaryQuery = useQuery({
    queryKey: ['money', 'investments', 'summary'],
    queryFn: () => api.investments.summary(),
  })

  function refresh() {
    queryClient.invalidateQueries({ queryKey: ['money', 'investments'] })
    // Holdings count toward net worth, so the dashboard is stale too.
    queryClient.invalidateQueries({ queryKey: ['money', 'dashboard'] })
  }

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.investments.delete(id),
    onSuccess: refresh,
  })

  const allInvestments = listQuery.data ?? []
  const summary = summaryQuery.data

  const activeTab = INVESTMENT_TABS.find((t) => t.id === tab) ?? INVESTMENT_TABS[0]
  const investments =
    activeTab.kinds.length === 0
      ? allInvestments
      : allInvestments.filter((inv) => activeTab.kinds.includes(inv.kind))

  // Tabs only earn their place when there is something behind them.
  const visibleTabs = INVESTMENT_TABS.filter(
    (t) => t.kinds.length === 0 || allInvestments.some((inv) => t.kinds.includes(inv.kind)),
  )

  const showsSecurities = investments.some((inv) => SECURITY_KINDS.has(inv.kind))

  return (
    <div className="p-8 max-w-[1500px] mx-auto">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Investments</h1>
          <p className="mt-1 text-sm text-[var(--color-text-muted)]">
            Deposits and holdings — imported from summary files, or entered by hand.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to="/money/import"
            className="flex items-center gap-2 rounded-lg border border-[var(--color-border-strong)] px-3 py-2 text-sm font-medium transition-colors hover:bg-[var(--color-hover)]"
          >
            <Upload size={15} />
            Import FD summary
          </Link>
          <button
            type="button"
            onClick={() => setAdding((v) => !v)}
            className="flex items-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)]"
          >
            <Plus size={16} />
            Add holding
          </button>
        </div>
      </div>

      {summary && summary.count > 0 && (
        <div className="mb-6 grid grid-cols-2 gap-4 md:grid-cols-4">
          <StatTile
            icon={Wallet}
            label="Portfolio value"
            value={formatINR(summary.totalValue, true)}
            iconColor="text-[var(--color-tint-violet)]"
            iconBg="bg-[var(--color-tint-violet-bg)]"
          />
          <StatTile
            icon={Coins}
            label="Invested"
            value={formatINR(summary.totalInvested, true)}
            iconColor="text-[var(--color-tint-cyan)]"
            iconBg="bg-[var(--color-tint-cyan-bg)]"
          />
          <StatTile
            icon={TrendingUp}
            label="Unrealised gain"
            value={formatINR(summary.gain, true)}
            iconColor="text-[var(--color-positive)]"
            iconBg="bg-[var(--color-positive-tint)]"
            sublabel={{
              text: summary.gain >= 0 ? 'Above cost' : 'Below cost',
              positive: summary.gain >= 0,
            }}
          />
          <StatTile
            icon={CalendarClock}
            label="Maturing in 90 days"
            value={String(summary.maturingSoon.length)}
            iconColor="text-[var(--color-tint-orange)]"
            iconBg="bg-[var(--color-tint-orange-bg)]"
          />
        </div>
      )}

      {/* Foreign holdings are shown in their own currency rather than added to
          the rupee total — converting them would need an exchange rate this
          app has no source for, and adding the raw numbers is simply wrong. */}
      {summary && summary.byCurrency.filter((c) => c.currency !== 'INR').length > 0 && (
        <div className="mb-6 flex flex-wrap gap-3">
          {summary.byCurrency
            .filter((c) => c.currency !== 'INR')
            .map((c) => (
              <Card key={c.currency} className="flex-1 min-w-[240px]">
                <div className="mb-1 flex items-center gap-2 text-sm text-[var(--color-text-muted)]">
                  <Coins size={14} />
                  {c.currency} holdings ({c.count})
                </div>
                <div className="text-2xl font-semibold tracking-tight">
                  {formatMoney(c.value, c.currency)}
                </div>
                <div className="mt-1 text-xs text-[var(--color-text-muted)]">
                  {formatMoney(c.invested, c.currency)} invested
                  {c.gain !== 0 && (
                    <span className={c.gain >= 0 ? 'text-[var(--color-positive)]' : 'text-[var(--color-negative)]'}>
                      {' · '}
                      {c.gain >= 0 ? '+' : ''}
                      {formatMoney(c.gain, c.currency)}
                    </span>
                  )}
                </div>
                <FXNote currency={c.currency} value={c.value} />
              </Card>
            ))}
        </div>
      )}

      {visibleTabs.length > 1 && (
        <div className="mb-4 flex flex-wrap gap-1 border-b border-[var(--color-border)]">
          {visibleTabs.map((t) => {
            const count =
              t.kinds.length === 0
                ? allInvestments.length
                : allInvestments.filter((inv) => t.kinds.includes(inv.kind)).length
            return (
              <button
                key={t.id}
                type="button"
                onClick={() => setTab(t.id)}
                className={`-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors ${
                  t.id === tab
                    ? 'border-[var(--color-accent)] text-[var(--color-text)]'
                    : 'border-transparent text-[var(--color-text-muted)] hover:text-[var(--color-text)]'
                }`}
              >
                {t.label}
                <span className="ml-1.5 text-xs text-[var(--color-text-subtle)]">{count}</span>
              </button>
            )
          })}
        </div>
      )}

      {adding && (
        <div className="mb-6">
          <AddHoldingForm
            onDone={() => {
              setAdding(false)
              refresh()
            }}
            onCancel={() => setAdding(false)}
          />
        </div>
      )}

      {listQuery.isPending ? (
        <div className="text-[var(--color-text-muted)]">Loading holdings…</div>
      ) : investments.length === 0 ? (
        <EmptyState />
      ) : (
        <Card
          title="Holdings"
          action={
            <label className="flex items-center gap-2 text-xs text-[var(--color-text-muted)]">
              <input
                type="checkbox"
                checked={showClosed}
                onChange={(e) => setShowClosed(e.target.checked)}
              />
              Show matured &amp; closed
            </label>
          }
        >
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-sm">
              <thead>
                <tr className="border-b border-[var(--color-border)] text-left text-xs uppercase tracking-wide text-[var(--color-text-subtle)]">
                  <th className="pb-2 pr-3 font-medium">Holding</th>
                  <th className="pb-2 pr-3 font-medium">Type</th>
                  {showsSecurities && <th className="pb-2 pr-3 text-right font-medium">Units</th>}
                  {showsSecurities && <th className="pb-2 pr-3 text-right font-medium">Avg cost</th>}
                  {showsSecurities && <th className="pb-2 pr-3 text-right font-medium">Price</th>}
                  <th className="pb-2 pr-3 text-right font-medium">Invested</th>
                  <th className="pb-2 pr-3 text-right font-medium">Value</th>
                  <th className="pb-2 pr-3 text-right font-medium">Gain</th>
                  {!showsSecurities && <th className="pb-2 pr-3 text-right font-medium">Rate</th>}
                  {!showsSecurities && <th className="pb-2 pr-3 font-medium">Matures</th>}
                  <th className="pb-2 w-8" />
                </tr>
              </thead>
              <tbody>
                {investments.map((inv) => (
                  <tr key={inv.id} className="border-b border-[var(--color-border)] last:border-0">
                    <td className="py-2.5 pr-3">
                      <div className="font-medium">{inv.name || '(unnamed)'}</div>
                      <div className="text-xs text-[var(--color-text-muted)]">
                        {[inv.institution, inv.identifier].filter(Boolean).join(' · ')}
                      </div>
                    </td>
                    <td className="py-2.5 pr-3 text-[var(--color-text-muted)]">
                      {INVESTMENT_KIND_LABELS[inv.kind] ?? inv.kind}
                    </td>
                    {showsSecurities && (
                      <td className="py-2.5 pr-3 text-right text-[var(--color-text-muted)]">
                        {inv.units != null ? Number(inv.units.toFixed(4)) : '—'}
                      </td>
                    )}
                    {showsSecurities && (
                      <td className="py-2.5 pr-3 text-right text-[var(--color-text-muted)]">
                        {inv.units && inv.units > 0
                          ? formatMoney(inv.investedAmount / inv.units, inv.currency)
                          : '—'}
                      </td>
                    )}
                    {showsSecurities && (
                      <td className="py-2.5 pr-3 text-right">
                        <button
                          type="button"
                          onClick={() => setEditing(inv)}
                          title={
                            inv.priced
                              ? `Priced ${inv.lastPriceAt || 'recently'} — click to update`
                              : 'No price yet — click to set one'
                          }
                          className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 transition-colors hover:bg-[var(--color-hover)] ${
                            inv.priced ? '' : 'text-[var(--color-text-subtle)]'
                          }`}
                        >
                          <Tag size={11} />
                          {inv.lastPrice != null
                            ? formatMoney(inv.lastPrice, inv.currency)
                            : 'set'}
                        </button>
                      </td>
                    )}
                    <td className="py-2.5 pr-3 text-right">
                      {formatMoney(inv.investedAmount, inv.currency)}
                    </td>
                    <td className="py-2.5 pr-3 text-right font-semibold">
                      {formatMoney(inv.currentValue, inv.currency)}
                      {inv.maturityAmount != null && (
                        <div className="text-xs font-normal text-[var(--color-text-muted)]">
                          {formatMoney(inv.maturityAmount, inv.currency)} at maturity
                        </div>
                      )}
                    </td>
                    <td className="py-2.5 pr-3 text-right">
                      {/* An unpriced holding is carried at cost. Showing "+0.00"
                          there would read as a measured flat return rather than
                          as the absence of a price. */}
                      {!inv.priced && SECURITY_KINDS.has(inv.kind) ? (
                        <span className="text-xs text-[var(--color-text-subtle)]">at cost</span>
                      ) : (
                        <span
                          className={`inline-flex items-center gap-1 font-medium ${
                            inv.gain >= 0
                              ? 'text-[var(--color-positive)]'
                              : 'text-[var(--color-negative)]'
                          }`}
                        >
                          {inv.gain >= 0 ? <TrendingUp size={12} /> : <TrendingDown size={12} />}
                          {formatMoney(Math.abs(inv.gain), inv.currency)}
                          <span className="text-xs opacity-80">
                            {inv.gainPercent >= 0 ? '+' : '−'}
                            {Math.abs(inv.gainPercent).toFixed(1)}%
                          </span>
                        </span>
                      )}
                    </td>
                    {!showsSecurities && (
                      <td className="py-2.5 pr-3 text-right text-[var(--color-text-muted)]">
                        {inv.interestRate != null ? `${inv.interestRate}%` : '—'}
                      </td>
                    )}
                    {!showsSecurities && (
                      <td className="py-2.5 pr-3 text-[var(--color-text-muted)]">
                        {inv.maturityDate || '—'}
                      </td>
                    )}
                    <td className="py-2.5">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          type="button"
                          title="Edit holding"
                          onClick={() => setEditing(inv)}
                          className="p-1 text-[var(--color-text-subtle)] transition-colors hover:text-[var(--color-text)]"
                        >
                          <Pencil size={14} />
                        </button>
                        <button
                          type="button"
                          title="Delete holding"
                          onClick={() => {
                            if (confirm(`Delete "${inv.name}"? This cannot be undone.`)) {
                              deleteMutation.mutate(inv.id)
                            }
                          }}
                          className="p-1 text-[var(--color-text-subtle)] transition-colors hover:text-[var(--color-negative)]"
                        >
                          <Trash2 size={14} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}

      {editing && (
        <EditHoldingModal
          holding={editing}
          onClose={() => setEditing(null)}
          onSaved={(updated) => {
            // Keep the modal open on save (trade edits are a sequence of
            // small actions, not a single form submit) but stay in sync with
            // what the server actually derived.
            setEditing(updated)
            refresh()
          }}
        />
      )}
    </div>
  )
}

/**
 * The rate a foreign holding is counted at, and where it came from.
 *
 * A number that moves net worth should never be anonymous: this says whether
 * the rate was typed in or read off the user's own bank remittance, and lets
 * them change it.
 */
function FXNote({ currency, value }: { currency: string; value: number }) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')

  const rates = useQuery({ queryKey: ['money', 'fx-rates'], queryFn: api.fxRates.list })
  const rate = (rates.data ?? []).find((r) => r.currency === currency)

  const save = useMutation({
    mutationFn: () => api.fxRates.set(currency, Number(draft)),
    onSuccess: () => {
      setEditing(false)
      queryClient.invalidateQueries({ queryKey: ['money'] })
    },
  })

  if (editing) {
    return (
      <div className="mt-2 flex items-center gap-2">
        <input
          type="number"
          step="0.01"
          autoFocus
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder={`INR per ${currency}`}
          className="w-28 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1 text-xs focus:border-[var(--color-accent)] focus:outline-none"
        />
        <button
          onClick={() => save.mutate()}
          disabled={!(Number(draft) > 0) || save.isPending}
          className="rounded bg-[var(--color-accent)] px-2 py-1 text-xs font-medium text-white disabled:opacity-50"
        >
          Save
        </button>
        <button
          onClick={() => setEditing(false)}
          className="text-xs text-[var(--color-text-muted)] hover:underline"
        >
          Cancel
        </button>
      </div>
    )
  }

  return (
    <button
      type="button"
      onClick={() => {
        setDraft(rate ? String(rate.inrPerUnit) : '')
        setEditing(true)
      }}
      className="mt-1 block text-left text-xs text-[var(--color-text-subtle)] hover:text-[var(--color-text-muted)]"
      title={rate?.note || undefined}
    >
      {rate && rate.inrPerUnit > 0 ? (
        <>
          Counted as {formatINR(value * rate.inrPerUnit)} at ₹{rate.inrPerUnit.toFixed(2)}/{currency}
          {rate.source === 'derived' ? ' — from your own forex transaction' : ' — set by you'}
        </>
      ) : (
        <>Not counted in net worth — no exchange rate yet. Click to set one.</>
      )}
    </button>
  )
}

/**
 * Edit everything about one holding: its descriptive fields always, its
 * price and orders when it's trade-backed, or its raw figures directly when
 * it isn't.
 *
 * The split matters and isn't cosmetic. A trade-backed holding's units,
 * invested amount and current value are DERIVED (see ledger.RecomputeHolding
 * server-side) — the server ignores those three fields on a PUT for such a
 * holding, so offering them as free-standing inputs here would show an edit
 * "succeeding" and then silently reverting the next time any trade or price
 * update recomputes the position. The correct lever for those holdings is the
 * trade list below: add the purchase the mail parser missed, correct a
 * demat-statement's approximated opening cost, or remove a trade entirely.
 */
function EditHoldingModal({
  holding,
  onClose,
  onSaved,
}: {
  holding: Investment
  onClose: () => void
  onSaved: (updated: Investment) => void
}) {
  const queryClient = useQueryClient()
  const isSecurity = SECURITY_KINDS.has(holding.kind)

  const [meta, setMeta] = useState({
    name: holding.name,
    institution: holding.institution,
    kind: holding.kind,
    notes: holding.notes,
    interestRate: holding.interestRate != null ? String(holding.interestRate) : '',
    maturityDate: holding.maturityDate,
    investedAmount: String(holding.investedAmount),
    currentValue: String(holding.currentValue),
    units: holding.units != null ? String(holding.units) : '',
  })
  const [price, setPrice] = useState(holding.lastPrice != null ? String(holding.lastPrice) : '')
  const [error, setError] = useState<string | null>(null)

  const trades = useQuery({
    queryKey: ['money', 'investments', holding.id, 'trades'],
    queryFn: () => api.investments.trades(holding.id),
    enabled: holding.hasTrades,
  })

  function afterMutate(updated: Investment) {
    queryClient.invalidateQueries({ queryKey: ['money', 'investments', holding.id, 'trades'] })
    setError(null)
    onSaved(updated)
  }

  const saveMeta = useMutation({
    mutationFn: () =>
      api.investments.update(holding.id, {
        name: meta.name.trim(),
        institution: meta.institution.trim(),
        kind: meta.kind,
        notes: meta.notes,
        interestRate: meta.interestRate ? Number(meta.interestRate) : undefined,
        maturityDate: meta.maturityDate,
        // Only take effect server-side when the holding has no trades; sent
        // regardless so the manual-holding path (below) has somewhere to go.
        investedAmount: Number(meta.investedAmount) || 0,
        currentValue: Number(meta.currentValue) || 0,
        units: meta.units ? Number(meta.units) : undefined,
      }),
    onSuccess: afterMutate,
    onError: (e) => setError(e instanceof Error ? e.message : 'Could not save'),
  })

  const savePrice = useMutation({
    mutationFn: () => api.investments.setPrice(holding.id, Number(price)),
    onSuccess: afterMutate,
    onError: (e) => setError(e instanceof Error ? e.message : 'Could not save the price'),
  })

  const deleteTrade = useMutation({
    mutationFn: (tradeId: number) => api.investments.deleteTrade(holding.id, tradeId),
    onSuccess: afterMutate,
  })
  const updateTrade = useMutation({
    mutationFn: ({ tradeId, data }: { tradeId: number; data: TradeInput }) =>
      api.investments.updateTrade(holding.id, tradeId, data),
    onSuccess: afterMutate,
  })
  const addTrade = useMutation({
    mutationFn: (data: TradeInput) => api.investments.addTrade(holding.id, data),
    onSuccess: afterMutate,
  })

  const units = holding.units ?? 0
  const pricePreview = Number(price) > 0 && units > 0 ? Number(price) * units : null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div
        className="max-h-[90vh] w-full max-w-xl overflow-y-auto rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-3">
          <h2 className="text-lg font-semibold">Edit holding</h2>
          <button onClick={onClose} className="text-[var(--color-text-subtle)] hover:text-[var(--color-text)]">
            <X size={18} />
          </button>
        </div>

        {error && <p className="mt-3 text-sm text-[var(--color-negative)]">{error}</p>}

        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <Labelled label="Name">
            <input
              value={meta.name}
              onChange={(e) => setMeta((m) => ({ ...m, name: e.target.value }))}
              className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
            />
          </Labelled>
          <Labelled label="Institution">
            <input
              value={meta.institution}
              onChange={(e) => setMeta((m) => ({ ...m, institution: e.target.value }))}
              className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
            />
          </Labelled>
          <Labelled label="Type">
            <select
              value={meta.kind}
              onChange={(e) => setMeta((m) => ({ ...m, kind: e.target.value }))}
              className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
            >
              {KIND_OPTIONS.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </Labelled>
          {!isSecurity && (
            <Labelled label="Interest rate %">
              <input
                type="number"
                inputMode="decimal"
                value={meta.interestRate}
                onChange={(e) => setMeta((m) => ({ ...m, interestRate: e.target.value }))}
                className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
              />
            </Labelled>
          )}
          {!isSecurity && (
            <Labelled label="Maturity date">
              <input
                type="date"
                value={meta.maturityDate}
                onChange={(e) => setMeta((m) => ({ ...m, maturityDate: e.target.value }))}
                className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
              />
            </Labelled>
          )}
        </div>

        {holding.hasTrades ? (
          <>
            {/* Units/invested/value are derived — see the component doc
                comment — so they're shown, not edited, here. */}
            <div className="mt-4 flex items-center gap-4 rounded-lg bg-[var(--color-surface-2)] px-3 py-2 text-xs text-[var(--color-text-muted)]">
              <span>
                {units ? Number(units.toFixed(4)) : 0} units · {formatMoney(holding.investedAmount, holding.currency)}{' '}
                invested
              </span>
              <span className="text-[var(--color-text-subtle)]">
                — derived from the trades below, not editable directly
              </span>
            </div>

            <div className="mt-4">
              <label className="mb-1 block text-xs font-medium text-[var(--color-text-muted)]">
                Price per unit ({holding.currency})
              </label>
              <div className="flex gap-2">
                <input
                  type="number"
                  step="0.0001"
                  value={price}
                  onChange={(e) => setPrice(e.target.value)}
                  className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm focus:border-[var(--color-accent)] focus:outline-none"
                />
                <button
                  onClick={() => savePrice.mutate()}
                  disabled={savePrice.isPending || !(Number(price) > 0)}
                  className="shrink-0 rounded-lg border border-[var(--color-border-strong)] px-3 py-2 text-sm font-medium hover:bg-[var(--color-hover)] disabled:opacity-50"
                >
                  Set price
                </button>
              </div>
              {pricePreview != null && (
                <p className="mt-1.5 text-xs text-[var(--color-text-muted)]">
                  Values this holding at {formatMoney(pricePreview, holding.currency)}.
                </p>
              )}
            </div>

            <div className="mt-5">
              <div className="mb-2 text-xs font-medium uppercase tracking-wide text-[var(--color-text-subtle)]">
                Orders
              </div>
              {trades.isPending ? (
                <p className="text-sm text-[var(--color-text-muted)]">Loading…</p>
              ) : (
                <div className="max-h-56 space-y-1 overflow-y-auto pr-1">
                  {(trades.data ?? []).map((tr) => (
                    <TradeRow
                      key={tr.id}
                      trade={tr}
                      onSave={(data) => updateTrade.mutate({ tradeId: tr.id, data })}
                      onDelete={() => {
                        if (confirm('Remove this order? The holding will be recomputed without it.')) {
                          deleteTrade.mutate(tr.id)
                        }
                      }}
                      busy={
                        (updateTrade.isPending && updateTrade.variables?.tradeId === tr.id) ||
                        (deleteTrade.isPending && deleteTrade.variables === tr.id)
                      }
                    />
                  ))}
                  {(trades.data ?? []).length === 0 && (
                    <p className="text-sm text-[var(--color-text-muted)]">No orders yet.</p>
                  )}
                </div>
              )}
              <AddTradeForm
                currency={holding.currency}
                onAdd={(data) => addTrade.mutate(data)}
                busy={addTrade.isPending}
              />
            </div>
          </>
        ) : (
          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <Labelled label="Invested">
              <input
                type="number"
                inputMode="decimal"
                value={meta.investedAmount}
                onChange={(e) => setMeta((m) => ({ ...m, investedAmount: e.target.value }))}
                className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
              />
            </Labelled>
            <Labelled label="Current value">
              <input
                type="number"
                inputMode="decimal"
                value={meta.currentValue}
                onChange={(e) => setMeta((m) => ({ ...m, currentValue: e.target.value }))}
                className="w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
              />
            </Labelled>
          </div>
        )}

        <Labelled label="Notes">
          <textarea
            value={meta.notes}
            onChange={(e) => setMeta((m) => ({ ...m, notes: e.target.value }))}
            rows={2}
            className="mt-3 w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm"
          />
        </Labelled>

        <div className="mt-6 flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-lg border border-[var(--color-border)] px-4 py-2 text-sm font-medium hover:bg-[var(--color-hover)]"
          >
            Close
          </button>
          <button
            onClick={() => saveMeta.mutate()}
            disabled={saveMeta.isPending || !meta.name.trim()}
            className="rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-accent-hover)] disabled:opacity-50"
          >
            Save
          </button>
        </div>
      </div>
    </div>
  )
}

/** One order in the trade list — a summary row that expands into an inline
 * edit form, matching how transaction rows edit elsewhere in this app. */
function TradeRow({
  trade,
  onSave,
  onDelete,
  busy,
}: {
  trade: InvestmentTrade
  onSave: (data: TradeInput) => void
  onDelete: () => void
  busy: boolean
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState({
    side: trade.side,
    shares: String(trade.shares),
    price: String(trade.price),
    tradeDate: trade.tradeDate,
  })

  if (!editing) {
    return (
      <div className="flex items-center justify-between rounded px-1 py-1 text-sm hover:bg-[var(--color-hover)]">
        <span className="text-[var(--color-text-muted)]">
          {trade.side === 'buy' ? 'Bought' : 'Sold'} {Number(trade.shares.toFixed(4))} @{' '}
          {formatMoney(trade.price, trade.currency)}
        </span>
        <div className="flex items-center gap-2">
          <span className="text-xs text-[var(--color-text-muted)]">
            {formatDate(trade.tradeDate) || trade.tradeDate}
          </span>
          <button
            type="button"
            title="Edit order"
            onClick={() => setEditing(true)}
            className="text-[var(--color-text-subtle)] hover:text-[var(--color-text)]"
          >
            <Pencil size={12} />
          </button>
          <button
            type="button"
            title="Delete order"
            onClick={onDelete}
            disabled={busy}
            className="text-[var(--color-text-subtle)] hover:text-[var(--color-negative)] disabled:opacity-50"
          >
            <Trash2 size={12} />
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5 rounded bg-[var(--color-surface-2)] px-2 py-1.5">
      <select
        value={draft.side}
        onChange={(e) => setDraft((d) => ({ ...d, side: e.target.value as 'buy' | 'sell' }))}
        className="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      >
        <option value="buy">Buy</option>
        <option value="sell">Sell</option>
      </select>
      <input
        type="number"
        inputMode="decimal"
        placeholder="shares"
        value={draft.shares}
        onChange={(e) => setDraft((d) => ({ ...d, shares: e.target.value }))}
        className="w-20 rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      />
      <input
        type="number"
        inputMode="decimal"
        placeholder="price"
        value={draft.price}
        onChange={(e) => setDraft((d) => ({ ...d, price: e.target.value }))}
        className="w-24 rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      />
      <input
        type="date"
        value={draft.tradeDate}
        onChange={(e) => setDraft((d) => ({ ...d, tradeDate: e.target.value }))}
        className="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      />
      <button
        type="button"
        disabled={busy || !(Number(draft.shares) > 0) || !(Number(draft.price) > 0)}
        onClick={() => {
          onSave({
            side: draft.side,
            shares: Number(draft.shares),
            price: Number(draft.price),
            tradeDate: draft.tradeDate,
          })
          setEditing(false)
        }}
        className="text-[var(--color-positive)] disabled:opacity-50"
        title="Save"
      >
        <Check size={14} />
      </button>
      <button
        type="button"
        onClick={() => setEditing(false)}
        className="text-[var(--color-text-subtle)] hover:text-[var(--color-text)]"
        title="Cancel"
      >
        <X size={14} />
      </button>
    </div>
  )
}

/** A compact inline form for adding an order by hand — a purchase the mail
 * parser never saw, or a real cost correcting an approximated seed. */
function AddTradeForm({
  currency,
  onAdd,
  busy,
}: {
  currency: string
  onAdd: (data: TradeInput) => void
  busy: boolean
}) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState({
    side: 'buy' as 'buy' | 'sell',
    shares: '',
    price: '',
    tradeDate: new Date().toISOString().slice(0, 10),
  })

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="mt-2 flex items-center gap-1.5 text-xs font-medium text-[var(--color-accent-2)] hover:underline"
      >
        <Plus size={13} />
        Add an order
      </button>
    )
  }

  const canSave = Number(draft.shares) > 0 && Number(draft.price) > 0

  return (
    <div className="mt-2 flex flex-wrap items-center gap-1.5 rounded bg-[var(--color-surface-2)] px-2 py-1.5">
      <select
        value={draft.side}
        onChange={(e) => setDraft((d) => ({ ...d, side: e.target.value as 'buy' | 'sell' }))}
        className="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      >
        <option value="buy">Buy</option>
        <option value="sell">Sell</option>
      </select>
      <input
        type="number"
        inputMode="decimal"
        placeholder="shares"
        value={draft.shares}
        onChange={(e) => setDraft((d) => ({ ...d, shares: e.target.value }))}
        className="w-20 rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      />
      <input
        type="number"
        inputMode="decimal"
        placeholder={`price (${currency})`}
        value={draft.price}
        onChange={(e) => setDraft((d) => ({ ...d, price: e.target.value }))}
        className="w-28 rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      />
      <input
        type="date"
        value={draft.tradeDate}
        onChange={(e) => setDraft((d) => ({ ...d, tradeDate: e.target.value }))}
        className="rounded border border-[var(--color-border)] bg-[var(--color-surface)] px-1.5 py-1 text-xs"
      />
      <button
        type="button"
        disabled={busy || !canSave}
        onClick={() => {
          onAdd({
            side: draft.side,
            shares: Number(draft.shares),
            price: Number(draft.price),
            tradeDate: draft.tradeDate,
            orderType: 'manual',
          })
          setDraft({ side: 'buy', shares: '', price: '', tradeDate: draft.tradeDate })
          setOpen(false)
        }}
        className="rounded bg-[var(--color-accent)] px-2.5 py-1 text-xs font-medium text-white disabled:opacity-50"
      >
        Add
      </button>
      <button
        type="button"
        onClick={() => setOpen(false)}
        className="text-xs text-[var(--color-text-muted)] hover:underline"
      >
        Cancel
      </button>
    </div>
  )
}

const KIND_OPTIONS = Object.entries(INVESTMENT_KIND_LABELS)

function AddHoldingForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [form, setForm] = useState({
    name: '',
    kind: 'fd',
    institution: '',
    identifier: '',
    investedAmount: '',
    currentValue: '',
    interestRate: '',
    maturityDate: '',
  })

  const createMutation = useMutation({
    mutationFn: () =>
      api.investments.create({
        name: form.name.trim(),
        kind: form.kind,
        institution: form.institution.trim(),
        identifier: form.identifier.trim(),
        investedAmount: Number(form.investedAmount) || 0,
        // Left blank, the server seeds value from the amount invested.
        currentValue: Number(form.currentValue) || 0,
        interestRate: form.interestRate ? Number(form.interestRate) : undefined,
        maturityDate: form.maturityDate,
      }),
    onSuccess: onDone,
  })

  const field = (key: keyof typeof form) => ({
    value: form[key],
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm((f) => ({ ...f, [key]: e.target.value })),
    className:
      'w-full rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-sm',
  })

  return (
    <Card title="Add a holding">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Labelled label="Name">
          <input placeholder="HDFC FD maturing 2027" {...field('name')} />
        </Labelled>
        <Labelled label="Type">
          <select {...field('kind')}>
            {KIND_OPTIONS.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </Labelled>
        <Labelled label="Institution">
          <input placeholder="HDFC Bank" {...field('institution')} />
        </Labelled>
        <Labelled label="Account / folio">
          <input placeholder="50301125623955" {...field('identifier')} />
        </Labelled>
        <Labelled label="Invested">
          <input type="number" inputMode="decimal" placeholder="10000" {...field('investedAmount')} />
        </Labelled>
        <Labelled label="Current value">
          <input type="number" inputMode="decimal" placeholder="same as invested" {...field('currentValue')} />
        </Labelled>
        <Labelled label="Interest rate %">
          <input type="number" inputMode="decimal" placeholder="7.0" {...field('interestRate')} />
        </Labelled>
        <Labelled label="Maturity date">
          <input type="date" {...field('maturityDate')} />
        </Labelled>
      </div>

      {createMutation.isError && (
        <p className="mt-3 text-sm text-[var(--color-negative)]">
          {createMutation.error instanceof Error ? createMutation.error.message : 'Could not save.'}
        </p>
      )}

      <div className="mt-4 flex items-center gap-2">
        <button
          type="button"
          disabled={!form.name.trim() || createMutation.isPending}
          onClick={() => createMutation.mutate()}
          className="rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)] disabled:opacity-50"
        >
          {createMutation.isPending ? 'Saving…' : 'Save holding'}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-lg border border-[var(--color-border-strong)] px-4 py-2 text-sm font-medium transition-colors hover:bg-[var(--color-hover)]"
        >
          Cancel
        </button>
      </div>
    </Card>
  )
}

function Labelled({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-[var(--color-text-muted)]">{label}</span>
      {children}
    </label>
  )
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-[var(--color-border)] py-24">
      <TrendingUp size={40} className="mb-4 text-[var(--color-text-muted)]" />
      <h2 className="mb-1 text-lg font-semibold">No holdings yet</h2>
      <p className="mb-5 max-w-md text-center text-sm text-[var(--color-text-muted)]">
        Import a fixed-deposit summary from your bank&rsquo;s net banking export, or add a holding by
        hand. Whatever is here counts toward the net worth on your dashboard.
      </p>
      <Link
        to="/money/import"
        className="flex items-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)]"
      >
        <Upload size={16} />
        Import a summary file
      </Link>
    </div>
  )
}
