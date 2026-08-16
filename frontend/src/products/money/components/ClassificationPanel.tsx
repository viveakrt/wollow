import { useState } from 'react'
import { Sparkles, Check, X, Loader2, Repeat, Receipt, Undo2, AlertTriangle } from 'lucide-react'
import { api } from '../api'
import { TRANSFER_KIND_LABELS } from '../types'
import type { Transaction } from '../types'

/**
 * What the model made of one transaction, shown inside the edit modal.
 *
 * The reading is presented as a claim to accept or reject, never as a fact
 * already applied: the background pass fills only blanks, so anything that
 * would change the transaction's meaning — an expense that is really a
 * transfer — waits here for a person.
 */
export function ClassificationPanel({
  transaction,
  onApplied,
}: {
  transaction: Transaction
  onApplied: () => void
}) {
  const ai = transaction.ai
  const [busy, setBusy] = useState<'apply' | 'dismiss' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [resolved, setResolved] = useState(false)

  if (!ai) return null

  // Worth asking about only when the model read the transaction as something
  // other than what it currently is; a category it already filled in silently
  // needs no confirmation.
  const suggestsTypeChange = ai.nature !== transaction.type
  const pending = !resolved && !ai.applied && (suggestsTypeChange || ai.needsReview)

  async function run(action: 'apply' | 'dismiss') {
    setBusy(action)
    setError(null)
    try {
      if (action === 'apply') await api.transactions.applyClassification(transaction.id)
      else await api.transactions.dismissClassification(transaction.id)
      setResolved(true)
      onApplied()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-3">
      <div className="mb-2 flex items-center gap-2">
        <Sparkles size={13} className="text-[var(--color-accent-2)]" />
        <span className="text-xs font-medium">AI reading</span>
        <span className="text-xs text-[var(--color-text-muted)]">
          {Math.round(ai.confidence * 100)}% confident
        </span>
        {ai.needsReview && (
          <span className="ml-auto inline-flex items-center gap-1 text-xs text-[var(--color-tint-orange)]">
            <AlertTriangle size={11} />
            needs review
          </span>
        )}
      </div>

      {ai.summary && (
        <p className="mb-2 text-xs text-[var(--color-text-muted)]">{ai.summary}</p>
      )}

      <div className="flex flex-wrap gap-1.5">
        {ai.nature === 'transfer' ? (
          <Chip>
            {TRANSFER_KIND_LABELS[ai.transferKind] ?? 'Transfer'}
            {ai.counterparty && ` · ${ai.counterparty}`}
          </Chip>
        ) : (
          ai.category && <Chip>{ai.category}</Chip>
        )}
        {ai.subcategory && <Chip>{ai.subcategory}</Chip>}
        {ai.merchant && <Chip>{ai.merchant}</Chip>}
        {ai.paymentMethod && <Chip>{ai.paymentMethod}</Chip>}
        {ai.isRecurring && (
          <Chip>
            <Repeat size={10} /> recurring
          </Chip>
        )}
        {ai.isBill && (
          <Chip>
            <Receipt size={10} /> bill
          </Chip>
        )}
        {ai.isRefund && (
          <Chip>
            <Undo2 size={10} /> refund
          </Chip>
        )}
      </div>

      {error && <p className="mt-2 text-xs text-[var(--color-negative)]">{error}</p>}

      {pending && (
        <div className="mt-3 flex items-center gap-2">
          <span className="flex-1 text-xs text-[var(--color-text-muted)]">
            {suggestsTypeChange
              ? `Record this as ${ai.nature === 'transfer' ? 'a transfer' : ai.nature}?`
              : 'Accept this reading?'}
          </span>
          <button
            type="button"
            onClick={() => run('apply')}
            disabled={busy !== null}
            className="flex items-center gap-1 rounded-lg bg-[var(--color-positive-tint)] px-2.5 py-1 text-xs font-medium text-[var(--color-positive)] hover:opacity-80 disabled:opacity-50"
          >
            {busy === 'apply' ? <Loader2 size={12} className="animate-spin" /> : <Check size={12} />}
            Apply
          </button>
          <button
            type="button"
            onClick={() => run('dismiss')}
            disabled={busy !== null}
            className="flex items-center gap-1 rounded-lg border border-[var(--color-border)] px-2.5 py-1 text-xs font-medium hover:bg-[var(--color-hover)] disabled:opacity-50"
          >
            <X size={12} />
            Ignore
          </button>
        </div>
      )}
    </div>
  )
}

function Chip({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[var(--color-hover)] px-2 py-0.5 text-xs text-[var(--color-text-muted)]">
      {children}
    </span>
  )
}
