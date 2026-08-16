import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Wallet,
  Upload,
  CreditCard,
  Landmark,
  PiggyBank,
  Trash2,
  X,
  Loader2,
  Plus,
  Pencil,
  Banknote,
  Users,
  Eye,
  EyeOff,
} from 'lucide-react'
import { api } from '../api'
import { formatINR } from '../lib/format'
import { Card } from '../components/Card'
import { AccountFormModal } from '../components/AccountFormModal'
import { ACCOUNT_TYPE_LABELS, LIABILITY_TYPES } from '../types'
import type { Account } from '../types'

/**
 * The line under an account's name: its bank and masked tail, minus whatever
 * the name already says. "Axis Bank •• 5792" followed by "Axis · •• 5792" is
 * pure stutter.
 */
function accountSubtitle(name: string, bank: string, accountNumber: string): string {
  const lower = name.toLowerCase()
  const parts: string[] = []
  if (bank && !lower.startsWith(bank.toLowerCase())) parts.push(bank)
  if (accountNumber) {
    const last4 = accountNumber.slice(-4)
    if (!lower.includes(last4)) parts.push(`•• ${last4}`)
  }
  return parts.join(' · ')
}

const TYPE_ICON: Record<string, typeof Wallet> = {
  bank: Landmark,
  credit_card: CreditCard,
  wallet: Wallet,
  cash: Banknote,
  investment: PiggyBank,
  ppf: PiggyBank,
  fd: PiggyBank,
  loan: CreditCard,
  family: Users,
}

