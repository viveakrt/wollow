import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Search, Trash2, Tag, X, Loader2, Shuffle } from 'lucide-react'
import { api } from '../api'
import { formatINR, formatDate } from '../lib/format'
import { EditTransactionModal } from '../components/EditTransactionModal'
import type { Transaction } from '../types'

export function Transactions() {
  const queryClient = useQueryClient()

  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [accountId, setAccountId] = useState('')
  const [categoryId, setCategoryId] = useState('')
  const [type, setType] = useState('')

  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [editing, setEditing] = useState<Transaction | null>(null)
  const [showCategoryMenu, setShowCategoryMenu] = useState(false)

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
    onSuccess: afterBulk,
  })
  const linkTransferMutation = useMutation({
    mutationFn: ({ a, b }: { a: number; b: number }) => api.transactions.linkTransfer(a, b),
    onSuccess: afterBulk,
  })

  const bulkBusy =
    bulkDeleteMutation.isPending ||
    bulkCategorizeMutation.isPending ||
    linkTransferMutation.isPending

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
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Transactions</h1>
        <p className="text-[var(--color-text-muted)] text-sm mt-1">
          {transactions.length} transaction{transactions.length !== 1 ? 's' : ''}
        </p>
      </div>

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
                    className={`border-t border-[var(--color-border)] hover:bg-white/[0.02] transition-colors cursor-pointer ${selected.has(t.id) ? 'bg-[var(--color-accent)]/5' : ''}`}
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
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap text-[var(--color-text-muted)]">
                      {t.accountName}
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap">
                      {t.type === 'transfer' ? (
                        <span className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs bg-[var(--color-hover)] text-[var(--color-text-muted)]">
                          <Shuffle size={11} />
                          Transfer
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
