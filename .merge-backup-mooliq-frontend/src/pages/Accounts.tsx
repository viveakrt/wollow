import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Wallet, Upload, CreditCard, Landmark, PiggyBank, Trash2, X, Loader2 } from 'lucide-react'
import { api } from '../lib/api'
import { formatINR } from '../lib/format'
import { Card } from '../components/Card'
import type { Account } from '../lib/types'

const TYPE_ICON: Record<string, typeof Wallet> = {
  bank: Landmark,
  credit_card: CreditCard,
  wallet: Wallet,
  investment: PiggyBank,
}

export function Accounts() {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [selectMode, setSelectMode] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [bulkBusy, setBulkBusy] = useState(false)

  function reload() {
    return api.accounts.list().then(setAccounts).finally(() => setLoading(false))
  }

  useEffect(() => {
    reload()
  }, [])

  const totalBalance = accounts.reduce((s, a) => s + a.currentBalance, 0)

  function toggle(id: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function handleDeleteOne(id: number) {
    if (!confirm('Delete this account and all its transactions? This cannot be undone.')) return
    setBusyId(id)
    try {
      await api.accounts.delete(id)
      await reload()
    } finally {
      setBusyId(null)
    }
  }

  async function handleBulkDelete() {
    if (selected.size === 0) return
    if (
      !confirm(
        `Delete ${selected.size} account${selected.size !== 1 ? 's' : ''} and all their transactions? This cannot be undone.`,
      )
    )
      return
    setBulkBusy(true)
    try {
      await api.accounts.bulkDelete([...selected])
      setSelected(new Set())
      setSelectMode(false)
      await reload()
    } finally {
      setBulkBusy(false)
    }
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
              className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-white/5 transition-colors"
            >
              {selectMode ? 'Cancel' : 'Select'}
            </button>
          )}
          <Link
            to="/import"
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 transition-colors"
          >
            <Upload size={16} />
            Import Statement
          </Link>
        </div>
      </div>

      {selectMode && selected.size > 0 && (
        <div className="flex items-center gap-3 mb-5 px-4 py-2.5 rounded-lg bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/30">
          <span className="text-sm font-medium">{selected.size} selected</span>
          <div className="flex-1" />
          <button
            onClick={handleBulkDelete}
            disabled={bulkBusy}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-red-500/30 text-red-400 text-xs font-medium hover:bg-red-500/10 disabled:opacity-50"
          >
            {bulkBusy ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}
            Delete
          </button>
          <button
            onClick={() => setSelected(new Set())}
            className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:bg-white/5"
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
            Import a bank statement to automatically create your first account.
          </p>
          <Link
            to="/import"
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 transition-colors"
          >
            <Upload size={16} />
            Import Statement
          </Link>
        </div>
      ) : (
        <>
          <Card className="mb-6">
            <div className="text-sm text-[var(--color-text-muted)] mb-1">Total Balance</div>
            <div className="text-3xl font-semibold tracking-tight">{formatINR(totalBalance)}</div>
          </Card>

          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {accounts.map((a) => {
              const Icon = TYPE_ICON[a.accountType] || Wallet
              return (
                <Card key={a.id} className="relative">
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
                      <div className="w-10 h-10 rounded-lg bg-violet-500/15 flex items-center justify-center">
                        <Icon size={18} className="text-violet-400" />
                      </div>
                      <div>
                        <div className="font-medium">{a.name}</div>
                        <div className="text-xs text-[var(--color-text-muted)]">
                          {a.bank} {a.accountNumber && `· •• ${a.accountNumber.slice(-4)}`}
                        </div>
                      </div>
                    </div>
                    {!selectMode && (
                      <button
                        onClick={() => handleDeleteOne(a.id)}
                        disabled={busyId === a.id}
                        className="p-1.5 rounded-lg text-[var(--color-text-muted)] hover:text-red-400 hover:bg-red-500/10 disabled:opacity-50"
                        title="Delete account"
                      >
                        {busyId === a.id ? (
                          <Loader2 size={14} className="animate-spin" />
                        ) : (
                          <Trash2 size={14} />
                        )}
                      </button>
                    )}
                  </div>
                  <div
                    className={`text-2xl font-semibold tracking-tight ${a.currentBalance < 0 ? 'text-[var(--color-negative)]' : ''}`}
                  >
                    {formatINR(a.currentBalance)}
                  </div>
                  <div className="text-xs text-[var(--color-text-muted)] mt-1 capitalize">
                    {a.accountType.replace('_', ' ')}
                  </div>
                </Card>
              )
            })}
          </div>
        </>
      )}
    </div>
  )
}
