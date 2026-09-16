import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Receipt, Check, Undo2, Loader2, AlertCircle } from 'lucide-react'
import { api } from '../api'
import { formatDate, formatINR } from '../lib/format'
import { Card } from '../components/Card'
import type { Bill } from '../types'

/** Soonest due first; bills without a parsed due date last. */
function byDueDate(a: Bill, b: Bill): number {
  if (!a.dueDate !== !b.dueDate) return a.dueDate ? -1 : 1
  return a.dueDate.localeCompare(b.dueDate)
}

export function Bills() {
  const queryClient = useQueryClient()
  const { data: bills = [], isPending: loading } = useQuery({
    queryKey: ['money', 'bills'],
    queryFn: api.bills.list,
  })

  // The dashboard's upcoming bills are the unpaid ones, so it is stale too.
  const invalidateMoney = () => queryClient.invalidateQueries({ queryKey: ['money'] })
  const markPaid = useMutation({ mutationFn: (id: number) => api.bills.markPaid(id), onSuccess: invalidateMoney })
  const markUnpaid = useMutation({ mutationFn: (id: number) => api.bills.markUnpaid(id), onSuccess: invalidateMoney })

  const busyId = markPaid.isPending ? markPaid.variables : markUnpaid.isPending ? markUnpaid.variables : null
  const error = markPaid.error ?? markUnpaid.error

  const unpaid = bills.filter((b) => b.status !== 'paid').sort(byDueDate)
  const paid = bills.filter((b) => b.status === 'paid').sort((a, b) => b.paidAt.localeCompare(a.paidAt))

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Bills</h1>
        <p className="text-[var(--color-text-muted)] text-sm mt-1">
          Credit card statements read from your mail. Mark a bill paid once you've paid it — the
          dashboard only lists unpaid ones.
        </p>
      </div>

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]">
          <AlertCircle size={14} />
          {error instanceof Error ? error.message : 'Something went wrong'}
        </div>
      )}

      {loading ? (
        <p className="text-[var(--color-text-muted)]">Loading…</p>
      ) : bills.length === 0 ? (
        <Card className="flex flex-col items-center py-16">
          <Receipt size={36} className="text-[var(--color-text-muted)] mb-4" />
          <h2 className="text-lg font-semibold mb-1">No bills yet</h2>
          <p className="text-sm text-[var(--color-text-muted)] text-center max-w-sm">
            Bills come from your card statement emails. Define a “Card statement (bill)” parser on
            one of them and every statement after it shows up here.
          </p>
          <Link
            to="/money/parsers/new"
            className="mt-4 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-accent-hover)]"
          >
            Define a parser
          </Link>
        </Card>
      ) : (
        <div className="space-y-8">
          <section>
            <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
              Unpaid ({unpaid.length})
            </h2>
            {unpaid.length === 0 ? (
              <p className="text-sm text-[var(--color-text-muted)]">Nothing unpaid.</p>
            ) : (
              <div className="space-y-3">
                {unpaid.map((b) => (
                  <BillCard key={b.id} bill={b} busy={busyId === b.id} onToggle={() => markPaid.mutate(b.id)} />
                ))}
              </div>
            )}
          </section>

          {paid.length > 0 && (
            <section>
              <h2 className="mb-3 text-xs font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
                Paid
              </h2>
              <div className="space-y-3">
                {paid.map((b) => (
                  <BillCard key={b.id} bill={b} busy={busyId === b.id} onToggle={() => markUnpaid.mutate(b.id)} />
                ))}
              </div>
            </section>
          )}
        </div>
      )}
    </div>
  )
}

function BillCard({ bill, busy, onToggle }: { bill: Bill; busy: boolean; onToggle: () => void }) {
  const isPaid = bill.status === 'paid'
  return (
    <Card className={isPaid ? 'opacity-75' : ''}>
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <div className="font-medium">
            {bill.accountName || bill.issuer}
            {bill.cardLast4 && <span className="text-[var(--color-text-muted)]"> •• {bill.cardLast4}</span>}
          </div>
          {bill.statementPeriod && (
            <div className="text-sm text-[var(--color-text-muted)]">{bill.statementPeriod}</div>
          )}
          <div className="text-xs text-[var(--color-text-muted)] mt-1">
            {isPaid
              ? `Paid ${formatDate(bill.paidAt)}`
              : bill.dueDate
                ? `Due ${formatDate(bill.dueDate)}`
                : 'No due date in the email'}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-4">
          <div className="text-right">
            {bill.totalDue != null ? (
              <div className="text-lg font-semibold">{formatINR(bill.totalDue)}</div>
            ) : (
              <div className="text-sm text-[var(--color-text-muted)]">Amount in statement PDF</div>
            )}
            {bill.minimumDue != null && (
              <div className="text-xs text-[var(--color-text-muted)]">Min due {formatINR(bill.minimumDue)}</div>
            )}
          </div>
          <button
            onClick={onToggle}
            disabled={busy}
            className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium disabled:opacity-50 ${
              isPaid
                ? 'border border-[var(--color-border)] hover:bg-[var(--color-hover)]'
                : 'bg-[var(--color-accent)] text-white hover:bg-[var(--color-accent-hover)]'
            }`}
          >
            {busy ? <Loader2 size={13} className="animate-spin" /> : isPaid ? <Undo2 size={13} /> : <Check size={13} />}
            {isPaid ? 'Mark unpaid' : 'Mark paid'}
          </button>
        </div>
      </div>
    </Card>
  )
}
