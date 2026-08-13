import { useEffect, useState } from 'react'
import { Receipt } from 'lucide-react'
import { api } from '../lib/api'
import { formatINR } from '../lib/format'
import { Card } from '../components/Card'
import type { Bill } from '../lib/types'

export function Bills() {
  const [bills, setBills] = useState<Bill[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.bills.list().then(setBills).finally(() => setLoading(false))
  }, [])

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Bills</h1>
        <p className="text-[var(--color-text-muted)] text-sm mt-1">
          Credit card statements detected from your connected email.
        </p>
      </div>

      {loading ? (
        <p className="text-[var(--color-text-muted)]">Loading…</p>
      ) : bills.length === 0 ? (
        <Card className="flex flex-col items-center py-16">
          <Receipt size={36} className="text-[var(--color-text-muted)] mb-4" />
          <h2 className="text-lg font-semibold mb-1">No bills yet</h2>
          <p className="text-sm text-[var(--color-text-muted)] text-center max-w-sm">
            Connect Gmail in Settings and sync to automatically detect credit card statement
            emails.
          </p>
        </Card>
      ) : (
        <div className="space-y-3">
          {bills.map((b) => (
            <Card key={b.id}>
              <div className="flex items-center justify-between">
                <div>
                  <div className="font-medium">
                    {b.issuer} {b.cardLast4 && `•• ${b.cardLast4}`}
                  </div>
                  <div className="text-sm text-[var(--color-text-muted)]">{b.statementPeriod}</div>
                  {b.dueDate && (
                    <div className="text-xs text-[var(--color-text-muted)] mt-1">Due {b.dueDate}</div>
                  )}
                </div>
                <div className="text-right">
                  {b.totalDue != null ? (
                    <div className="text-lg font-semibold">{formatINR(b.totalDue)}</div>
                  ) : (
                    <div className="text-sm text-[var(--color-text-muted)]">Amount in statement PDF</div>
                  )}
                  {b.minimumDue != null && (
                    <div className="text-xs text-[var(--color-text-muted)]">
                      Min due {formatINR(b.minimumDue)}
                    </div>
                  )}
                  <span
                    className={`inline-block mt-1 text-xs px-2 py-0.5 rounded-full ${
                      b.status === 'paid'
                        ? 'bg-emerald-500/15 text-emerald-400'
                        : 'bg-orange-500/15 text-orange-400'
                    }`}
                  >
                    {b.status}
                  </span>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
