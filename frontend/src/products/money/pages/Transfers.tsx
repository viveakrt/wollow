import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeftRight, Check, X, RefreshCw, Loader2 } from 'lucide-react'
import { api } from '../api'
import { formatINR, formatDate } from '../lib/format'
import { Card } from '../components/Card'

export function Transfers() {
  const queryClient = useQueryClient()

  const { data: suggestions = [], isPending: loading } = useQuery({
    queryKey: ['money', 'transfer-suggestions'],
    queryFn: api.transferSuggestions.list,
  })

  // Confirming a transfer rewrites both legs' linked_txn_id, so the whole money
  // cache is stale afterwards, not just the suggestion list.
  const invalidateMoney = () => queryClient.invalidateQueries({ queryKey: ['money'] })

  const scanMutation = useMutation({
    mutationFn: api.transferSuggestions.scan,
    onSuccess: invalidateMoney,
  })
  const confirmMutation = useMutation({
    mutationFn: api.transferSuggestions.confirm,
    onSuccess: invalidateMoney,
  })
  const dismissMutation = useMutation({
    mutationFn: api.transferSuggestions.dismiss,
    onSuccess: invalidateMoney,
  })

  const scanning = scanMutation.isPending
  const busyId =
    (confirmMutation.isPending ? confirmMutation.variables : null) ??
    (dismissMutation.isPending ? dismissMutation.variables : null)

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Transfers</h1>
          <p className="text-[var(--color-text-muted)] text-sm mt-1">
            Money moving between your own accounts — like a bank debit that pays off a credit
            card — shouldn't count as spending twice. Confirm matches below to link them.
          </p>
        </div>
        <button
          onClick={() => scanMutation.mutate()}
          disabled={scanning}
          className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] disabled:opacity-50 transition-colors whitespace-nowrap"
        >
          {scanning ? <Loader2 size={16} className="animate-spin" /> : <RefreshCw size={16} />}
          Scan for transfers
        </button>
      </div>

      {loading ? (
        <p className="text-[var(--color-text-muted)]">Loading…</p>
      ) : suggestions.length === 0 ? (
        <Card className="flex flex-col items-center py-16">
          <ArrowLeftRight size={36} className="text-[var(--color-text-muted)] mb-4" />
          <h2 className="text-lg font-semibold mb-1">No transfer suggestions</h2>
          <p className="text-sm text-[var(--color-text-muted)] text-center max-w-sm">
            Click "Scan for transfers" to look for matching expense/income pairs across your
            accounts.
          </p>
        </Card>
      ) : (
        <div className="space-y-3">
          {suggestions.map((s) => (
            <Card key={s.id}>
              <div className="flex items-center gap-4">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 text-xs text-[var(--color-text-muted)] mb-1">
                    <span>{s.txnA.accountName}</span>
                    <ArrowLeftRight size={12} />
                    <span>{s.txnB.accountName}</span>
                    <span className="ml-auto">{Math.round(s.confidence * 100)}% match</span>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <div className="text-sm font-medium truncate">
                        {s.txnA.merchant || s.txnA.narration}
                      </div>
                      <div className="text-xs text-[var(--color-text-muted)]">
                        {formatDate(s.txnA.txnDate)} · -{formatINR(s.txnA.withdrawalAmt)}
                      </div>
                    </div>
                    <div>
                      <div className="text-sm font-medium truncate">
                        {s.txnB.merchant || s.txnB.narration}
                      </div>
                      <div className="text-xs text-[var(--color-text-muted)]">
                        {formatDate(s.txnB.txnDate)} · +{formatINR(s.txnB.depositAmt)}
                      </div>
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <button
                    onClick={() => confirmMutation.mutate(s.id)}
                    disabled={busyId === s.id}
                    className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[var(--color-positive-tint)] text-[var(--color-positive)] text-xs font-medium hover:opacity-80 disabled:opacity-50"
                  >
                    <Check size={13} />
                    Confirm
                  </button>
                  <button
                    onClick={() => dismissMutation.mutate(s.id)}
                    disabled={busyId === s.id}
                    className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-border)] text-xs font-medium hover:bg-[var(--color-hover)] disabled:opacity-50"
                  >
                    <X size={13} />
                    Not a match
                  </button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
