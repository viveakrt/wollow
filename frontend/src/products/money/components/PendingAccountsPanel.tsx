import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { UserPlus, Mail } from 'lucide-react'
import { api } from '../api'
import { Card } from './Card'
import { AccountFormModal } from './AccountFormModal'
import { ACCOUNT_TYPE_LABELS } from '../types'
import type { Account, PendingAccountGroup } from '../types'

/**
 * Mail a parser read but whose account nobody has registered, grouped by the
 * account it names. Adding the account releases the held mail for the next
 * sync — nothing is ever created behind the user's back.
 */
export function PendingAccountsPanel() {
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState<Partial<Account> | null>(null)
  const [released, setReleased] = useState<string | null>(null)

  const pending = useQuery({ queryKey: ['money', 'pending-accounts'], queryFn: api.accounts.pending })
  const groups = pending.data ?? []
  if (groups.length === 0) return null

  function prefill(g: PendingAccountGroup): Partial<Account> {
    const label = g.name || g.issuer || 'Account'
    return {
      name: g.last4 ? `${label} •• ${g.last4}` : label,
      bank: g.issuer,
      accountType: g.kind || 'bank',
      accountNumber: g.last4,
    }
  }

  return (
    <Card className="mb-6 border-[var(--color-tint-orange)]/40">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <UserPlus size={15} className="text-[var(--color-tint-orange)]" />
            Mail waiting for an account
          </h3>
          <p className="mt-1 text-xs text-[var(--color-text-muted)]">
            Your parsers read these emails, but the account they name isn't set up yet. Add it and
            they are imported on the next mail sync.
          </p>
        </div>
        {released && (
          <span className="text-xs text-[var(--color-positive)]">
            {released} added — its mail is imported on the next sync (
            <Link to="/money/settings" className="underline">
              sync now
            </Link>
            ).
          </span>
        )}
      </div>
      <div className="divide-y divide-[var(--color-border)]">
        {groups.map((g) => {
          const label = g.name || g.issuer || 'Unknown sender'
          return (
            <div
              key={`${g.issuer}|${g.last4}|${g.kind}`}
              className="flex flex-wrap items-center justify-between gap-3 py-2.5"
            >
              <div className="min-w-0">
                <div className="text-sm font-medium">
                  {label}
                  {g.last4 && <span className="text-[var(--color-text-muted)]"> •• {g.last4}</span>}
                  <span className="ml-2 rounded-full bg-[var(--color-hover)] px-2 py-0.5 text-[11px] font-normal text-[var(--color-text-muted)]">
                    {ACCOUNT_TYPE_LABELS[g.kind] ?? g.kind}
                  </span>
                </div>
                <div className="truncate text-xs text-[var(--color-text-muted)]">
                  {g.count} email{g.count !== 1 ? 's' : ''}
                  {g.latestSubject && ` · latest: ${g.latestSubject}`}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                {g.sample.mailAccountId > 0 && g.sample.uid > 0 && (
                  <Link
                    to={`/mail/messages/${g.sample.mailAccountId}/${g.sample.uid}`}
                    className="flex items-center gap-1.5 rounded-lg border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium hover:bg-[var(--color-hover)]"
                  >
                    <Mail size={13} />
                    View email
                  </Link>
                )}
                <button
                  onClick={() => setAdding(prefill(g))}
                  className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-white hover:bg-[var(--color-accent-hover)]"
                >
                  <UserPlus size={13} />
                  Add account
                </button>
              </div>
            </div>
          )
        })}
      </div>

      {adding && (
        <AccountFormModal
          account={null}
          initial={adding}
          onClose={() => setAdding(null)}
          onSaved={() => {
            setReleased(adding.name ?? 'Account')
            setAdding(null)
            queryClient.invalidateQueries({ queryKey: ['money'] })
          }}
        />
      )}
    </Card>
  )
}
