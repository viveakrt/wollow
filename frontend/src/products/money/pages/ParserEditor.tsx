import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, AlertCircle, CheckCircle2, Loader2, Wand2, History } from 'lucide-react'
import { api } from '../api'
import { Card } from '../components/Card'
import { EmailPicker } from '../components/parsers/EmailPicker'
import { SpanEditor } from '../components/parsers/SpanEditor'
import { RuleSettingsForm, type RuleSettings } from '../components/parsers/RuleSettingsForm'
import { RuleTestTable } from '../components/parsers/RuleTestTable'
import type {
  LabeledSpan,
  ParserRule,
  ParserRuleInput,
  ParserSample,
  ParserTestResponse,
  RescanResult,
} from '../types'

type Step = 'pick' | 'mark' | 'test' | 'done'

const DEFAULT_SETTINGS: RuleSettings = {
  name: '',
  kind: 'transaction',
  enabled: true,
  priority: 0,
  issuer: '',
  senderDomain: '',
  senderEmail: '',
  subjectContains: '',
  bodyContains: '',
  accountId: 0,
  accountType: 'bank',
  attributes: { direction: 'expense' },
}

/**
 * Define (or edit) a parser: pick an email, mark the values in its text, see
 * what the rule reads out of the sender's recent mail, save.
 *
 * Like the statement importer this is a local state machine — only the
 * catalogue of fields, the sample fetch and the final save touch the server.
 */
