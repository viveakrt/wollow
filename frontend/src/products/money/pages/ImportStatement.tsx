import { useState, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Upload, FileSpreadsheet, CheckCircle2, AlertCircle, Loader2 } from 'lucide-react'
import { api } from '../api'
import { formatINR, formatDate } from '../lib/format'
import { Card } from '../components/Card'
import { ACCOUNT_TYPE_LABELS } from '../types'
import type { ImportPreview } from '../types'

type Step = 'upload' | 'review' | 'done'

// The upload -> review -> done flow is a local state machine rather than server
// state, so the wizard keeps useState; only the account list and the
// post-commit cache invalidation go through the query client.
export function ImportStatement() {
  const queryClient = useQueryClient()
  const [step, setStep] = useState<Step>('upload')
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [preview, setPreview] = useState<ImportPreview | null>(null)
  const [selectedAccountId, setSelectedAccountId] = useState<string>('')
  const [newAccountName, setNewAccountName] = useState('')
  const [newAccountType, setNewAccountType] = useState('bank')
  const [committing, setCommitting] = useState(false)
  const [result, setResult] = useState<{ importedRows: number; duplicateRows: number; noun: string } | null>(
    null,
  )
  const fileInputRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()

  const { data: accounts = [] } = useQuery({
    queryKey: ['money', 'accounts'],
    queryFn: api.accounts.list,
  })

  async function handleFile(file: File) {
    setError(null)
    setUploading(true)
    try {
      const p = await api.import.preview(file)
      setPreview(p)
      setSelectedAccountId(p.suggestedAccount ? String(p.suggestedAccount.id) : '')
      if (!p.suggestedAccount) {
        setNewAccountName(`${p.bank} •• ${p.accountNumber.slice(-4)}`)
      }
      // The parser reads a PPF passbook out of the same template as a savings
      // export; its guess seeds the picker and the user can override it.
      setNewAccountType(p.accountType || 'bank')
      setStep('review')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to parse file')
    } finally {
      setUploading(false)
    }
  }

  async function handleCommit() {
    if (!preview) return
    setCommitting(true)
    setError(null)
    try {
      // A deposit summary has no account to import into and no transactions to
      // dedupe — it replaces the holdings it names outright.
      if (preview.kind === 'deposits') {
        const res = await api.import.depositsCommit(preview.fileName, preview.deposits ?? [])
        setResult({ importedRows: res.imported, duplicateRows: res.updated, noun: 'holding' })
        setStep('done')
        queryClient.invalidateQueries({ queryKey: ['money'] })
        return
      }

      const payload: Record<string, unknown> = {
        fileName: preview.fileName,
        // Sent even when importing into an existing account: an account added
        // by hand usually knows only its masked tail, and this is where it
        // learns the full number.
        accountNumber: preview.accountNumber,
        transactions: preview.transactions,
      }
      if (selectedAccountId) {
        payload.accountId = Number(selectedAccountId)
      } else {
        payload.newAccount = {
          name: newAccountName || `${preview.bank} •• ${preview.accountNumber.slice(-4)}`,
          bank: preview.bank,
          accountType: newAccountType,
          accountNumber: preview.accountNumber,
          ifsc: preview.ifsc,
          branch: preview.accountBranch,
          openingBalance: preview.openingBalance,
        }
      }
      const res = await api.import.hdfcCommit(payload)
      setResult({ ...res, noun: 'transaction' })
      setStep('done')
      // A commit creates transactions and possibly a new account, so the
      // dashboard, account list and transaction list are all stale.
      queryClient.invalidateQueries({ queryKey: ['money'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to import')
    } finally {
      setCommitting(false)
    }
  }

  const isDeposits = preview?.kind === 'deposits'

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Import Statement</h1>
        <p className="text-[var(--color-text-muted)] text-sm mt-1">
          Upload an HDFC Bank .xls export — an account or PPF statement becomes transactions, a
          fixed-deposit summary becomes holdings. Which is which is worked out from the file.
        </p>
      </div>

      <StepIndicator step={step} />

      {error && (
        <div className="mt-5 flex items-center gap-2 px-4 py-3 rounded-lg bg-[var(--color-negative-tint)] border border-[var(--color-negative)] text-sm text-[var(--color-negative)]">
          <AlertCircle size={16} />
          {error}
        </div>
      )}

      {step === 'upload' && (
        <Card className="mt-5">
          <div
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault()
              const file = e.dataTransfer.files?.[0]
              if (file) handleFile(file)
            }}
            onClick={() => fileInputRef.current?.click()}
            className="border-2 border-dashed border-[var(--color-border)] rounded-xl py-16 flex flex-col items-center justify-center cursor-pointer hover:border-[var(--color-accent)] transition-colors"
          >
            {uploading ? (
              <>
                <Loader2 size={36} className="text-[var(--color-accent)] animate-spin mb-4" />
                <p className="text-sm text-[var(--color-text-muted)]">Parsing statement…</p>
              </>
            ) : (
              <>
                <Upload size={36} className="text-[var(--color-text-muted)] mb-4" />
                <p className="font-medium mb-1">Drag &amp; drop your statement here</p>
                <p className="text-sm text-[var(--color-text-muted)]">
                  or click to browse — HDFC account, PPF and FD summary .xls exports
                </p>
              </>
            )}
            <input
              ref={fileInputRef}
              type="file"
              accept=".xls"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0]
                if (file) handleFile(file)
              }}
            />
          </div>
        </Card>
      )}

      {step === 'review' && preview && isDeposits && (
        <div className="mt-5 space-y-5">
          <Card>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              <SummaryStat label="File" value={preview.bank + ' deposits'} />
              <SummaryStat label="Deposits Found" value={String(preview.totalRows)} />
              <SummaryStat label="New" value={String(preview.newRows)} positive />
              <SummaryStat label="Already Tracked" value={String(preview.duplicateRows)} muted />
            </div>
            <p className="mt-4 border-t border-[var(--color-border)] pt-4 text-sm text-[var(--color-text-muted)]">
              Deposits already tracked are refreshed rather than duplicated — a summary export is a
              snapshot of the whole portfolio, so re-importing a newer one is how principal and
              maturity figures stay current.
            </p>
          </Card>

          <Card title={`Preview (${preview.deposits?.length ?? 0} deposits)`}>
            <div className="max-h-96 overflow-y-auto rounded-lg border border-[var(--color-border)]">
              <table className="w-full text-sm">
                <thead className="sticky top-0 bg-[var(--color-surface-2)]">
                  <tr className="text-xs uppercase text-[var(--color-text-muted)]">
                    <th className="px-3 py-2 text-left font-medium">Account</th>
                    <th className="px-3 py-2 text-right font-medium">Principal</th>
                    <th className="px-3 py-2 text-right font-medium">At maturity</th>
                    <th className="px-3 py-2 text-right font-medium">Rate</th>
                    <th className="px-3 py-2 text-left font-medium">Matures</th>
                    <th className="w-20 px-3 py-2 text-left font-medium">Status</th>
                  </tr>
                </thead>
                <tbody>
                  {(preview.deposits ?? []).map((d) => (
                    <tr key={d.dedupeKey} className="border-t border-[var(--color-border)]">
                      <td className="px-3 py-2">
                        <div>{d.identifier}</div>
                        <div className="text-xs text-[var(--color-text-muted)]">{d.branch}</div>
                      </td>
                      <td className="px-3 py-2 text-right">{formatINR(d.investedAmount)}</td>
                      <td className="px-3 py-2 text-right">{formatINR(d.maturityAmount)}</td>
                      <td className="px-3 py-2 text-right text-[var(--color-text-muted)]">
                        {d.interestRate}%
                      </td>
                      <td className="px-3 py-2 whitespace-nowrap text-[var(--color-text-muted)]">
                        {formatDate(d.maturityDate)}
                      </td>
                      <td className="px-3 py-2">
                        {d.isDuplicate ? (
                          <span className="rounded-full bg-[var(--color-hover)] px-2 py-0.5 text-xs text-[var(--color-text-muted)]">
                            Update
                          </span>
                        ) : (
                          <span className="rounded-full bg-[var(--color-positive-tint)] px-2 py-0.5 text-xs text-[var(--color-positive)]">
                            New
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>

          <div className="flex justify-end gap-3">
            <button
              onClick={() => setStep('upload')}
              className="rounded-lg border border-[var(--color-border)] px-4 py-2 text-sm font-medium hover:bg-[var(--color-hover)]"
            >
              Back
            </button>
            <button
              onClick={handleCommit}
              disabled={committing || preview.totalRows === 0}
              className="flex items-center gap-2 rounded-lg bg-[var(--color-accent)] px-5 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)] disabled:cursor-not-allowed disabled:opacity-50"
            >
              {committing && <Loader2 size={16} className="animate-spin" />}
              Save {preview.totalRows} holding{preview.totalRows !== 1 ? 's' : ''}
            </button>
          </div>
        </div>
      )}

      {step === 'review' && preview && !isDeposits && (
        <div className="mt-5 space-y-5">
          <Card>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-5">
              <SummaryStat label="Statement Period" value={`${formatDate(preview.statementFrom)} – ${formatDate(preview.statementTo)}`} />
              <SummaryStat label="Total Rows" value={String(preview.totalRows)} />
              <SummaryStat label="New" value={String(preview.newRows)} positive />
              <SummaryStat label="Duplicates" value={String(preview.duplicateRows)} muted />
            </div>

            <div className="border-t border-[var(--color-border)] pt-4">
              <label className="block text-sm font-medium mb-2">Import into account</label>
              <select
                value={selectedAccountId}
                onChange={(e) => setSelectedAccountId(e.target.value)}
                className="w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
              >
                <option value="">+ Create new account</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} ({a.bank} •• {a.accountNumber.slice(-4)})
                  </option>
                ))}
              </select>
              {!selectedAccountId && (
                <div className="mt-2 grid gap-2 sm:grid-cols-2">
                  <input
                    value={newAccountName}
                    onChange={(e) => setNewAccountName(e.target.value)}
                    placeholder="Account name"
                    className="w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
                  />
                  <select
                    value={newAccountType}
                    onChange={(e) => setNewAccountType(e.target.value)}
                    className="w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
                  >
                    {Object.entries(ACCOUNT_TYPE_LABELS).map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              {!selectedAccountId && preview.accountType === 'ppf' && (
                <p className="mt-2 text-xs text-[var(--color-text-muted)]">
                  This looks like a PPF passbook — deposits only, with credited interest — so PPF is
                  preselected. Change it if that&rsquo;s wrong.
                </p>
              )}
            </div>
          </Card>

          <Card title={`Preview (${preview.transactions.length} rows)`}>
            <div className="max-h-96 overflow-y-auto border border-[var(--color-border)] rounded-lg">
              <table className="w-full text-sm">
                <thead className="sticky top-0 bg-[var(--color-surface-2)]">
                  <tr className="text-xs uppercase text-[var(--color-text-muted)]">
                    <th className="text-left font-medium px-3 py-2">Date</th>
                    <th className="text-left font-medium px-3 py-2">Merchant</th>
                    <th className="text-left font-medium px-3 py-2">Category</th>
                    <th className="text-right font-medium px-3 py-2">Amount</th>
                    <th className="text-left font-medium px-3 py-2 w-20">Status</th>
                  </tr>
                </thead>
                <tbody>
                  {preview.transactions.map((t, i) => (
                    <tr key={i} className="border-t border-[var(--color-border)]">
                      <td className="px-3 py-2 whitespace-nowrap text-[var(--color-text-muted)]">
                        {formatDate(t.txnDate)}
                      </td>
                      <td className="px-3 py-2 max-w-[220px] truncate">{t.merchant || t.narration}</td>
                      <td className="px-3 py-2 text-[var(--color-text-muted)]">
                        {t.suggestedCategory || 'Others'}
                      </td>
                      <td
                        className={`px-3 py-2 text-right whitespace-nowrap font-medium ${
                          t.type === 'income' ? 'text-[var(--color-positive)]' : ''
                        }`}
                      >
                        {t.type === 'income' ? '+' : '-'}
                        {formatINR(t.depositAmt || t.withdrawalAmt)}
                      </td>
                      <td className="px-3 py-2">
                        {t.isDuplicate ? (
                          <span className="text-xs px-2 py-0.5 rounded-full bg-[var(--color-hover)] text-[var(--color-text-muted)]">
                            Duplicate
                          </span>
                        ) : (
                          <span className="text-xs px-2 py-0.5 rounded-full bg-[var(--color-positive-tint)] text-[var(--color-positive)]">
                            New
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>

          <div className="flex justify-end gap-3">
            <button
              onClick={() => setStep('upload')}
              className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)]"
            >
              Back
            </button>
            <button
              onClick={handleCommit}
              disabled={committing || preview.newRows === 0}
              className="flex items-center gap-2 px-5 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {committing && <Loader2 size={16} className="animate-spin" />}
              Import {preview.newRows} transaction{preview.newRows !== 1 ? 's' : ''}
            </button>
          </div>
        </div>
      )}

      {step === 'done' && result && (
        <Card className="mt-5 text-center py-12">
          <CheckCircle2 size={48} className="text-[var(--color-positive)] mx-auto mb-4" />
          <h2 className="text-lg font-semibold mb-1">Import complete</h2>
          <p className="text-sm text-[var(--color-text-muted)] mb-6">
            Imported {result.importedRows} new {result.noun}
            {result.importedRows !== 1 ? 's' : ''}
            {result.duplicateRows > 0 &&
              (result.noun === 'holding'
                ? ` (refreshed ${result.duplicateRows} already tracked)`
                : ` (skipped ${result.duplicateRows} duplicate${result.duplicateRows !== 1 ? 's' : ''})`)}
            .
          </p>
          <div className="flex justify-center gap-3">
            <button
              onClick={() => {
                setStep('upload')
                setPreview(null)
                setResult(null)
              }}
              className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)]"
            >
              Import Another
            </button>
            <button
              onClick={() => navigate('/')}
              className="px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)]"
            >
              Go to Dashboard
            </button>
          </div>
        </Card>
      )}
    </div>
  )
}

function StepIndicator({ step }: { step: Step }) {
  const steps: { key: Step; label: string }[] = [
    { key: 'upload', label: 'Upload' },
    { key: 'review', label: 'Review & Import' },
    { key: 'done', label: 'Done' },
  ]
  const idx = steps.findIndex((s) => s.key === step)
  return (
    <div className="flex items-center gap-2">
      {steps.map((s, i) => (
        <div key={s.key} className="flex items-center gap-2">
          <div
            className={`flex items-center gap-2 px-3 py-1.5 rounded-full text-xs font-medium ${
              i <= idx
                ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent-2)]'
                : 'bg-[var(--color-surface)] text-[var(--color-text-muted)]'
            }`}
          >
            <FileSpreadsheet size={14} />
            {s.label}
          </div>
          {i < steps.length - 1 && <div className="w-6 h-px bg-[var(--color-border)]" />}
        </div>
      ))}
    </div>
  )
}

function SummaryStat({
  label,
  value,
  positive,
  muted,
}: {
  label: string
  value: string
  positive?: boolean
  muted?: boolean
}) {
  return (
    <div>
      <div className="text-xs text-[var(--color-text-muted)] mb-1">{label}</div>
      <div
        className={`text-lg font-semibold ${
          positive ? 'text-[var(--color-positive)]' : muted ? 'text-[var(--color-text-muted)]' : ''
        }`}
      >
        {value}
      </div>
    </div>
  )
}
