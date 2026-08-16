import { useEffect, useState } from 'react'
import { X, Trash2, Loader2 } from 'lucide-react'
import { api } from '../api'
import { ClassificationPanel } from './ClassificationPanel'
import { TRANSFER_KIND_LABELS } from '../types'
import type { Account, Category, Transaction } from '../types'

export function EditTransactionModal({
  transaction,
  accounts,
  categories,
  onClose,
  onSaved,
  onDeleted,
}: {
  transaction: Transaction
  accounts: Account[]
  categories: Category[]
  onClose: () => void
  onSaved: () => void
  onDeleted: () => void
}) {
  const [accountId, setAccountId] = useState(transaction.accountId)
  const [txnDate, setTxnDate] = useState(transaction.txnDate)
  const [narration, setNarration] = useState(transaction.narration)
  const [merchant, setMerchant] = useState(transaction.merchant)
  const [type, setType] = useState(transaction.type)
  // A transfer can be either leg; whichever amount is set is the one to edit.
  const [amount, setAmount] = useState(
    String(
      transaction.type === 'income'
        ? transaction.depositAmt
        : transaction.withdrawalAmt || transaction.depositAmt,
    ),
  )
  const [categoryId, setCategoryId] = useState(transaction.categoryId ? String(transaction.categoryId) : '')
  const [transferKind, setTransferKind] = useState(transaction.transferKind || 'self')
  const [counterparty, setCounterparty] = useState(transaction.counterparty || '')
  const [notes, setNotes] = useState(transaction.notes)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function handleSave() {
    setError(null)
    const amt = parseFloat(amount)
    if (!amt || amt <= 0) {
      setError('Enter a valid amount')
      return
    }
    setSaving(true)
    // A transfer keeps the transaction's original direction: the money still
    // left (or entered) this account, it just isn't income or spending.
    const isOutflow =
      type === 'expense' || (type === 'transfer' && transaction.depositAmt === 0)
    try {
      await api.transactions.update(transaction.id, {
        accountId,
        txnDate,
        valueDate: txnDate,
        narration,
        merchant,
        type,
        withdrawalAmt: isOutflow ? amt : 0,
        depositAmt: isOutflow ? 0 : amt,
        categoryId: categoryId ? Number(categoryId) : undefined,
        transferKind: type === 'transfer' ? transferKind : undefined,
        counterparty: type === 'transfer' ? counterparty : undefined,
        notes,
      })
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save')
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!confirm('Delete this transaction? This cannot be undone.')) return
    setDeleting(true)
    try {
      await api.transactions.delete(transaction.id)
      onDeleted()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to delete')
      setDeleting(false)
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
          <h2 className="text-lg font-semibold">Edit Transaction</h2>
          <button onClick={onClose} className="text-[var(--color-text-muted)] hover:text-[var(--color-text)]">
            <X size={18} />
          </button>
        </div>

        {error && (
          <div className="mb-4 px-3 py-2 rounded-lg bg-[var(--color-negative-tint)] border border-[var(--color-negative)] text-sm text-[var(--color-negative)]">
            {error}
          </div>
        )}

        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <Field label="Date">
              <input
                type="date"
                value={txnDate}
                onChange={(e) => setTxnDate(e.target.value)}
                className={inputClass}
              />
            </Field>
            <Field label="Type">
              <select value={type} onChange={(e) => setType(e.target.value as typeof type)} className={inputClass}>
                <option value="expense">Expense</option>
                <option value="income">Income</option>
                <option value="transfer">Transfer</option>
              </select>
            </Field>
          </div>

          <Field label="Amount">
            <input
              type="number"
              step="0.01"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              className={inputClass}
            />
          </Field>

          <Field label="Account">
            <select
              value={accountId}
              onChange={(e) => setAccountId(Number(e.target.value))}
              className={inputClass}
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>

          <Field label="Merchant / Payee">
            <input value={merchant} onChange={(e) => setMerchant(e.target.value)} className={inputClass} />
          </Field>

          <Field label="Narration">
            <input value={narration} onChange={(e) => setNarration(e.target.value)} className={inputClass} />
          </Field>

          {type === 'transfer' ? (
            <div className="grid grid-cols-2 gap-3">
              <Field label="Transfer kind">
                <select
                  value={transferKind}
                  onChange={(e) => setTransferKind(e.target.value)}
                  className={inputClass}
                >
                  {Object.entries(TRANSFER_KIND_LABELS).map(([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="To whom / where">
                <input
                  value={counterparty}
                  onChange={(e) => setCounterparty(e.target.value)}
                  placeholder="e.g. Mom, Zerodha"
                  className={inputClass}
                />
              </Field>
            </div>
          ) : (
            <Field label="Category">
              <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)} className={inputClass}>
                <option value="">Uncategorized</option>
                {categories.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </Field>
          )}

          <Field label="Notes">
            <textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              rows={2}
              className={inputClass}
            />
          </Field>

          <ClassificationPanel transaction={transaction} onApplied={onSaved} />
        </div>

        <div className="flex items-center justify-between mt-6">
          <button
            onClick={handleDelete}
            disabled={deleting}
            className="flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm font-medium text-[var(--color-negative)] hover:bg-[var(--color-negative-tint)] disabled:opacity-50"
          >
            {deleting ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
            Delete
          </button>
          <div className="flex gap-2">
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
              Save
            </button>
          </div>
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
      <label className="block text-xs font-medium text-[var(--color-text-muted)] mb-1">{label}</label>
      {children}
    </div>
  )
}
