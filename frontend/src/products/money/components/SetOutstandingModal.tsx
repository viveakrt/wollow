import { useEffect, useState } from 'react'
import { X, Loader2 } from 'lucide-react'
import { api } from '../api'
import { LIABILITY_TYPES } from '../types'
import type { Account } from '../types'

/**
 * State an account's balance by hand — a card's outstanding, a bank balance
 * from the app. Recorded as a snapshot the running balance anchors on, so it
 * survives later alerts instead of being overwritten by them.
 */
export function SetOutstandingModal({
  account,
  onClose,
  onSaved,
}: {
  account: Account
  onClose: () => void
  onSaved: () => void
}) {
  const liability = LIABILITY_TYPES.has(account.accountType)
  const [amount, setAmount] = useState(
    String(liability ? Math.max(0, -account.currentBalance) : account.currentBalance),
  )
  const [asOf, setAsOf] = useState(new Date().toISOString().slice(0, 10))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function save() {
    const value = parseFloat(amount)
    if (Number.isNaN(value) || value < 0) {
      setError('Enter the amount as a positive number')
      return
    }
    setSaving(true)
    setError(null)
    try {
      await api.accounts.setBalance(account.id, value, asOf)
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save')
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div
        className="w-full max-w-sm rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">{liability ? 'Set outstanding' : 'Set balance'}</h2>
          <button onClick={onClose} className="text-[var(--color-text-muted)] hover:text-[var(--color-text)]">
            <X size={18} />
          </button>
        </div>
        <p className="mb-4 text-xs text-[var(--color-text-muted)]">
          {liability
            ? `What ${account.name} owes right now, as the card app shows it.`
            : `What ${account.name} holds right now. Later transactions move it from here.`}
        </p>
        {error && (
          <div className="mb-3 rounded-lg border border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]">
            {error}
          </div>
        )}
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-xs font-medium text-[var(--color-text-muted)]">Amount (₹)</label>
            <input
              type="number"
              step="0.01"
              min="0"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              className={inputClass}
              autoFocus
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[var(--color-text-muted)]">As of</label>
            <input type="date" value={asOf} onChange={(e) => setAsOf(e.target.value)} className={inputClass} />
          </div>
        </div>
        <div className="mt-6 flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-lg border border-[var(--color-border)] px-4 py-2 text-sm font-medium hover:bg-[var(--color-hover)]"
          >
            Cancel
          </button>
          <button
            onClick={save}
            disabled={saving}
            className="flex items-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-accent-hover)] disabled:opacity-50"
          >
            {saving && <Loader2 size={14} className="animate-spin" />}
            Save
          </button>
        </div>
      </div>
    </div>
  )
}

const inputClass =
  'w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]'
