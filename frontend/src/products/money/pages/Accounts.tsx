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
  Archive,
  ArchiveRestore,
  Gauge,
} from 'lucide-react'
import { api } from '../api'
import { formatINR } from '../lib/format'
import { Card } from '../components/Card'
import { AccountFormModal } from '../components/AccountFormModal'
import { SetOutstandingModal } from '../components/SetOutstandingModal'
import { PendingAccountsPanel } from '../components/PendingAccountsPanel'
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

/** Accounts read in groups: what's spendable, what's owed, what's put away. */
const GROUPS: { title: string; types: string[] }[] = [
  { title: 'Bank & cash', types: ['bank', 'cash'] },
  { title: 'Credit cards & loans', types: ['credit_card', 'loan'] },
  { title: 'Wallets', types: ['wallet'] },
  { title: 'Investments & deposits', types: ['investment', 'ppf', 'fd'] },
  { title: 'Family & other', types: ['family', 'other'] },
]

function groupOf(type: string): string {
  return GROUPS.find((g) => g.types.includes(type))?.title ?? 'Family & other'
}

export function Accounts() {
  const queryClient = useQueryClient()
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [selectMode, setSelectMode] = useState(false)
  const [showArchived, setShowArchived] = useState(false)
  // The modal state distinguishes "closed" (null) from "create" ('new') from
  // "edit" (the account being edited).
  const [modal, setModal] = useState<Account | 'new' | null>(null)
  const [balanceFor, setBalanceFor] = useState<Account | null>(null)

  const { data: accounts = [], isPending: loading } = useQuery({
    queryKey: ['money', 'accounts', { includeArchived: showArchived }],
    queryFn: showArchived ? api.accounts.listAll : api.accounts.list,
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
  const archive = useMutation({
    mutationFn: (a: Account) => (a.archivedAt ? api.accounts.unarchive(a.id) : api.accounts.archive(a.id)),
    onSuccess: invalidateMoney,
  })

  const busyId = deleteOne.isPending ? deleteOne.variables : null
  const togglingId = toggleNetworth.isPending ? toggleNetworth.variables?.id : null
  const archivingId = archive.isPending ? archive.variables?.id : null
  const bulkBusy = bulkDelete.isPending

  // Money held and money owed are shown apart, by what the account *is*
  // rather than by the sign of its balance, and only for active accounts that
  // count: an excluded account is on the page but out of the totals.
  const active = accounts.filter((a) => !a.archivedAt)
  const counted = active.filter((a) => a.includeInNetworth)
  const excludedCount = active.length - counted.length
  const totals = counted.reduce(
    (acc, a) => {
      if (LIABILITY_TYPES.has(a.accountType)) acc.owed += Math.max(0, -a.currentBalance)
      else if (a.currentBalance < 0) acc.owed += -a.currentBalance
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
    if (!confirm('Delete this account and all its transactions? This cannot be undone. Archive keeps the history instead.')) return
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

  const grouped = GROUPS.map((g) => ({
    ...g,
    members: accounts.filter((a) => groupOf(a.accountType) === g.title),
  })).filter((g) => g.members.length > 0)

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
          <label className="mr-2 flex items-center gap-2 text-sm text-[var(--color-text-muted)]">
            <input
              type="checkbox"
              checked={showArchived}
              onChange={(e) => setShowArchived(e.target.checked)}
              className="accent-[var(--color-accent)]"
            />
            Show archived
          </label>
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

      <PendingAccountsPanel />

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
            Add an account by hand, or import a bank statement — cash counts too. Mail never creates
            accounts on its own.
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

          <div className="space-y-8">
            {grouped.map((g) => (
              <section key={g.title}>
                <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
                  {g.title}
                </h2>
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                  {g.members.map((a) => (
                    <AccountCard
                      key={a.id}
                      account={a}
                      selectMode={selectMode}
                      selected={selected.has(a.id)}
                      onToggleSelect={() => toggle(a.id)}
                      busy={busyId === a.id}
                      toggling={togglingId === a.id}
                      archiving={archivingId === a.id}
                      onToggleNetworth={() => toggleNetworth.mutate(a)}
                      onEdit={() => setModal(a)}
                      onArchive={() => archive.mutate(a)}
                      onDelete={() => handleDeleteOne(a.id)}
                      onSetBalance={() => setBalanceFor(a)}
                    />
                  ))}
                </div>
              </section>
            ))}
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
      {balanceFor && (
        <SetOutstandingModal
          account={balanceFor}
          onClose={() => setBalanceFor(null)}
          onSaved={() => {
            setBalanceFor(null)
            invalidateMoney()
          }}
        />
      )}
    </div>
  )
}

function AccountCard({
  account: a,
  selectMode,
  selected,
  onToggleSelect,
  busy,
  toggling,
  archiving,
  onToggleNetworth,
  onEdit,
  onArchive,
  onDelete,
  onSetBalance,
}: {
  account: Account
  selectMode: boolean
  selected: boolean
  onToggleSelect: () => void
  busy: boolean
  toggling: boolean
  archiving: boolean
  onToggleNetworth: () => void
  onEdit: () => void
  onArchive: () => void
  onDelete: () => void
  onSetBalance: () => void
}) {
  const Icon = TYPE_ICON[a.accountType] || Wallet
  const liability = LIABILITY_TYPES.has(a.accountType)
  const outstanding = liability ? Math.max(0, -a.currentBalance) : 0
  const available = liability && a.creditLimit > 0 ? Math.max(0, a.creditLimit - outstanding) : null
  const utilisation = liability && a.creditLimit > 0 ? Math.min(1, outstanding / a.creditLimit) : null
  const dimmed = Boolean(a.archivedAt) || !a.includeInNetworth

  return (
    <Card className={`relative ${dimmed ? 'opacity-60' : ''}`}>
      <div className="flex items-start justify-between mb-4">
        <div className="flex items-center gap-3">
          {selectMode && (
            <input
              type="checkbox"
              checked={selected}
              onChange={onToggleSelect}
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
            {!a.archivedAt && (
              <button
                onClick={onToggleNetworth}
                disabled={toggling}
                className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-hover)] disabled:opacity-50"
                title={
                  a.includeInNetworth
                    ? 'Counted in net worth — click to exclude'
                    : 'Not counted in net worth — click to include'
                }
              >
                {toggling ? (
                  <Loader2 size={14} className="animate-spin" />
                ) : a.includeInNetworth ? (
                  <Eye size={14} />
                ) : (
                  <EyeOff size={14} />
                )}
              </button>
            )}
            <button
              onClick={onEdit}
              className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-hover)]"
              title="Edit account"
            >
              <Pencil size={14} />
            </button>
            <button
              onClick={onArchive}
              disabled={archiving}
              className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-[var(--color-hover)] disabled:opacity-50"
              title={a.archivedAt ? 'Restore account' : 'Archive — hide it, keep its history'}
            >
              {archiving ? (
                <Loader2 size={14} className="animate-spin" />
              ) : a.archivedAt ? (
                <ArchiveRestore size={14} />
              ) : (
                <Archive size={14} />
              )}
            </button>
            <button
              onClick={onDelete}
              disabled={busy}
              className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-[var(--color-negative)] hover:bg-[var(--color-negative-tint)] disabled:opacity-50"
              title="Delete account and its transactions"
            >
              {busy ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
            </button>
          </div>
        )}
      </div>

      {liability ? (
        <>
          <div className="flex items-baseline justify-between gap-3">
            <div>
              <div className="text-xs text-[var(--color-text-muted)]">Outstanding</div>
              <div
                className={`text-2xl font-semibold tracking-tight ${outstanding > 0 ? 'text-[var(--color-negative)]' : ''}`}
              >
                {formatINR(outstanding)}
              </div>
            </div>
            {available != null && (
              <div className="text-right">
                <div className="text-xs text-[var(--color-text-muted)]">Available</div>
                <div className="text-sm font-medium">{formatINR(available)}</div>
              </div>
            )}
          </div>
          {utilisation != null && (
            <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-hover)]">
              <div
                className={`h-full rounded-full ${utilisation > 0.8 ? 'bg-[var(--color-negative)]' : 'bg-[var(--color-accent)]'}`}
                style={{ width: `${Math.round(utilisation * 100)}%` }}
              />
            </div>
          )}
          {a.currentBalance > 0 && (
            <div className="mt-1 text-xs text-[var(--color-positive)]">
              {formatINR(a.currentBalance)} in credit (overpaid)
            </div>
          )}
        </>
      ) : (
        <div
          className={`text-2xl font-semibold tracking-tight ${a.currentBalance < 0 ? 'text-[var(--color-negative)]' : ''}`}
        >
          {formatINR(a.currentBalance)}
        </div>
      )}

      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-[var(--color-text-muted)]">
        <span>{ACCOUNT_TYPE_LABELS[a.accountType] ?? a.accountType}</span>
        {liability && (
          <span>
            · {a.creditLimit > 0 ? `${formatINR(a.creditLimit, true)} limit` : 'limit unknown'}
          </span>
        )}
        {a.source === 'email' && <span>· found in mail</span>}
        {a.archivedAt && (
          <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[var(--color-hover)]">
            <Archive size={10} />
            archived
          </span>
        )}
        {!a.includeInNetworth && !a.archivedAt && (
          <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-[var(--color-hover)]">
            <EyeOff size={10} />
            not counted
          </span>
        )}
        {!selectMode && !a.archivedAt && (
          <button
            onClick={onSetBalance}
            className="ml-auto inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[var(--color-accent-2)] hover:bg-[var(--color-accent-tint)]"
            title={liability ? 'State what the card owes right now' : 'State the balance right now'}
          >
            <Gauge size={11} />
            {liability ? 'Set outstanding' : 'Set balance'}
          </button>
        )}
      </div>
    </Card>
  )
}
