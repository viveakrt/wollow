import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { X, Loader2 } from 'lucide-react'
import { api } from '../api'
import { ACCOUNT_TYPE_LABELS, LIABILITY_TYPES } from '../types'
import type { Account } from '../types'

/**
 * Create or edit an account by hand. Statement import and mail discovery
 * cover the common cases, but cash, a family member's account, or a bank
 * that never emails all have to be enterable directly — email is a helper
 * here, not the gatekeeper.
 */
export function AccountFormModal({
  account,
  onClose,
  onSaved,
}: {
  /** null creates a new account; an existing account edits it. */
  account: Account | null
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(account?.name ?? '')
  const [accountType, setAccountType] = useState(account?.accountType ?? 'bank')
  const [bank, setBank] = useState(account?.bank ?? '')
  const [accountNumber, setAccountNumber] = useState(account?.accountNumber ?? '')
  const [openingBalance, setOpeningBalance] = useState(
    account ? String(account.openingBalance) : '0',
  )
  const [creditLimit, setCreditLimit] = useState(account ? String(account.creditLimit) : '0')
  const [includeInNetworth, setIncludeInNetworth] = useState(account?.includeInNetworth ?? true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Offered so the stored bank is the issuer code the mail parsers key on —
  // an account saved as "HDFC Bank Ltd" would never receive its own alerts.
  const { data: institutions = [] } = useQuery({
    queryKey: ['money', 'institutions'],
    queryFn: api.institutions.list,
    staleTime: Infinity,
  })

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // Picking "family account" is a statement that this money is someone
  // else's: default it out of the owner's net worth, but stay overridable.
  function handleTypeChange(next: string) {
    if (next === 'family' && accountType !== 'family') setIncludeInNetworth(false)
    setAccountType(next)
  }

  async function handleSave() {
    setError(null)
    if (!name.trim()) {
      setError('Give the account a name')
      return
    }
    const opening = parseFloat(openingBalance) || 0
    const limit = parseFloat(creditLimit) || 0
    setSaving(true)
    const payload = {
      name: name.trim(),
      accountType,
      bank: bank.trim(),
      accountNumber: accountNumber.trim(),
      openingBalance: opening,
      creditLimit: limit,
      currency: account?.currency || 'INR',
      ifsc: account?.ifsc ?? '',
      branch: account?.branch ?? '',
      includeInNetworth,
    }
    try {
      if (account) await api.accounts.update(account.id, payload)
      else await api.accounts.create(payload)
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save')
      setSaving(false)
    }
  }

  return (
    <div
      className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4"
      onClick={onClose}
    >
      <div
        className="bg-[var(--color-surface)] border border-[var(--color-border)] rounded-xl w-full max-w-md p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-5">
          <h2 className="text-lg font-semibold">{account ? 'Edit Account' : 'Add Account'}</h2>
          <button
            onClick={onClose}
            className="text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
          >
            <X size={18} />
          </button>
        </div>

        {error && (
          <div className="mb-4 px-3 py-2 rounded-lg bg-[var(--color-negative-tint)] border border-[var(--color-negative)] text-sm text-[var(--color-negative)]">
            {error}
          </div>
        )}

        <div className="space-y-3">
          <Field label="Name">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Cash in hand, Mom's SBI account"
              className={inputClass}
              autoFocus
            />
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label="Type">
              <select
                value={accountType}
                onChange={(e) => handleTypeChange(e.target.value)}
                className={inputClass}
              >
                {Object.entries(ACCOUNT_TYPE_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Bank / Institution">
              <input
                value={bank}
                onChange={(e) => setBank(e.target.value)}
                list="money-institutions"
                placeholder="e.g. HDFC"
                className={inputClass}
              />
              <datalist id="money-institutions">
                {institutions.map((inst) => (
                  <option key={inst.issuer} value={inst.issuer}>
                    {inst.name}
                  </option>
                ))}
              </datalist>
            </Field>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <Field label="Account number (optional)">
              <input
                value={accountNumber}
                onChange={(e) => setAccountNumber(e.target.value)}
                placeholder="or just the last 4 digits"
                className={inputClass}
              />
            </Field>
            {/* Both fields are how alerts find this account: the issuer plus
                the last four digits are what a bank's mail states. */}
            <Field label={account ? 'Opening balance' : 'Current balance'}>
              <input
                type="number"
                step="0.01"
                value={openingBalance}
                onChange={(e) => setOpeningBalance(e.target.value)}
                className={inputClass}
              />
            </Field>
          </div>

          {LIABILITY_TYPES.has(accountType) && (
            <Field label="Credit limit (0 if unknown)">
              <input
                type="number"
                step="0.01"
                value={creditLimit}
                onChange={(e) => setCreditLimit(e.target.value)}
                className={inputClass}
              />
            </Field>
          )}

          <label className="flex items-start gap-3 pt-1 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={includeInNetworth}
              onChange={(e) => setIncludeInNetworth(e.target.checked)}
              className="mt-0.5 accent-[var(--color-accent)]"
            />
            <span>
              <span className="block text-sm font-medium">Count in net worth</span>
              <span className="block text-xs text-[var(--color-text-muted)]">
                Turn off for accounts you track but don't own — a family member's account, or one
                you don't want in your totals.
              </span>
            </span>
          </label>
        </div>

        <div className="flex justify-end gap-2 mt-6">
          <button
            onClick={onClose}
            className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)]"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={saving}
            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] disabled:opacity-50"
          >
            {saving && <Loader2 size={14} className="animate-spin" />}
            {account ? 'Save' : 'Add account'}
          </button>
        </div>
      </div>
    </div>
  )
}

const inputClass =
  'w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]'

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-xs font-medium text-[var(--color-text-muted)] mb-1">
        {label}
      </label>
      {children}
    </div>
  )
}