export function ParserEditor() {
  const { id } = useParams<{ id: string }>()
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const editingId = id ? Number(id) : null

  const [step, setStep] = useState<Step>('pick')
  const [sample, setSample] = useState<ParserSample | null>(null)
  const [settings, setSettings] = useState<RuleSettings>(DEFAULT_SETTINGS)
  const [spans, setSpans] = useState<LabeledSpan[]>([])
  const [testResult, setTestResult] = useState<ParserTestResponse | null>(null)
  const [saved, setSaved] = useState<ParserRule | null>(null)
  const [rescan, setRescan] = useState<RescanResult | null>(null)
  const [busy, setBusy] = useState<'sample' | 'test' | 'save' | 'rescan' | null>(null)
  const [error, setError] = useState<string | null>(null)

  const catalogue = useQuery({ queryKey: ['money', 'parser-fields'], queryFn: api.parserRules.fields })
  const fields = useMemo(() => catalogue.data?.[settings.kind] ?? [], [catalogue.data, settings.kind])

  // Entry points: an existing rule, a message handed over by its inbox chip,
  // or nothing — in which case the first step is choosing an email.
  useEffect(() => {
    let cancelled = false
    async function boot() {
      if (editingId) {
        try {
          const rule = await api.parserRules.get(editingId)
          if (cancelled) return
          setSettings({
            name: rule.name,
            kind: rule.kind,
            enabled: rule.enabled,
            priority: rule.priority,
            issuer: rule.issuer,
            senderDomain: rule.senderDomain,
            senderEmail: rule.senderEmail,
            subjectContains: rule.subjectContains,
            bodyContains: rule.bodyContains,
            accountId: rule.accountId,
            accountType: rule.accountType,
            attributes: rule.attributes ?? {},
          })
          setSample(rule.sample)
          setSpans(rule.fields.map((f) => ({ name: f.name, start: f.start, end: f.end, sample: f.sample })))
          setStep('mark')
        } catch (e) {
          if (!cancelled) setError(e instanceof Error ? e.message : 'Could not load the rule')
        }
        return
      }
      const mailAccountId = Number(searchParams.get('mailAccountId'))
      const uid = Number(searchParams.get('uid'))
      if (mailAccountId && uid) {
        await pick(mailAccountId, uid, searchParams.get('folder') || 'INBOX')
      }
    }
    boot()
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editingId])

  async function pick(mailAccountId: number, uid: number, folder = 'INBOX') {
    setError(null)
    setBusy('sample')
    try {
      const s = await api.parserRules.sample(mailAccountId, uid, folder)
      setSample(s)
      setSpans([])
      setSettings((prev) => ({
        ...prev,
        name: prev.name || nameFrom(s),
        senderDomain: s.fromDomain ?? '',
        issuer: s.issuer ?? '',
        accountType:
          prev.kind === 'bill' ? 'credit_card' : s.defaultKind && s.defaultKind !== 'investment' ? s.defaultKind : prev.accountType,
      }))
      setStep('mark')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not load the email')
    } finally {
      setBusy(null)
    }
  }

  const missingRequired = fields.filter((f) => f.required && !spans.some((s) => s.name === f.name))

  function buildInput(): ParserRuleInput | null {
    if (!sample) return null
    return { ...settings, sample: { ...sample }, spans }
  }

  async function runTest() {
    const input = buildInput()
    if (!input || !sample) return
    setError(null)
    setBusy('test')
    try {
      const result = await api.parserRules.test(input, sample.mailAccountId, 20)
      setTestResult(result)
      setStep('test')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The rule could not be built')
    } finally {
      setBusy(null)
    }
  }

  async function save() {
    const input = buildInput()
    if (!input) return
    setError(null)
    setBusy('save')
    try {
      const rule = editingId
        ? await api.parserRules.update(editingId, input)
        : await api.parserRules.create(input)
      setSaved(rule)
      setStep('done')
      queryClient.invalidateQueries({ queryKey: ['money'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not save the rule')
    } finally {
      setBusy(null)
    }
  }

  async function applyToOldMail() {
    if (!saved) return
    setError(null)
    setBusy('rescan')
    try {
      setRescan(await api.parserRules.rescan(saved.id))
      queryClient.invalidateQueries({ queryKey: ['money'] })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Rescan failed')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="mx-auto max-w-6xl p-8">
      <Link
        to="/money/parsers"
        className="mb-4 inline-flex items-center gap-1 text-sm font-medium text-[var(--color-accent-2)] hover:text-[var(--color-accent)]"
      >
        <ArrowLeft size={16} />
        Parsers
      </Link>
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">
          {editingId ? 'Edit parser' : 'Define a parser'}
        </h1>
        <p className="mt-1 text-sm text-[var(--color-text-muted)]">
          Pick one email, mark where the amount, account and other values are written, and every
          later email from that sender is read the same way.
        </p>
      </div>

      <StepIndicator step={step} />

      {error && (
        <div className="mt-5 flex items-center gap-2 rounded-lg border border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-4 py-3 text-sm text-[var(--color-negative)]">
          <AlertCircle size={16} />
          {error}
        </div>
      )}

      {step === 'pick' && (
        <Card className="mt-5" title="1. Choose an email to learn from">
          {busy === 'sample' ? (
            <div className="flex items-center gap-2 py-10 text-sm text-[var(--color-text-muted)]">
              <Loader2 size={14} className="animate-spin" /> Fetching the email…
            </div>
          ) : (
            <EmailPicker onPick={(mailAccountId, uid) => pick(mailAccountId, uid)} />
          )}
        </Card>
      )}

      {step === 'mark' && sample && (
        <div className="mt-5 grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <Card title="2. Mark the values">
            <div className="mb-3 text-xs text-[var(--color-text-muted)]">
              <span className="font-medium text-[var(--color-text)]">{sample.subject || '(no subject)'}</span>
              {' · '}
              {sample.from}
            </div>
            {catalogue.isPending ? (
              <div className="text-sm text-[var(--color-text-muted)]">Loading…</div>
            ) : (
              <SpanEditor text={sample.text ?? ''} fields={fields} spans={spans} onChange={setSpans} />
            )}
            <div className="mt-5 flex items-center justify-between">
              {editingId ? (
                <span />
              ) : (
                <button onClick={() => setStep('pick')} className={secondaryButton}>
                  Choose a different email
                </button>
              )}
              <div className="flex items-center gap-3">
                {missingRequired.length > 0 && (
                  <span className="text-xs text-[var(--color-text-muted)]">
                    Mark {missingRequired.map((f) => f.label.toLowerCase()).join(', ')} to continue
                  </span>
                )}
                <button
                  onClick={runTest}
                  disabled={missingRequired.length > 0 || !settings.name.trim() || busy === 'test'}
                  className={primaryButton}
                >
                  {busy === 'test' && <Loader2 size={14} className="animate-spin" />}
                  Test on recent mail
                </button>
              </div>
            </div>
          </Card>
          <Card title="Rule settings" className="h-fit">
            <RuleSettingsForm
              value={settings}
              onChange={setSettings}
              sampleFrom={sample.from}
              editing={Boolean(editingId)}
            />
          </Card>
        </div>
      )}

      {step === 'test' && testResult && (
        <Card className="mt-5" title="3. Check what it reads">
          <RuleTestTable result={testResult} fields={fields} />
          <div className="mt-5 flex items-center justify-between">
            <button onClick={() => setStep('mark')} className={secondaryButton}>
              Back
            </button>
            <button onClick={save} disabled={busy === 'save'} className={primaryButton}>
              {busy === 'save' && <Loader2 size={14} className="animate-spin" />}
              {editingId ? 'Save changes' : 'Save parser'}
            </button>
          </div>
        </Card>
      )}

      {step === 'done' && saved && (
        <Card className="mt-5 py-10 text-center">
          <CheckCircle2 size={44} className="mx-auto mb-3 text-[var(--color-positive)]" />
          <h2 className="mb-1 text-lg font-semibold">“{saved.name}” is saved</h2>
          <p className="mb-6 text-sm text-[var(--color-text-muted)]">
            New mail from {saved.senderEmail || saved.senderDomain} is read with it from the next sync.
            {' '}Mail that arrived before it existed can be read now.
          </p>
          {rescan && (
            <p className="mb-4 text-sm text-[var(--color-positive)]">
              Re-read {rescan.cleared} message{rescan.cleared !== 1 ? 's' : ''}: {rescan.transactions}{' '}
              transaction{rescan.transactions !== 1 ? 's' : ''}, {rescan.bills} bill
              {rescan.bills !== 1 ? 's' : ''}, {rescan.balances} balance
              {rescan.balances !== 1 ? 's' : ''}, {rescan.trades} order{rescan.trades !== 1 ? 's' : ''}
              {rescan.pendingAccount > 0 && `, ${rescan.pendingAccount} waiting for an account`}.
            </p>
          )}
          <div className="flex justify-center gap-3">
            {!rescan && (
              <button onClick={applyToOldMail} disabled={busy === 'rescan'} className={secondaryButton}>
                {busy === 'rescan' ? <Loader2 size={14} className="animate-spin" /> : <History size={14} />}
                Apply to old mail now
              </button>
            )}
            <button onClick={() => navigate('/money/parsers')} className={primaryButton}>
              <Wand2 size={14} />
              Back to parsers
            </button>
          </div>
        </Card>
      )}
    </div>
  )
}

/** A readable default name: the sender's domain and the subject, digits stripped. */
function nameFrom(s: ParserSample): string {
  const subject = (s.subject || '').replace(/[\d,.]+/g, '').replace(/\s+/g, ' ').trim()
  const short = subject.length > 40 ? subject.slice(0, 40).trim() + '…' : subject
  return [s.issuer || s.fromDomain, short].filter(Boolean).join(' · ')
}

function StepIndicator({ step }: { step: Step }) {
  const steps: { key: Step; label: string }[] = [
    { key: 'pick', label: 'Choose email' },
    { key: 'mark', label: 'Mark values' },
    { key: 'test', label: 'Test' },
    { key: 'done', label: 'Done' },
  ]
  const idx = steps.findIndex((s) => s.key === step)
  return (
    <div className="flex items-center gap-2">
      {steps.map((s, i) => (
        <div key={s.key} className="flex items-center gap-2">
          <div
            className={`flex items-center gap-2 rounded-full px-3 py-1.5 text-xs font-medium ${
              i <= idx
                ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent-2)]'
                : 'bg-[var(--color-surface)] text-[var(--color-text-muted)]'
            }`}
          >
            <Wand2 size={14} />
            {s.label}
          </div>
          {i < steps.length - 1 && <div className="h-px w-6 bg-[var(--color-border)]" />}
        </div>
      ))}
    </div>
  )
}

const primaryButton =
  'flex items-center gap-2 px-5 py-2 rounded-lg bg-[var(--color-accent)] text-white text-sm font-medium hover:bg-[var(--color-accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed transition-colors'
const secondaryButton =
  'flex items-center gap-2 px-4 py-2 rounded-lg border border-[var(--color-border)] text-sm font-medium hover:bg-[var(--color-hover)] disabled:opacity-50 transition-colors'
