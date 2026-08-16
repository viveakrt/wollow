import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Search,
  Trash2,
  Tag,
  X,
  Loader2,
  Shuffle,
  Mail,
  ArrowLeftRight,
  Sparkles,
  Users,
  TrendingUp,
  AlertTriangle,
} from 'lucide-react'
import { api } from '../api'
import { formatINR, formatDate } from '../lib/format'
import { EditTransactionModal } from '../components/EditTransactionModal'
import { TRANSFER_KIND_LABELS } from '../types'
import type { ClassifyStatus, Transaction } from '../types'

/** Live progress of a running classification pass. */
function ClassifyProgress({ status }: { status?: ClassifyStatus }) {
  const done = status?.progress?.done ?? 0
  const total = status?.progress?.total ?? 0
  const pct = total > 0 ? Math.round((done / total) * 100) : 0
  return (
    <div className="flex items-center gap-3">
      <Loader2 size={14} className="shrink-0 animate-spin text-[var(--color-accent-2)]" />
      <span className="shrink-0">
        Reading transactions{total > 0 ? ` — ${done} of ${total}` : '…'}
      </span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-[var(--color-hover)]">
        <div
          className="h-full rounded-full bg-[var(--color-accent)] transition-[width] duration-500"
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  )
}

export function Transactions() {
  const queryClient = useQueryClient()

  // Arriving from a message in Mail: ?txn=<id> highlights that transaction.
  const [searchParams, setSearchParams] = useSearchParams()
  const highlightId = Number(searchParams.get('txn')) || null
  const highlightRef = useRef<HTMLTableRowElement>(null)

  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [accountId, setAccountId] = useState('')
  const [categoryId, setCategoryId] = useState('')
  const [type, setType] = useState('')

  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [editing, setEditing] = useState<Transaction | null>(null)
  const [showCategoryMenu, setShowCategoryMenu] = useState(false)
  const [showTransferMenu, setShowTransferMenu] = useState(false)
  const [classifyError, setClassifyError] = useState<string | null>(null)
  const [spreadNote, setSpreadNote] = useState<string | null>(null)

  // Only the free-text box is debounced; the dropdowns fire immediately.
  useEffect(() => {
    const handle = setTimeout(() => setDebouncedSearch(search), 250)
    return () => clearTimeout(handle)
  }, [search])

  const filters = { search: debouncedSearch, accountId, categoryId, type, limit: 200 }

  const { data: transactions = [], isPending: loading } = useQuery({
    queryKey: ['money', 'transactions', filters],
    queryFn: () => api.transactions.list(filters),
  })
  const { data: accounts = [] } = useQuery({
    queryKey: ['money', 'accounts'],
    queryFn: api.accounts.list,
  })
  const { data: categories = [] } = useQuery({
    queryKey: ['money', 'categories'],
    queryFn: api.categories.list,
  })

  // Rows that scrolled out of the current filter can't stay selected.
  useEffect(() => {
    const ids = new Set(transactions.map((t) => t.id))
    setSelected((prev) => {
      const next = new Set<number>()
      prev.forEach((id) => {
        if (ids.has(id)) next.add(id)
      })
      return next.size === prev.size ? prev : next
    })
  }, [transactions])

  // The linked transaction may be older than the 200 rows the list loads, so
  // fetch it directly rather than hoping it happens to be on this page.
  const highlighted = useQuery({
    queryKey: ['money', 'transaction', highlightId],
    queryFn: () => api.transactions.get(highlightId as number),
    enabled: highlightId != null,
  })

  const highlightInList = transactions.some((t) => t.id === highlightId)

  useEffect(() => {
    if (highlightId != null && highlightInList) {
      highlightRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }, [highlightId, highlightInList])

  const invalidateMoney = () => queryClient.invalidateQueries({ queryKey: ['money'] })
  const afterBulk = () => {
    setSelected(new Set())
    invalidateMoney()
  }

  const bulkDeleteMutation = useMutation({
    mutationFn: (ids: number[]) => api.transactions.bulkDelete(ids),
    onSuccess: afterBulk,
  })
  const bulkCategorizeMutation = useMutation({
    mutationFn: ({ ids, catId }: { ids: number[]; catId: number | null }) =>
      api.transactions.bulkCategorize(ids, catId),
    onSuccess: (res) => {
      // Say so when the choice reached rows the user didn't select — silently
      // editing thirty other rows would be alarming to discover later.
      if (res.matched > 0) {
        setClassifyError(null)
        setSpreadNote(
          `Applied to ${res.matched} more transaction${res.matched === 1 ? '' : 's'} with the same description.`,
        )
      }
      afterBulk()
    },
  })
  const linkTransferMutation = useMutation({
    mutationFn: ({ a, b }: { a: number; b: number }) => api.transactions.linkTransfer(a, b),
    onSuccess: afterBulk,
  })
  const markTransferMutation = useMutation({
    mutationFn: ({ ids, kind, counterparty }: { ids: number[]; kind: string; counterparty: string }) =>
      api.transactions.bulkMarkTransfer(ids, kind, counterparty),
    onSuccess: afterBulk,
  })
  // Classification runs detached — one model call per transaction — so the
  // page polls for progress the way Mail's inbox does, quickly while a pass
  // is running and lazily once it settles.
  const classifyStatus = useQuery({
    queryKey: ['money', 'classifyStatus'],
    queryFn: api.transactions.classifyStatus,
    refetchInterval: (q) => (q.state.data?.running ? 2000 : 30000),
  })

  const classifyRunning = classifyStatus.data?.running ?? false
  const classifiedCount = classifyStatus.data?.classified ?? 0

  // A running pass rewrites categories and merchants underneath the list;
  // refresh as it makes progress rather than leaving stale rows on screen.
  useEffect(() => {
    queryClient.invalidateQueries({ queryKey: ['money', 'transactions'] })
  }, [classifyRunning, classifiedCount, queryClient])

  const classifyMutation = useMutation({
    mutationFn: (ids: number[]) => api.transactions.classify(ids.length > 0 ? ids : undefined),
    onSuccess: () => {
      setClassifyError(null)
      setSelected(new Set())
      classifyStatus.refetch()
    },
    onError: (e) => setClassifyError(e instanceof Error ? e.message : 'Classification failed'),
  })

  const bulkBusy =
    bulkDeleteMutation.isPending ||
    bulkCategorizeMutation.isPending ||
    linkTransferMutation.isPending ||
    markTransferMutation.isPending

  function toggleRow(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleAll() {
    setSelected((prev) =>
      prev.size === transactions.length ? new Set() : new Set(transactions.map((t) => t.id)),
    )
  }

  function handleBulkDelete() {
    if (selected.size === 0) return
    if (!confirm(`Delete ${selected.size} transaction${selected.size !== 1 ? 's' : ''}? This cannot be undone.`))
      return
    bulkDeleteMutation.mutate([...selected])
  }

  function handleBulkCategorize(catId: number | null) {
    if (selected.size === 0) return
    setShowCategoryMenu(false)
    bulkCategorizeMutation.mutate({ ids: [...selected], catId })
  }

  function handleMarkTransfer(kind: string) {
    if (selected.size === 0) return
    setShowTransferMenu(false)
    // Family and investment transfers have someone/something on the other
    // side worth recording; a quick prompt beats a second modal.
    let counterparty = ''
    if (kind === 'family' || kind === 'investment') {
      counterparty =
        prompt(kind === 'family' ? 'Who is this for? (e.g. Mom)' : 'Where did it go? (e.g. Zerodha)') ??
        ''
    }
    markTransferMutation.mutate({ ids: [...selected], kind, counterparty })
  }

  const selectedTxns = transactions.filter((t) => selected.has(t.id))
  const canLinkTransfer =
    selectedTxns.length === 2 && selectedTxns[0].accountId !== selectedTxns[1].accountId

  function handleLinkTransfer() {
    if (!canLinkTransfer) return
    linkTransferMutation.mutate({ a: selectedTxns[0].id, b: selectedTxns[1].id })
  }

  const allSelected = transactions.length > 0 && selected.size === transactions.length

  return (
    <div className="p-8 max-w-[1500px] mx-auto">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Transactions</h1>
          <p className="text-[var(--color-text-muted)] text-sm mt-1">
            {transactions.length} transaction{transactions.length !== 1 ? 's' : ''}
          </p>
        </div>
        <div className="flex items-center gap-3">
          {classifyStatus.data && classifyStatus.data.pending > 0 && !classifyRunning && (
            <span className="text-xs text-[var(--color-text-muted)]">
              {classifyStatus.data.pending} unclassified
            </span>
          )}
          <button
            onClick={() => classifyMutation.mutate([...selected])}
            disabled={classifyRunning || classifyMutation.isPending}
            title={
              selected.size > 0
                ? `Re-classify the ${selected.size} selected transactions`
                : 'Classify every transaction that has not been read yet'
            }
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] disabled:opacity-50 transition-colors"
          >
            {classifyRunning || classifyMutation.isPending ? (
              <Loader2 size={16} className="animate-spin" />
            ) : (
              <Sparkles size={16} />
            )}
            {selected.size > 0 ? 'Classify selected' : 'Classify with AI'}
          </button>
        </div>
      </div>

      {spreadNote && (
        <div className="mb-4 flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2.5 text-sm">
          <Tag size={14} className="shrink-0 text-[var(--color-accent-2)]" />
          <span className="flex-1">{spreadNote}</span>
          <button
            onClick={() => setSpreadNote(null)}
            className="rounded-lg p-1 text-[var(--color-text-muted)] hover:bg-[var(--color-hover)]"
          >
            <X size={14} />
          </button>
        </div>
      )}

      {(classifyRunning || classifyError || classifyStatus.data?.error) && (
        <div className="mb-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2.5 text-sm">
          {classifyError || classifyStatus.data?.error ? (
            <div className="flex items-center gap-3">
              <AlertTriangle size={14} className="shrink-0 text-[var(--color-negative)]" />
              <span className="flex-1 text-[var(--color-negative)]">
                {classifyError || classifyStatus.data?.error}
              </span>
              <button
                onClick={() => setClassifyError(null)}
                className="rounded-lg p-1 text-[var(--color-text-muted)] hover:bg-[var(--color-hover)]"
              >
                <X size={14} />
              </button>
            </div>
          ) : (
            <ClassifyProgress status={classifyStatus.data} />
          )}
        </div>
      )}

      {highlightId != null && highlighted.data && (
        <div className="mb-5 flex flex-wrap items-center gap-3 rounded-xl border border-[var(--color-accent)] bg-[var(--color-accent-tint)] px-4 py-3">
          <ArrowLeftRight size={16} className="shrink-0 text-[var(--color-accent-2)]" />
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">
              {highlighted.data.merchant || highlighted.data.narration}
            </div>
            <div className="text-xs text-[var(--color-text-muted)]">
              {formatDate(highlighted.data.txnDate)} · {highlighted.data.accountName} ·{' '}
              {formatINR(highlighted.data.withdrawalAmt + highlighted.data.depositAmt)}
              {!highlightInList && ' · outside the current filter'}
            </div>
          </div>
          {highlighted.data.sourceEmail && (
            <Link
              to={`/mail/messages/${highlighted.data.sourceEmail.mailAccountId}/${highlighted.data.sourceEmail.uid}`}
              className="flex items-center gap-1.5 rounded-lg border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium transition-colors hover:bg-[var(--color-hover)]"
            >
              <Mail size={13} />
              Back to email
            </Link>
          )}
          <button
            type="button"
            onClick={() => setSearchParams({}, { replace: true })}
            aria-label="Dismiss linked transaction"
            className="rounded-lg p-1.5 text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]"
          >
            <X size={14} />
          </button>
        </div>
      )}

      <div className="flex flex-wrap gap-3 mb-5">
        <div className="relative flex-1 min-w-[220px]">
          <Search
            size={16}
            className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-muted)]"
          />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search narration or merchant…"
            className="w-full pl-9 pr-3 py-2 bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
          />
        </div>
        <select
          value={accountId}
          onChange={(e) => setAccountId(e.target.value)}
          className="px-3 py-2 bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
        >
          <option value="">All accounts</option>
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
        <select
          value={categoryId}
          onChange={(e) => setCategoryId(e.target.value)}
          className="px-3 py-2 bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
        >
          <option value="">All categories</option>
          {categories.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <select
          value={type}
          onChange={(e) => setType(e.target.value)}
          className="px-3 py-2 bg-[var(--color-surface)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
        >
          <option value="">All types</option>
          <option value="income">Income</option>
          <option value="expense">Expense</option>
          <option value="transfer">Transfer</option>
        </select>
      </div>

      {selected.size > 0 && (
        <div className="flex items-center gap-3 mb-3 px-4 py-2.5 rounded-lg bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/30">
          <span className="text-sm font-medium">
            {selected.size} selected
          </span>
          <div className="flex-1" />
          {canLinkTransfer && (
            <button
              onClick={handleLinkTransfer}
              disabled={bulkBusy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-border)] text-xs font-medium hover:bg-[var(--color-hover)] disabled:opacity-50"
            >
              <Shuffle size={13} />
              Link as transfer
            </button>
          )}
          <div className="relative">
            <button
              onClick={() => setShowTransferMenu((v) => !v)}
              disabled={bulkBusy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-border)] text-xs font-medium hover:bg-[var(--color-hover)] disabled:opacity-50"
            >
              <ArrowLeftRight size={13} />
              Mark as transfer
            </button>
            {showTransferMenu && (
              <div className="absolute right-0 top-full mt-1 w-56 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg shadow-xl z-10 py-1">
                <button
                  onClick={() => handleMarkTransfer('self')}
                  className="w-full text-left px-3 py-2 text-sm hover:bg-[var(--color-hover)] flex items-center gap-2"
                >
                  <Shuffle size={13} className="text-[var(--color-text-muted)]" />
                  Between my accounts
                </button>
                <button
                  onClick={() => handleMarkTransfer('investment')}
                  className="w-full text-left px-3 py-2 text-sm hover:bg-[var(--color-hover)] flex items-center gap-2"
                >
                  <TrendingUp size={13} className="text-[var(--color-text-muted)]" />
                  To investment
                </button>
                <button
                  onClick={() => handleMarkTransfer('family')}
                  className="w-full text-left px-3 py-2 text-sm hover:bg-[var(--color-hover)] flex items-center gap-2"
                >
                  <Users size={13} className="text-[var(--color-text-muted)]" />
                  To family
                </button>
              </div>
            )}
          </div>
          <div className="relative">
            <button
              onClick={() => setShowCategoryMenu((v) => !v)}
              disabled={bulkBusy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-border)] text-xs font-medium hover:bg-[var(--color-hover)] disabled:opacity-50"
            >
              <Tag size={13} />
              Set category
            </button>
            {showCategoryMenu && (
              <div className="absolute right-0 top-full mt-1 w-56 max-h-64 overflow-y-auto bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg shadow-xl z-10 py-1">
                <button
                  onClick={() => handleBulkCategorize(null)}
                  className="w-full text-left px-3 py-2 text-sm text-[var(--color-text-muted)] hover:bg-[var(--color-hover)]"
                >
                  Uncategorized
                </button>
                {categories.map((c) => (
                  <button
                    key={c.id}
                    onClick={() => handleBulkCategorize(c.id)}
                    className="w-full text-left px-3 py-2 text-sm hover:bg-[var(--color-hover)] flex items-center gap-2"
                  >
                    <span className="w-2 h-2 rounded-full" style={{ background: c.color }} />
                    {c.name}
                  </button>
                ))}
              </div>
            )}
          </div>
          <button
            onClick={handleBulkDelete}
            disabled={bulkBusy}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-negative)] text-[var(--color-negative)] text-xs font-medium hover:bg-[var(--color-negative-tint)] disabled:opacity-50"
          >
            {bulkBusy ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}
            Delete
          </button>
          <button
            onClick={() => setSelected(new Set())}
            className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:bg-[var(--color-hover)]"
          >
            <X size={14} />
          </button>
        </div>
      )}

      <div className="border border-[var(--color-border)] rounded-xl overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-[var(--color-surface)] text-[var(--color-text-muted)] text-xs uppercase tracking-wide">
                <th className="px-4 py-3 w-10">
                  <input
                    type="checkbox"
                    checked={allSelected}
                    onChange={toggleAll}
                    className="accent-[var(--color-accent)]"
                  />
                </th>
                <th className="text-left font-medium px-4 py-3">Date</th>
                <th className="text-left font-medium px-4 py-3">Description</th>
                <th className="text-left font-medium px-4 py-3">Account</th>
                <th className="text-left font-medium px-4 py-3">Category</th>
                <th className="text-right font-medium px-4 py-3">Amount</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={6} className="px-4 py-10 text-center text-[var(--color-text-muted)]">
                    Loading…
                  </td>
                </tr>
              ) : transactions.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-10 text-center text-[var(--color-text-muted)]">
                    No transactions found.
                  </td>
                </tr>
              ) : (
                transactions.map((t) => (
                  <tr
                    key={t.id}
                    ref={t.id === highlightId ? highlightRef : undefined}
                    className={`border-t border-[var(--color-border)] hover:bg-[var(--color-hover)] transition-colors cursor-pointer ${
                      t.id === highlightId
                        ? 'bg-[var(--color-accent-tint)] ring-1 ring-inset ring-[var(--color-accent)]'
                        : selected.has(t.id)
                          ? 'bg-[var(--color-hover)]'
                          : ''
                    }`}
                    onClick={() => setEditing(t)}
                  >
                    <td className="px-4 py-3" onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        checked={selected.has(t.id)}
                        onChange={() => toggleRow(t.id)}
                        className="accent-[var(--color-accent)]"
                      />
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap text-[var(--color-text-muted)]">
                      {formatDate(t.txnDate)}
                    </td>
                    <td className="px-4 py-3 max-w-[380px]">
                      <div className="truncate font-medium">{t.merchant || t.narration}</div>
                      {t.merchant && (
                        <div className="text-xs text-[var(--color-text-muted)] truncate">
                          {t.narration}
                        </div>
                      )}
                      {t.sourceEmail && (
                        <Link
                          to={`/mail/messages/${t.sourceEmail.mailAccountId}/${t.sourceEmail.uid}`}
                          onClick={(e) => e.stopPropagation()}
                          title={t.sourceEmail.subject}
                          className="mt-1 inline-flex max-w-full items-center gap-1 text-xs text-[var(--color-accent-2)] hover:underline"
                        >
                          <Mail size={11} className="shrink-0" />
                          <span className="truncate">Source email</span>
                        </Link>
                      )}
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap text-[var(--color-text-muted)]">
                      {t.accountName}
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap">
                      {t.type === 'transfer' ? (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs bg-[var(--color-hover)] text-[var(--color-text-muted)]">
                          {t.transferKind === 'family' ? (
                            <Users size={11} />
                          ) : t.transferKind === 'investment' ? (
                            <TrendingUp size={11} />
                          ) : (
                            <Shuffle size={11} />
                          )}
                          {TRANSFER_KIND_LABELS[t.transferKind ?? ''] ?? 'Transfer'}
                          {t.counterparty && ` · ${t.counterparty}`}
                        </span>
                      ) : t.categoryName ? (
                        <span
                          className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs"
                          style={{
                            background: `${t.categoryColor}22`,
                            color: t.categoryColor,
                          }}
                        >
                          {t.categoryName}
                        </span>
                      ) : (
                        <span className="text-xs text-[var(--color-text-muted)]">Uncategorized</span>
                      )}
                      {/* The model read this row as something other than what
                          it is, or wasn't sure — open it to accept or ignore. */}
                      {t.ai && !t.ai.applied && (t.ai.needsReview || t.ai.nature !== t.type) && (
                        <span
                          title={t.ai.summary || 'AI has a suggestion for this transaction'}
                          className="ml-1.5 inline-flex items-center gap-1 rounded-full bg-[var(--color-accent)]/10 px-1.5 py-0.5 text-xs text-[var(--color-accent-2)]"
                        >
                          <Sparkles size={10} />
                          {t.ai.nature !== t.type ? TRANSFER_KIND_LABELS[t.ai.transferKind] ? 'transfer?' : `${t.ai.nature}?` : 'review'}
                        </span>
                      )}
                    </td>
                    <td
                      className={`px-4 py-3 text-right whitespace-nowrap font-semibold ${
                        t.type === 'income' ? 'text-[var(--color-positive)]' : t.type === 'transfer' ? 'text-[var(--color-text-muted)]' : ''
                      }`}
                    >
                      {t.type === 'income' ? '+' : '-'}
                      {formatINR(t.depositAmt || t.withdrawalAmt)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {editing && (
        <EditTransactionModal
          transaction={editing}
          accounts={accounts}
          categories={categories}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            invalidateMoney()
          }}
          onDeleted={() => {
            setEditing(null)
            invalidateMoney()
          }}
        />
      )}
    </div>
  )
}