export function Accounts() {
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [selectMode, setSelectMode] = useState(false)
  // The modal state distinguishes "closed" (null) from "create" ('new') from
  // "edit" (the account being edited).
  const [modal, setModal] = useState<Account | 'new' | null>(null)

  const { data: accounts = [], isPending: loading } = useQuery({
    queryKey: ['money', 'accounts'],
    queryFn: api.accounts.list,
  })

  // Deleting an account cascades to its transactions, so the dashboard and
  // transaction list are stale too — invalidate the whole money namespace.
  const invalidateMoney = () => queryClient.invalidateQueries({ queryKey: ['money'] })

  const deleteOne = useMutation({ mutationFn: api.accounts.delete, onSuccess: invalidateMoney })
  const bulkDelete = useMutation({
    mutationFn: (ids: number[]) => api.accounts.bulkDelete(ids),
    onSuccess: () => {
      setSelected(new Set())
      setSelectMode(false)
      invalidateMoney()
    },
  })
  // Flipping the net-worth switch resends the account as-is with the flag
  // inverted — the update endpoint takes the whole record.
  const toggleNetworth = useMutation({
    mutationFn: (a: Account) => api.accounts.update(a.id, { ...a, includeInNetworth: !a.includeInNetworth }),
    onSuccess: invalidateMoney,
  })

  const busyId = deleteOne.isPending ? deleteOne.variables : null
  const togglingId = toggleNetworth.isPending ? toggleNetworth.variables?.id : null
  const bulkBusy = bulkDelete.isPending

  // Money held and money owed are shown apart, and only for accounts that
  // count: an excluded account is on the page but out of the totals, which is
  // the entire point of excluding it.
  const counted = accounts.filter((a) => a.includeInNetworth)
  const excludedCount = accounts.length - counted.length
  const totals = counted.reduce(
    (acc, a) => {
      if (a.currentBalance < 0) acc.owed += -a.currentBalance
      else acc.held += a.currentBalance
      return acc
    },
    { held: 0, owed: 0 },
  )

  function toggle(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function handleDeleteOne(id: number) {
    if (!confirm('Delete this account and all its transactions? This cannot be undone.')) return
    deleteOne.mutate(id)
  }

  function handleBulkDelete() {
    if (selected.size === 0) return
    if (
      !confirm(
        `Delete ${selected.size} account${selected.size !== 1 ? 's' : ''} and all their transactions? This cannot be undone.`,
      )
    )
      return
    bulkDelete.mutate([...selected])
  }

  return (
    <div className="p-8 max-w-[1500px] mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Accounts</h1>
          <p className="text-[var(--color-text-muted)] text-sm mt-1">
            All your financial accounts in one place.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {accounts.length > 0 && (
            <button
              onClick={() => {
                setSelectMode((v) => !v)
                setSelected(new Set())
              }}
              className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)] transition-colors"
            >
              {selectMode ? 'Cancel' : 'Select'}
            </button>
          )}
          <Link
            to="/money/import"
            className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)] transition-colors"
          >
            <Upload size={16} />
            Import Statement
          </Link>
          <button
            onClick={() => setModal('new')}
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] transition-colors"
          >
            <Plus size={16} />
            Add Account
          </button>
        </div>
      </div>

      {selectMode && selected.size > 0 && (
        <div className="flex items-center gap-3 mb-5 px-4 py-2.5 rounded-lg bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/30">
          <span className="text-sm font-medium">{selected.size} selected</span>
          <div className="flex-1" />
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

      {loading ? (
        <div className="text-[var(--color-text-muted)]">Loading…</div>
      ) : accounts.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 border border-dashed border-[var(--color-border)] rounded-xl">
          <Wallet size={40} className="text-[var(--color-text-muted)] mb-4" />
          <h2 className="text-lg font-semibold mb-1">No accounts yet</h2>
          <p className="text-sm text-[var(--color-text-muted)] mb-5">
            Import a bank statement, or add an account by hand — cash counts too.
          </p>
          <div className="flex items-center gap-2">
            <Link
              to="/money/import"
              className="flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)] transition-colors"
            >
              <Upload size={16} />
              Import Statement
            </Link>
            <button
              onClick={() => setModal('new')}
              className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] transition-colors"
            >
              <Plus size={16} />
              Add Account
            </button>
          </div>
        </div>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Card>
              <div className="mb-1 text-sm text-[var(--color-text-muted)]">Money held</div>
              <div className="text-3xl font-semibold tracking-tight">{formatINR(totals.held)}</div>
            </Card>
            <Card>
              <div className="mb-1 text-sm text-[var(--color-text-muted)]">Money owed</div>
              <div className="text-3xl font-semibold tracking-tight text-[var(--color-negative)]">
                {formatINR(totals.owed)}
              </div>
            </Card>
            <Card>
              <div className="mb-1 text-sm text-[var(--color-text-muted)]">Net</div>
              <div className="text-3xl font-semibold tracking-tight">
                {formatINR(totals.held - totals.owed)}
              </div>
              {excludedCount > 0 && (
                <div className="mt-1 text-xs text-[var(--color-text-muted)]">
                  {excludedCount} account{excludedCount !== 1 ? 's' : ''} not counted
                </div>
              )}
            </Card>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {accounts.map((a) => {
              const Icon = TYPE_ICON[a.accountType] || Wallet
              return (
                <Card key={a.id} className={`relative ${a.includeInNetworth ? '' : 'opacity-60'}`}>
                  <div className="flex items-start justify-between mb-4">
                    <div className="flex items-center gap-3">
                      {selectMode && (
                        <input
                          type="checkbox"
                          checked={selected.has(a.id)}
                          onChange={() => toggle(a.id)}
                          className="accent-[var(--color-accent)]"
                        />
                      )}
                      <div className="w-10 h-10 rounded-lg bg-[var(--color-tint-violet-bg)] flex items-center justify-center">
                        <Icon size={18} className="text-[var(--color-tint-violet)]" />
                      </div>
                      <div>
                        <div className="font-medium">{a.name}</div>
                        <div className="text-xs text-[var(--color-text-muted)]">
                          {accountSubtitle(a.name, a.bank, a.accountNumber)}
                        </div>
                      </div>
                    </div>
                    {!selectMode && (
                      <div className="flex items-center gap-1">
                        <button
                          onClick={() => toggleNetworth.mutate(a)}
                          disabled={togglingId === a.id}
                          className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-hover)] disabled:opacity-50"
                          title={
                            a.includeInNetworth
                              ? 'Counted in net worth — click to exclude'
                              : 'Not counted in net worth — click to include'
                          }
                        >
                          {togglingId === a.id ? (
                            <Loader2 size={14} className="animate-spin" />
                          ) : a.includeInNetworth ? (
                            <Eye size={14} />
                          ) : (
                            <EyeOff size={14} />
                          )}
                        </button>
                        <button
                          onClick={() => setModal(a)}
                          className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-hover)]"
                          title="Edit account"
                        >
                          <Pencil size={14} />
                        </button>
                        <button
                          onClick={() => handleDeleteOne(a.id)}
                          disabled={busyId === a.id}
                          className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-negative)] hover:bg-[var(--color-negative-tint)] disabled:opacity-50"
                          title="Delete account"
                        >
                          {busyId === a.id ? (
                            <Loader2 size={14} className="animate-spin" />
                          ) : (
                            <Trash2 size={14} />
                          )}
                        </button>
                      </div>
                    )}
                  </div>
                  <div
                    className={`text-2xl font-semibold tracking-tight ${a.currentBalance < 0 ? 'text-[var(--color-negative)]' : ''}`}
                  >
                    {formatINR(a.currentBalance)}
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-[var(--color-text-muted)]">
                    <span>{ACCOUNT_TYPE_LABELS[a.accountType] ?? a.accountType}</span>
                    {LIABILITY_TYPES.has(a.accountType) && a.currentBalance < 0 && (
                      <span className="text-[var(--color-negative)]">outstanding</span>
                    )}
                    {a.creditLimit > 0 && <span>· {formatINR(a.creditLimit, true)} limit</span>}
                    {a.source === 'email' && <span>· found in mail</span>}
                    {!a.includeInNetworth && (
                      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[var(--color-hover)]">
                        <EyeOff size={10} />
                        not counted
                      </span>
                    )}
                  </div>
                </Card>
              )
            })}
          </div>
        </>
      )}

      {modal && (
        <AccountFormModal
          account={modal === 'new' ? null : modal}
          onClose={() => setModal(null)}
          onSaved={() => {
            setModal(null)
            invalidateMoney()
          }}
        />
      )}
    </div>
  )
}
