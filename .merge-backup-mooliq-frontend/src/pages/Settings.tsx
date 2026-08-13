import { useEffect, useState } from 'react'
import { Mail, RefreshCw, Trash2, Loader2, CheckCircle2, AlertCircle, ExternalLink, KeyRound } from 'lucide-react'
import { api } from '../lib/api'
import { Card } from '../components/Card'
import type { EmailAccount, SyncResult, PDFPassword } from '../lib/types'

const KNOWN_ISSUERS = ['HDFC', 'ICICI', 'Axis', 'BOBCARD']

export function Settings() {
  const [accounts, setAccounts] = useState<EmailAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [email, setEmail] = useState('')
  const [appPassword, setAppPassword] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [syncingId, setSyncingId] = useState<number | null>(null)
  const [lastSync, setLastSync] = useState<{ id: number; result: SyncResult } | null>(null)

  const [pdfPasswords, setPdfPasswords] = useState<PDFPassword[]>([])
  const [pdfIssuer, setPdfIssuer] = useState(KNOWN_ISSUERS[0])
  const [pdfPassword, setPdfPassword] = useState('')
  const [savingPdfPassword, setSavingPdfPassword] = useState(false)
  const [parsingPending, setParsingPending] = useState(false)
  const [parseResult, setParseResult] = useState<{ parsed: number; failed: number } | null>(null)

  function reload() {
    api.emailAccounts.list().then(setAccounts).finally(() => setLoading(false))
    api.pdfPasswords.list().then(setPdfPasswords)
  }

  useEffect(reload, [])

  async function handleConnect() {
    setError(null)
    if (!email || !appPassword) {
      setError('Email and app password are required')
      return
    }
    setConnecting(true)
    try {
      await api.emailAccounts.connect(email, appPassword)
      setEmail('')
      setAppPassword('')
      reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not connect')
    } finally {
      setConnecting(false)
    }
  }

  async function handleSync(id: number) {
    setSyncingId(id)
    setError(null)
    try {
      const result = await api.emailAccounts.sync(id)
      setLastSync({ id, result })
      reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Sync failed')
    } finally {
      setSyncingId(null)
    }
  }

  async function handleDelete(id: number) {
    await api.emailAccounts.delete(id)
    reload()
  }

  async function handleSavePdfPassword() {
    if (!pdfPassword) return
    setSavingPdfPassword(true)
    try {
      await api.pdfPasswords.set(pdfIssuer, pdfPassword)
      setPdfPassword('')
      reload()
    } finally {
      setSavingPdfPassword(false)
    }
  }

  async function handleParsePending() {
    setParsingPending(true)
    try {
      const result = await api.pdfPasswords.parsePending()
      setParseResult(result)
    } finally {
      setParsingPending(false)
    }
  }

  return (
    <div className="p-8 max-w-3xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-[var(--color-text-muted)] text-sm mt-1">
          Connect Gmail to automatically pull transaction alerts and credit card bills.
        </p>
      </div>

      <Card title="Connect Gmail" className="mb-6">
        <p className="text-sm text-[var(--color-text-muted)] mb-4">
          Uses an IMAP{' '}
          <a
            href="https://myaccount.google.com/apppasswords"
            target="_blank"
            rel="noreferrer"
            className="text-[var(--color-accent-2)] hover:underline inline-flex items-center gap-1"
          >
            App Password <ExternalLink size={12} />
          </a>{' '}
          — not your regular Gmail password. Requires 2-Step Verification to be enabled on your
          Google account. Only emails from known bank/card senders are scanned.
        </p>

        {error && (
          <div className="flex items-center gap-2 px-3 py-2 mb-3 rounded-lg bg-red-500/10 border border-red-500/30 text-sm text-red-400">
            <AlertCircle size={14} />
            {error}
          </div>
        )}

        <div className="flex flex-col gap-3 sm:flex-row">
          <input
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@gmail.com"
            type="email"
            className="flex-1 px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
          />
          <input
            value={appPassword}
            onChange={(e) => setAppPassword(e.target.value)}
            placeholder="16-character app password"
            type="password"
            className="flex-1 px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
          />
          <button
            onClick={handleConnect}
            disabled={connecting}
            className="flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 disabled:opacity-50 transition-colors whitespace-nowrap"
          >
            {connecting && <Loader2 size={14} className="animate-spin" />}
            Connect
          </button>
        </div>
      </Card>

      <Card title="Connected Accounts">
        {loading ? (
          <p className="text-sm text-[var(--color-text-muted)]">Loading…</p>
        ) : accounts.length === 0 ? (
          <div className="flex flex-col items-center py-8 text-center">
            <Mail size={32} className="text-[var(--color-text-muted)] mb-3" />
            <p className="text-sm text-[var(--color-text-muted)]">No email accounts connected yet.</p>
          </div>
        ) : (
          <div className="space-y-3">
            {accounts.map((a) => (
              <div
                key={a.id}
                className="flex items-center justify-between px-4 py-3 rounded-lg bg-[var(--color-surface-2)] border border-[var(--color-border)]"
              >
                <div className="min-w-0">
                  <div className="font-medium truncate">{a.email}</div>
                  <div className="text-xs text-[var(--color-text-muted)]">
                    {a.lastSyncedAt ? `Last synced ${new Date(a.lastSyncedAt).toLocaleString()}` : 'Never synced'}
                  </div>
                  {lastSync?.id === a.id && (
                    <div className="flex items-center gap-1.5 text-xs text-emerald-400 mt-1">
                      <CheckCircle2 size={12} />
                      Found {lastSync.result.transactions} transaction
                      {lastSync.result.transactions !== 1 ? 's' : ''}, {lastSync.result.bills} bill
                      {lastSync.result.bills !== 1 ? 's' : ''} ({lastSync.result.scanned} emails scanned)
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0 ml-3">
                  <button
                    onClick={() => handleSync(a.id)}
                    disabled={syncingId === a.id}
                    className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-border)] text-xs font-medium hover:bg-white/5 disabled:opacity-50"
                  >
                    <RefreshCw size={13} className={syncingId === a.id ? 'animate-spin' : ''} />
                    Sync now
                  </button>
                  <button
                    onClick={() => handleDelete(a.id)}
                    className="p-1.5 rounded-lg border border-[var(--color-border)] hover:bg-red-500/10 hover:border-red-500/30 hover:text-red-400"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Card title="Credit Card Statement Passwords" className="mt-6">
        <p className="text-sm text-[var(--color-text-muted)] mb-4">
          Card statement PDFs are usually locked with a fixed formula per issuer (e.g. name +
          date of birth). Enter it once per issuer to unlock itemized transactions from bill
          emails automatically.
        </p>

        <div className="flex flex-col gap-3 sm:flex-row mb-4">
          <select
            value={pdfIssuer}
            onChange={(e) => setPdfIssuer(e.target.value)}
            className="px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
          >
            {KNOWN_ISSUERS.map((i) => (
              <option key={i} value={i}>
                {i}
              </option>
            ))}
          </select>
          <input
            value={pdfPassword}
            onChange={(e) => setPdfPassword(e.target.value)}
            placeholder="PDF password"
            type="password"
            className="flex-1 px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
          />
          <button
            onClick={handleSavePdfPassword}
            disabled={savingPdfPassword || !pdfPassword}
            className="flex items-center justify-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 disabled:opacity-50 transition-colors whitespace-nowrap"
          >
            {savingPdfPassword && <Loader2 size={14} className="animate-spin" />}
            Save
          </button>
        </div>

        {pdfPasswords.length > 0 && (
          <div className="flex flex-wrap gap-2 mb-4">
            {pdfPasswords.map((p) => (
              <span
                key={p.id}
                className="flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs bg-white/5 text-[var(--color-text-muted)]"
              >
                <KeyRound size={11} />
                {p.issuer} configured
              </span>
            ))}
          </div>
        )}

        <div className="flex items-center gap-3 pt-3 border-t border-[var(--color-border)]">
          <button
            onClick={handleParsePending}
            disabled={parsingPending}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-[var(--color-border)] text-xs font-medium hover:bg-white/5 disabled:opacity-50"
          >
            <RefreshCw size={13} className={parsingPending ? 'animate-spin' : ''} />
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
