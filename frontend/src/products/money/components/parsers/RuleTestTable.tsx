import { CheckCircle2, XCircle } from 'lucide-react'
import type { ParserFieldSpec, ParserTestResponse } from '../../types'
import { formatDate } from '../../lib/format'

/**
 * What the draft rule read out of the sender's recent mail. Every row is a
 * real message; a miss says why, so the user can tell "different template"
 * from "my selection was too narrow".
 */
export function RuleTestTable({
  result,
  fields,
}: {
  result: ParserTestResponse
  fields: ParserFieldSpec[]
}) {
  const matched = result.results.filter((r) => r.matched).length
  const names = result.fields.map((f) => f.name)
  const labelOf = new Map(fields.map((f) => [f.name, f.label]))

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
        <span>
          <span className="font-semibold">{matched}</span> of {result.results.length} recent messages
          read
        </span>
        {result.results.length > 0 && matched < result.results.length && (
          <span className="text-xs text-[var(--color-text-muted)]">
            Misses are normal when the sender uses several templates — define another rule for those.
          </span>
        )}
      </div>

      {result.results.length === 0 ? (
        <p className="text-sm text-[var(--color-text-muted)]">
          No messages from this sender are in the mailbox yet, so there is nothing to test against.
        </p>
      ) : (
        <div className="max-h-96 overflow-auto rounded-lg border border-[var(--color-border)]">
          <table className="w-full text-xs">
            <thead className="sticky top-0 bg-[var(--color-surface-2)] text-left text-[var(--color-text-muted)]">
              <tr>
                <th className="px-3 py-2 font-medium"></th>
                <th className="px-3 py-2 font-medium">Date</th>
                <th className="px-3 py-2 font-medium">Subject</th>
                {names.map((n) => (
                  <th key={n} className="px-3 py-2 font-medium">
                    {labelOf.get(n) ?? n}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {result.results.map((r) => (
                <tr key={r.uid} className="border-t border-[var(--color-border)] align-top">
                  <td className="px-3 py-2">
                    {r.matched ? (
                      <CheckCircle2 size={14} className="text-[var(--color-positive)]" />
                    ) : (
                      <XCircle size={14} className="text-[var(--color-text-muted)]" />
                    )}
                  </td>
                  <td className="whitespace-nowrap px-3 py-2 text-[var(--color-text-muted)]">
                    {formatDate(r.date)}
                  </td>
                  <td className="max-w-[16rem] truncate px-3 py-2" title={r.subject}>
                    {r.subject || '(no subject)'}
                  </td>
                  {r.matched ? (
                    names.map((n) => (
                      <td key={n} className="max-w-[12rem] truncate px-3 py-2 font-mono" title={r.values?.[n]}>
                        {r.values?.[n] ?? <span className="text-[var(--color-text-subtle)]">—</span>}
                      </td>
                    ))
                  ) : (
                    <td colSpan={Math.max(1, names.length)} className="px-3 py-2 text-[var(--color-text-muted)]">
                      {r.reason}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
