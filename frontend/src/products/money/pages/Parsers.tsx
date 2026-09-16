import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Wand2, Pencil, Trash2, History, Loader2, AlertCircle } from 'lucide-react'
import { api } from '../api'
import { Card } from '../components/Card'
import { PendingAccountsPanel } from '../components/PendingAccountsPanel'
import { PARSER_KIND_LABELS } from '../types'
import type { ParserRule, RescanResult } from '../types'
import { formatDate } from '../lib/format'

export function Parsers() {
  const queryClient = useQueryClient()
  const [lastRescan, setLastRescan] = useState<{ id: number; result: RescanResult } | null>(null)

  const rules = useQuery({ queryKey: ['money', 'parser-rules'], queryFn: api.parserRules.list })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['money'] })

  const toggle = useMutation({
    mutationFn: (rule: ParserRule) => api.parserRules.update(rule.id, { ...rule, enabled: !rule.enabled }),
    onSuccess: invalidate,
  })
  const remove = useMutation({ mutationFn: api.parserRules.delete, onSuccess: invalidate })
  const rescan = useMutation({
    mutationFn: api.parserRules.rescan,
    onSuccess: (result, id) => {
      setLastRescan({ id, result })
      invalidate()
    },
  })

  const error = toggle.error ?? remove.error ?? rescan.error

  return (
    <div className="mx-auto max-w-[1200px] p-8">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Parsers</h1>
          <p className="mt-1 text-sm text-[var(--color-text-muted)]">
            How Money reads your bank, card and broker mail. Each parser is taught from one real
            email; there are no built-in ones.
          </p>
        </div>
        <Link
          to="/money/parsers/new"
          className="flex items-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-accent-hover)]"
        >
          <Plus size={16} />
          Define parser
        </Link>
      </div>

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]">
          <AlertCircle size={14} />
          {error instanceof Error ? error.message : 'Something went wrong'}
        </div>
      )}

      <PendingAccountsPanel />

      {rules.isPending ? (
        <div className="text-[var(--color-text-muted)]">Loading…</div>
      ) : (rules.data ?? []).length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-[var(--color-border)] py-24">
          <Wand2 size={40} className="mb-4 text-[var(--color-text-muted)]" />
          <h2 className="mb-1 text-lg font-semibold">No parsers yet</h2>
          <p className="mb-5 max-w-md text-center text-sm text-[var(--color-text-muted)]">
            Until you define one, finance mail is only marked as seen. Open any bank alert in Mail
            and use “Define a parser from this email”, or start here.
          </p>
          <Link
            to="/money/parsers/new"
            className="flex items-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-accent-hover)]"
          >
            <Plus size={16} />
            Define parser
          </Link>
        </div>
      ) : (
        <Card className="p-0">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-[var(--color-text-muted)]">
              <tr className="border-b border-[var(--color-border)]">
                <th className="px-5 py-3 font-medium">Parser</th>
                <th className="px-3 py-3 font-medium">Reads</th>
                <th className="px-3 py-3 font-medium">From</th>
                <th className="px-3 py-3 font-medium">Matched</th>
                <th className="px-3 py-3 font-medium">On</th>
                <th className="px-3 py-3"></th>
              </tr>
            </thead>
            <tbody>
              {(rules.data ?? []).map((rule) => (
                <tr
                  key={rule.id}
                  className={`border-b border-[var(--color-border)] last:border-0 ${rule.enabled ? '' : 'opacity-60'}`}
                >
                  <td className="px-5 py-3">
                    <div className="font-medium">{rule.name}</div>
                    {(rule.subjectContains || rule.bodyContains) && (
                      <div className="text-xs text-[var(--color-text-muted)]">
                        {rule.subjectContains && `subject has “${rule.subjectContains}”`}
                        {rule.subjectContains && rule.bodyContains && ' · '}
                        {rule.bodyContains && `body has “${rule.bodyContains}”`}
                      </div>
                    )}
                    {lastRescan?.id === rule.id && (
                      <div className="mt-1 text-xs text-[var(--color-positive)]">
                        Re-read {lastRescan.result.cleared} message
                        {lastRescan.result.cleared !== 1 ? 's' : ''}: {lastRescan.result.transactions}{' '}
                        transaction{lastRescan.result.transactions !== 1 ? 's' : ''},{' '}
                        {lastRescan.result.bills} bill{lastRescan.result.bills !== 1 ? 's' : ''},{' '}
                        {lastRescan.result.balances} balance{lastRescan.result.balances !== 1 ? 's' : ''}
                        {lastRescan.result.pendingAccount > 0 &&
                          `, ${lastRescan.result.pendingAccount} waiting for an account`}
                        .
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-3 text-[var(--color-text-muted)]">{PARSER_KIND_LABELS[rule.kind]}</td>
                  <td className="px-3 py-3 text-[var(--color-text-muted)]">
                    {rule.senderEmail || rule.senderDomain}
                  </td>
                  <td className="px-3 py-3">
                    {rule.matchCount}
                    {rule.lastMatchedAt && (
                      <span className="text-xs text-[var(--color-text-muted)]">
                        {' '}
                        · last {formatDate(rule.lastMatchedAt)}
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-3">
                    <label className="inline-flex cursor-pointer items-center gap-2 text-xs">
                      <input
                        type="checkbox"
                        checked={rule.enabled}
                        disabled={toggle.isPending && toggle.variables?.id === rule.id}
                        onChange={() => toggle.mutate(rule)}
                        className="accent-[var(--color-accent)]"
                      />
                      {rule.enabled ? 'On' : 'Off'}
                    </label>
                  </td>
                  <td className="px-3 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => rescan.mutate(rule.id)}
                        disabled={rescan.isPending && rescan.variables === rule.id}
                        title="Read the mail from this sender that arrived before the parser existed"
                        className="flex items-center gap-1 rounded-lg px-2 py-1.5 text-xs text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] disabled:opacity-50"
                      >
                        {rescan.isPending && rescan.variables === rule.id ? (
                          <Loader2 size={13} className="animate-spin" />
                        ) : (
                          <History size={13} />
                        )}
                        Apply to old mail
                      </button>
                      <Link
                        to={`/money/parsers/${rule.id}`}
                        title="Edit"
                        className="rounded-lg p-1.5 text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]"
                      >
                        <Pencil size={14} />
                      </Link>
                      <button
                        onClick={() => {
                          if (confirm(`Delete the parser “${rule.name}”? Mail it already read stays as it is.`))
                            remove.mutate(rule.id)
                        }}
                        title="Delete"
                        className="rounded-lg p-1.5 text-[var(--color-text-muted)] hover:bg-[var(--color-negative-tint)] hover:text-[var(--color-negative)]"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </div>
  )
}
