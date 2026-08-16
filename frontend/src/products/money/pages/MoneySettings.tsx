import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Mail,
  RefreshCw,
  Loader2,
  CheckCircle2,
  AlertCircle,
  KeyRound,
  ArrowRight,
  History,
} from 'lucide-react'
import { api } from '../api'
import { Card } from '../components/Card'
import type { SyncResult, RescanResult } from '../types'

const KNOWN_ISSUERS = ['HDFC', 'ICICI', 'Axis', 'BOBCARD']

export function MoneySettings() {
  const queryClient = useQueryClient()
  const [lastSync, setLastSync] = useState<{ id: number; result: SyncResult } | null>(null)
  const [lastRescan, setLastRescan] = useState<{ id: number; result: RescanResult } | null>(null)
  const [pdfIssuer, setPdfIssuer] = useState(KNOWN_ISSUERS[0])
  const [pdfPassword, setPdfPassword] = useState('')
  const [parseResult, setParseResult] = useState<{ parsed: number; failed: number } | null>(null)

  const mailboxes = useQuery({
    queryKey: ['money', 'email-accounts'],
    queryFn: api.emailAccounts.list,
  })
  const passwords = useQuery({
    queryKey: ['money', 'pdf-passwords'],
    queryFn: api.pdfPasswords.list,
  })

  const syncMutation = useMutation({
    mutationFn: api.emailAccounts.sync,
    onSuccess: (result, id) => {
      setLastSync({ id, result })
      queryClient.invalidateQueries({ queryKey: ['money'] })
    },
  })

  const rescanMutation = useMutation({
    mutationFn: api.emailAccounts.rescan,
    onSuccess: (result, id) => {
      setLastRescan({ id, result })
      queryClient.invalidateQueries({ queryKey: ['money'] })
    },
  })

  const savePassword = useMutation({
    mutationFn: () => api.pdfPasswords.set(pdfIssuer, pdfPassword),
    onSuccess: () => {
      setPdfPassword('')
      queryClient.invalidateQueries({ queryKey: ['money', 'pdf-passwords'] })
    },
  })

  const parsePending = useMutation({
    mutationFn: api.pdfPasswords.parsePending,
    onSuccess: setParseResult,
  })

  const error = syncMutation.error ?? rescanMutation.error ?? savePassword.error ?? parsePending.error

  return (
    <div className="mx-auto max-w-3xl p-8">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Money settings</h1>
        <p className="mt-1 text-sm text-[var(--color-text-muted)]">
          Scan connected mailboxes for bank and card alerts, and unlock statement PDFs.
        </p>
      </div>

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]">
          <AlertCircle size={14} />
          {error instanceof Error ? error.message : 'Something went wrong'}
        </div>
      )}

      <Card title="Mailboxes">
        <p className="mb-4 text-sm text-[var(--color-text-muted)]">
          Mailboxes are connected once in Mail and shared by both products — Money reads bank and
          card alerts out of the same inbox, and only from known issuer senders.
        </p>

        {mailboxes.isPending ? (
          <p className="text-sm text-[var(--color-text-muted)]">Loading…</p>
        ) : mailboxes.data && mailboxes.data.length > 0 ? (
          <div className="flex flex-col gap-3">
            {mailboxes.data.map((account) => (
              <div
                key={account.id}
                className="flex items-center justify-between rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-3"
              >
                <div className="min-w-0">
                  <div className="truncate font-medium">{account.email}</div>
                  <div className="text-xs text-[var(--color-text-muted)]">
                    {account.lastSyncedAt
                      ? `Last scanned ${new Date(account.lastSyncedAt).toLocaleString()}`
                      : 'Never scanned for transactions'}
                  </div>
                  {lastSync?.id === account.id && (
                    <>
                      <div className="mt-1 flex items-center gap-1.5 text-xs text-[var(--color-positive)]">
                        <CheckCircle2 size={12} />
                        Found {lastSync.result.transactions} transaction
                        {lastSync.result.transactions !== 1 ? 's' : ''}, {lastSync.result.bills} bill
                        {lastSync.result.bills !== 1 ? 's' : ''} ({lastSync.result.scanned} emails scanned)
                      </div>
                      {/* This used to be invisible, which is how a stalled
                          import looked exactly like a finished one. */}
                      {lastSync.result.failed > 0 && (
                        <div className="mt-1 text-xs text-[var(--color-tint-orange)]">
                          {lastSync.result.failed} could not be read this time; they'll be retried.
                        </div>
                      )}
                    </>
                  )}
                  {lastRescan?.id === account.id && (
                    <div className="mt-1 flex items-center gap-1.5 text-xs text-[var(--color-positive)]">
                      <CheckCircle2 size={12} />
                      {lastRescan.result.cleared === 0
                        ? 'Nothing stuck — every message already has its transaction, bill, or trade.'
                        : `Recovered ${lastRescan.result.transactions} transaction${lastRescan.result.transactions !== 1 ? 's' : ''}, ${lastRescan.result.bills} bill${lastRescan.result.bills !== 1 ? 's' : ''} from ${lastRescan.result.cleared} stuck message${lastRescan.result.cleared !== 1 ? 's' : ''}.`}
                    </div>
                  )}
                </div>
                <div className="ml-3 flex shrink-0 items-center gap-2">
                  <button
                    type="button"
                    onClick={() => rescanMutation.mutate(account.id)}
                    disabled={rescanMutation.isPending && rescanMutation.variables === account.id}
                    title="Recover transactions, bills, and trades left stranded by a deleted account or holding, and retry mail that couldn't be read the first time"
                    className="flex items-center gap-1.5 rounded-lg border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium transition-colors hover:bg-[var(--color-hover)] disabled:opacity-50"
                  >
                    {rescanMutation.isPending && rescanMutation.variables === account.id ? (
                      <Loader2 size={13} className="animate-spin" />
                    ) : (
                      <History size={13} />
                    )}
                    Rescan
                  </button>
                  <button
                    type="button"
                    onClick={() => syncMutation.mutate(account.id)}
                    disabled={syncMutation.isPending && syncMutation.variables === account.id}
                    className="flex items-center gap-1.5 rounded-lg border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium transition-colors hover:bg-[var(--color-hover)] disabled:opacity-50"
                  >
                    <RefreshCw
                      size={13}
                      className={
                        syncMutation.isPending && syncMutation.variables === account.id
                          ? 'animate-spin'
                          : ''
                      }
                    />
                    Scan now
                  </button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="flex flex-col items-center py-8 text-center">
            <Mail size={32} className="mb-3 text-[var(--color-text-muted)]" />
            <p className="mb-4 text-sm text-[var(--color-text-muted)]">No mailboxes connected yet.</p>
            <Link
              to="/mail/accounts/new"
              className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)]"
            >
              Connect one in Mail
              <ArrowRight size={14} />
            </Link>
          </div>
        )}
      </Card>

      <Card title="Credit card statement passwords" className="mt-6">
        <p className="mb-4 text-sm text-[var(--color-text-muted)]">
          Card statement PDFs are usually locked with a fixed formula per issuer (e.g. name + date
          of birth). Enter it once per issuer to unlock itemized transactions from bill emails
          automatically. Passwords are encrypted at rest.
        </p>

        <div className="mb-4 flex flex-col gap-3 sm:flex-row">
          <select
            value={pdfIssuer}
            onChange={(e) => setPdfIssuer(e.target.value)}
            className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm focus:border-[var(--color-accent)] focus:outline-none"
          >
            {KNOWN_ISSUERS.map((issuer) => (
              <option key={issuer} value={issuer}>
                {issuer}
              </option>
            ))}
          </select>
          <input
            value={pdfPassword}
            onChange={(e) => setPdfPassword(e.target.value)}
            placeholder="PDF password"
            type="password"
            className="flex-1 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm focus:border-[var(--color-accent)] focus:outline-none"
          />
          <button
            type="button"
            onClick={() => savePassword.mutate()}
            disabled={savePassword.isPending || !pdfPassword}
            className="flex items-center justify-center gap-2 whitespace-nowrap rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)] disabled:opacity-50"
          >
            {savePassword.isPending && <Loader2 size={14} className="animate-spin" />}
            Save
          </button>
        </div>

        {passwords.data && passwords.data.length > 0 && (
          <div className="mb-4 flex flex-wrap gap-2">
            {passwords.data.map((p) => (
              <span
                key={p.id}
                className="flex items-center gap-1.5 rounded-full bg-[var(--color-hover)] px-2.5 py-1 text-xs text-[var(--color-text-muted)]"
              >
                <KeyRound size={11} />
                {p.issuer} configured
              </span>
            ))}
          </div>
        )}

        <div className="flex items-center gap-3 border-t border-[var(--color-border)] pt-3">
          <button
            type="button"
            onClick={() => parsePending.mutate()}
            disabled={parsePending.isPending}
            className="flex items-center gap-1.5 rounded-lg border border-[var(--color-border)] px-3 py-1.5 text-xs font-medium transition-colors hover:bg-[var(--color-hover)] disabled:opacity-50"
          >
            <RefreshCw size={13} className={parsePending.isPending ? 'animate-spin' : ''} />
            Retry pending statement PDFs
          </button>
          {parseResult && (
            <span className="text-xs text-[var(--color-text-muted)]">
              {parseResult.parsed} unlocked, {parseResult.failed} still pending
            </span>
          )}
        </div>
      </Card>
    </div>
  )
}
