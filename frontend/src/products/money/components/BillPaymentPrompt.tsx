import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Check, Loader2, Receipt, X } from 'lucide-react'
import { api } from '../api'
import { formatDate, formatINR } from '../lib/format'
import type { BillCoverage, BillPaymentSuggestion } from '../types'

const COVERAGE: Record<BillCoverage, { label: string; tone: string }> = {
  full: {
    label: 'Covers the total due',
    tone: 'bg-[var(--color-positive-tint)] text-[var(--color-positive)]',
  },
  minimum: {
    label: 'Covers the minimum due',
    tone: 'bg-[var(--color-tint-orange-bg)] text-[var(--color-tint-orange)]',
  },
  partial: {
    label: 'Less than the minimum due',
    tone: 'bg-[var(--color-negative-tint)] text-[var(--color-negative)]',
  },
  unknown: {
    label: 'Statement amount unknown',
    tone: 'bg-[var(--color-hover)] text-[var(--color-text-muted)]',
  },
}

/** Asks whether a payment just linked into a card settled one of its unpaid bills. */
export function BillPaymentPrompt({
  suggestion,
  onClose,
}: {
  suggestion: BillPaymentSuggestion
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [paid, setPaid] = useState<Set<number>>(new Set())
  const [busyId, setBusyId] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function markPaid(id: number) {
    setBusyId(id)
    setError(null)
    try {
      await api.bills.markPaid(id, suggestion.date)
      setPaid((prev) => new Set(prev).add(id))
      queryClient.invalidateQueries({ queryKey: ['money'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not mark the bill paid')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div
        role="dialog"
        aria-label="Mark a bill as paid"
        className="w-full max-w-lg rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            <Receipt size={18} className="text-[var(--color-tint-orange)]" />
            Did this pay a bill?
          </h2>
          <button onClick={onClose} className="text-[var(--color-text-muted)] hover:text-[var(--color-text)]">
            <X size={18} />
          </button>
        </div>
        <p className="mb-4 text-sm text-[var(--color-text-muted)]">
          {formatINR(suggestion.amount)} was paid to{' '}
          <span className="font-medium text-[var(--color-text)]">{suggestion.accountName}</span> on{' '}
          {formatDate(suggestion.date)}. Mark the bill it settled as paid — the card balance already
          reflects the payment.
        </p>

        {error && (
          <div className="mb-3 rounded-lg border border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]">
            {error}
          </div>
        )}

        <div className="space-y-2">
          {suggestion.bills.map((b) => {
            const coverage = COVERAGE[b.coverage] ?? COVERAGE.unknown
            return (
              <div
                key={b.id}
                className="flex items-start justify-between gap-3 rounded-lg border border-[var(--color-border)] px-4 py-3"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">
                    {b.statementPeriod || `${b.issuer}${b.cardLast4 ? ` •• ${b.cardLast4}` : ''}`}
                  </div>
                  <div className="text-xs text-[var(--color-text-muted)]">
                    {b.dueDate ? `Due ${formatDate(b.dueDate)}` : 'No due date'}
                    {b.totalDue != null && ` · Total ${formatINR(b.totalDue)}`}
                    {b.minimumDue != null && ` · Min ${formatINR(b.minimumDue)}`}
                  </div>
                  <span className={`mt-1 inline-block rounded-full px-2 py-0.5 text-[11px] ${coverage.tone}`}>
                    {coverage.label}
                  </span>
                </div>
                {paid.has(b.id) ? (
                  <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-[var(--color-positive)]">
                    <Check size={13} />
                    Paid
                  </span>
                ) : (
                  <button
                    onClick={() => markPaid(b.id)}
                    disabled={busyId != null}
                    className="flex shrink-0 items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-white hover:bg-[var(--color-accent-hover)] disabled:opacity-50"
                  >
                    {busyId === b.id ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}
                    Mark paid
                  </button>
                )}
              </div>
            )
          })}
        </div>

        <div className="mt-5 flex justify-end">
          <button
            onClick={onClose}
            className="rounded-lg border border-[var(--color-border)] px-4 py-2 text-sm font-medium hover:bg-[var(--color-hover)]"
          >
            {paid.size > 0 ? 'Done' : 'Not now'}
          </button>
        </div>
      </div>
    </div>
  )
}
