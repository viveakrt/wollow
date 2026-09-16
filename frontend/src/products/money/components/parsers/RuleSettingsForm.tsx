import { useQuery } from '@tanstack/react-query'
import { api } from '../../api'
import { PARSER_KIND_HELP, PARSER_KIND_LABELS, RULE_ACCOUNT_TYPES } from '../../types'
import type { ParserRuleKind, ParserAttributes } from '../../types'

/** Everything about a rule that is not a marked value. */
export interface RuleSettings {
  name: string
  kind: ParserRuleKind
  enabled: boolean
  priority: number
  issuer: string
  senderDomain: string
  senderEmail: string
  subjectContains: string
  bodyContains: string
  accountId: number
  accountType: string
  attributes: ParserAttributes
}

const KINDS: ParserRuleKind[] = ['transaction', 'bill', 'balance', 'trade']

export function RuleSettingsForm({
  value,
  onChange,
  sampleFrom,
  editing,
}: {
  value: RuleSettings
  onChange: (next: RuleSettings) => void
  /** The sample's sender address, offered for exact-sender matching. */
  sampleFrom: string
  editing: boolean
}) {
  const institutions = useQuery({
    queryKey: ['money', 'institutions'],
    queryFn: api.institutions.list,
    staleTime: Infinity,
  })
  const accounts = useQuery({ queryKey: ['money', 'accounts'], queryFn: api.accounts.list })

  const set = <K extends keyof RuleSettings>(key: K, v: RuleSettings[K]) => onChange({ ...value, [key]: v })
  const setAttr = <K extends keyof ParserAttributes>(key: K, v: ParserAttributes[K]) =>
    onChange({ ...value, attributes: { ...value.attributes, [key]: v } })

  const exact = value.senderEmail !== ''

  return (
    <div className="space-y-4">
      <Field label="Name">
        <input value={value.name} onChange={(e) => set('name', e.target.value)} className={inputClass} />
      </Field>

      <Field label="This email is a…">
        <select
          value={value.kind}
          onChange={(e) => {
            const kind = e.target.value as ParserRuleKind
            onChange({
              ...value,
              kind,
              accountType:
                kind === 'bill' ? 'credit_card' : kind === 'trade' ? 'investment' : value.accountType,
            })
          }}
          className={inputClass}
        >
          {KINDS.map((k) => (
            <option key={k} value={k}>
              {PARSER_KIND_LABELS[k]}
            </option>
          ))}
        </select>
        <p className="mt-1 text-xs text-[var(--color-text-muted)]">{PARSER_KIND_HELP[value.kind]}</p>
      </Field>

      {value.kind === 'transaction' && (
        <div className="grid grid-cols-2 gap-3">
          <Field label="Money moved">
            <select
              value={value.attributes.direction ?? 'expense'}
              onChange={(e) => setAttr('direction', e.target.value as 'expense' | 'income')}
              className={inputClass}
            >
              <option value="expense">Out (debit / spend)</option>
              <option value="income">In (credit / received)</option>
            </select>
          </Field>
          <Field label="Payment method (optional)">
            <input
              value={value.attributes.paymentMethod ?? ''}
              onChange={(e) => setAttr('paymentMethod', e.target.value)}
              placeholder="Detected from the text"
              className={inputClass}
            />
          </Field>
        </div>
      )}

      {value.kind !== 'trade' && (
        <div className="grid grid-cols-2 gap-3">
          <Field label="Account type">
            <select
              value={value.accountType}
              onChange={(e) => set('accountType', e.target.value)}
              className={inputClass}
            >
              {RULE_ACCOUNT_TYPES.filter((t) => t.value !== 'investment').map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Institution">
            <input
              value={value.issuer}
              onChange={(e) => set('issuer', e.target.value)}
              list="rule-institutions"
              placeholder="e.g. HDFC"
              className={inputClass}
            />
            <datalist id="rule-institutions">
              {(institutions.data ?? []).map((inst) => (
                <option key={inst.issuer} value={inst.issuer}>
                  {inst.name}
                </option>
              ))}
            </datalist>
          </Field>
        </div>
      )}

      {value.kind === 'trade' && (
        <div className="grid grid-cols-2 gap-3">
          <Field label="Broker">
            <input
              value={value.attributes.broker ?? value.issuer}
              onChange={(e) => setAttr('broker', e.target.value)}
              placeholder="e.g. INDmoney"
              className={inputClass}
            />
          </Field>
          <Field label="Instrument">
            <select
              value={value.attributes.instrumentKind ?? 'stock'}
              onChange={(e) => setAttr('instrumentKind', e.target.value)}
              className={inputClass}
            >
              <option value="stock">Indian stock</option>
              <option value="us_stock">US stock</option>
              <option value="mutual_fund">Mutual fund</option>
            </select>
          </Field>
          <Field label="Currency">
            <input
              value={value.attributes.currency ?? 'INR'}
              onChange={(e) => setAttr('currency', e.target.value.toUpperCase())}
              className={inputClass}
            />
          </Field>
          <Field label="Side (if not in the email)">
            <select
              value={value.attributes.side ?? 'buy'}
              onChange={(e) => setAttr('side', e.target.value as 'buy' | 'sell')}
              className={inputClass}
            >
              <option value="buy">Buy</option>
              <option value="sell">Sell</option>
            </select>
          </Field>
        </div>
      )}

      {value.kind !== 'trade' && (
        <Field label="Always this account (optional)">
          <select
            value={value.accountId || ''}
            onChange={(e) => set('accountId', Number(e.target.value) || 0)}
            className={inputClass}
          >
            <option value="">Find it from the digits in the email</option>
            {(accounts.data ?? []).map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          <p className="mt-1 text-xs text-[var(--color-text-muted)]">
            For senders that never state an account number — a wallet, say.
          </p>
        </Field>
      )}

      <div className="border-t border-[var(--color-border)] pt-4">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
          Which emails
        </p>
        <Field label="Sender domain">
          <input
            value={value.senderDomain}
            onChange={(e) => set('senderDomain', e.target.value)}
            placeholder="hdfcbank.net"
            className={inputClass}
          />
        </Field>
        {sampleFrom && (
          <label className="mt-2 flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              checked={exact}
              onChange={(e) => set('senderEmail', e.target.checked ? sampleFrom : '')}
              className="accent-[var(--color-accent)]"
            />
            Only mail from exactly {sampleFrom}
          </label>
        )}
        <div className="mt-3 grid grid-cols-1 gap-3">
          <Field label="Subject contains (optional)">
            <input
              value={value.subjectContains}
              onChange={(e) => set('subjectContains', e.target.value)}
              placeholder="e.g. debited"
              className={inputClass}
            />
          </Field>
          <Field label="Body contains (optional)">
            <input
              value={value.bodyContains}
              onChange={(e) => set('bodyContains', e.target.value)}
              placeholder="A phrase only this kind of email has"
              className={inputClass}
            />
          </Field>
        </div>
        <p className="mt-1 text-xs text-[var(--color-text-muted)]">
          One sender often has several templates. Give each its own rule and use these to tell them
          apart; a rule that matches the sender but can't read the values simply passes.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3 border-t border-[var(--color-border)] pt-4">
        <Field label="Priority">
          <input
            type="number"
            value={value.priority}
            onChange={(e) => set('priority', Number(e.target.value) || 0)}
            className={inputClass}
          />
          <p className="mt-1 text-xs text-[var(--color-text-muted)]">Higher is tried first.</p>
        </Field>
        {editing && (
          <label className="flex items-center gap-2 pt-6 text-sm">
            <input
              type="checkbox"
              checked={value.enabled}
              onChange={(e) => set('enabled', e.target.checked)}
              className="accent-[var(--color-accent)]"
            />
            Enabled
          </label>
        )}
      </div>
    </div>
  )
}

const inputClass =
  'w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]'

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium text-[var(--color-text-muted)]">{label}</label>
      {children}
    </div>
  )
}
