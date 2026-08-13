import { useEffect, useState, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { Upload, FileSpreadsheet, CheckCircle2, AlertCircle, Loader2 } from 'lucide-react'
import { api } from '../lib/api'
import { formatINR, formatDate } from '../lib/format'
import { Card } from '../components/Card'
import type { Account, ImportPreview } from '../lib/types'

type Step = 'upload' | 'review' | 'done'

export function ImportStatement() {
  const [step, setStep] = useState<Step>('upload')
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [preview, setPreview] = useState<ImportPreview | null>(null)
  const [accounts, setAccounts] = useState<Account[]>([])
  const [selectedAccountId, setSelectedAccountId] = useState<string>('')
  const [newAccountName, setNewAccountName] = useState('')
  const [committing, setCommitting] = useState(false)
  const [result, setResult] = useState<{ importedRows: number; duplicateRows: number } | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()

  useEffect(() => {
    api.accounts.list().then(setAccounts)
  }, [])

  async function handleFile(file: File) {
    setError(null)
    setUploading(true)
    try {
      const p = await api.import.hdfcPreview(file)
      setPreview(p)
      setSelectedAccountId(p.suggestedAccount ? String(p.suggestedAccount.id) : '')
      if (!p.suggestedAccount) {
        setNewAccountName(`HDFC •• ${p.accountNumber.slice(-4)}`)
      }
      setStep('review')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to parse statement')
    } finally {
      setUploading(false)
    }
  }

  async function handleCommit() {
    if (!preview) return
    setCommitting(true)
    setError(null)
    try {
      const payload: Record<string, unknown> = {
        fileName: preview.fileName,
        transactions: preview.transactions,
      }
      if (selectedAccountId) {
        payload.accountId = Number(selectedAccountId)
      } else {
        payload.newAccount = {
          name: newAccountName || `HDFC •• ${preview.accountNumber.slice(-4)}`,
          bank: 'HDFC',
          accountType: 'bank',
          accountNumber: preview.accountNumber,
          ifsc: preview.ifsc,
          branch: preview.accountBranch,
          openingBalance: preview.openingBalance,
        }
      }
      const res = await api.import.hdfcCommit(payload)
      setResult(res)
      setStep('done')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to import transactions')
    } finally {
      setCommitting(false)
    }
  }

  return (
    <div className="p-8 max-w-4xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">Import Statement</h1>
        <p className="text-[var(--color-text-muted)] text-sm mt-1">
          Upload an HDFC Bank .xls statement export to import transactions automatically.
        </p>
      </div>

      <StepIndicator step={step} />

      {error && (
        <div className="mt-5 flex items-center gap-2 px-4 py-3 rounded-lg bg-red-500/10 border border-red-500/30 text-sm text-red-400">
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
                <p className="font-medium mb-1">Drag & drop your statement here</p>
                <p className="text-sm text-[var(--color-text-muted)]">
                  or click to browse — supports HDFC .xls exports
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

      {step === 'review' && preview && (
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
                <input
                  value={newAccountName}
                  onChange={(e) => setNewAccountName(e.target.value)}
                  placeholder="Account name"
                  className="w-full mt-2 px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]"
                />
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
                          <span className="text-xs px-2 py-0.5 rounded-full bg-white/5 text-[var(--color-text-muted)]">
                            Duplicate
                          </span>
                        ) : (
                          <span className="text-xs px-2 py-0.5 rounded-full bg-emerald-500/15 text-emerald-400">
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
              className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-white/5"
            >
              Back
            </button>
            <button
              onClick={handleCommit}
              disabled={committing || preview.newRows === 0}
              className="flex items-center gap-2 px-5 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {committing && <Loader2 size={16} className="animate-spin" />}
              Import {preview.newRows} transaction{preview.newRows !== 1 ? 's' : ''}
            </button>
          </div>
        </div>
      )}

      {step === 'done' && result && (
        <Card className="mt-5 text-center py-12">
          <CheckCircle2 size={48} className="text-emerald-400 mx-auto mb-4" />
          <h2 className="text-lg font-semibold mb-1">Import complete</h2>
          <p className="text-sm text-[var(--color-text-muted)] mb-6">
            Imported {result.importedRows} new transaction{result.importedRows !== 1 ? 's' : ''}
            {result.duplicateRows > 0 && ` (skipped ${result.duplicateRows} duplicate${result.duplicateRows !== 1 ? 's' : ''})`}
            .
          </p>
          <div className="flex justify-center gap-3">
            <button
              onClick={() => {
                setStep('upload')
                setPreview(null)
                setResult(null)
              }}
              className="px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-white/5"
            >
              Import Another
            </button>
            <button
              onClick={() => navigate('/')}
              className="px-4 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-violet-500"
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
