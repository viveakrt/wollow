import { AlertCircle, Check, Loader2 } from 'lucide-react'
import type { BulkJobStatus } from '../api'

/**
 * Progress and outcome for a sender-level bulk action.
 *
 * These actions run detached and can touch thousands of messages, so a
 * spinner on a button is not enough: without a count, a delete that is working
 * fine is indistinguishable from one that has hung — which is exactly how the
 * old synchronous version presented when it timed out.
 */
export function BulkJobBanner({
  status,
  onDismiss,
}: {
  status: BulkJobStatus
  onDismiss: () => void
}) {
  if (status.running) {
    const done = status.progress?.done ?? 0
    const total = status.progress?.total ?? 0
    // Before the first chunk reports, show an indeterminate bar rather than a
    // misleading 0%.
    const pct = total > 0 ? Math.round((done / total) * 100) : null

    return (
      <Frame tone="busy">
        <Loader2 size={15} className="shrink-0 animate-spin" />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className="font-medium">Working through your mailbox…</span>
            {total > 0 && (
              <span className="text-xs opacity-80">
                {done.toLocaleString()} of {total.toLocaleString()} {status.progress?.label ?? 'messages'}
              </span>
            )}
          </div>
          <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-border)]">
            <div
              className={`h-full rounded-full bg-[var(--color-accent)] transition-[width] duration-300 ${
                pct === null ? 'w-1/3 animate-pulse' : ''
              }`}
              style={pct === null ? undefined : { width: `${pct}%` }}
            />
          </div>
        </div>
        {pct !== null && <span className="shrink-0 text-xs tabular-nums opacity-80">{pct}%</span>}
      </Frame>
    )
  }

  if (status.error) {
    return (
      <Frame tone="error" onDismiss={onDismiss}>
        <AlertCircle size={15} className="shrink-0" />
        <span className="min-w-0 flex-1">{status.error}</span>
      </Frame>
    )
  }

  const detail = status.detail
  if (!detail) return null

  const failed = detail.failed?.length ?? 0
  const noun = `message${detail.updated === 1 ? '' : 's'}`

  return (
    <Frame tone={failed > 0 ? 'error' : 'ok'} onDismiss={onDismiss}>
      {failed > 0 ? (
        <AlertCircle size={15} className="shrink-0" />
      ) : (
        <Check size={15} className="shrink-0" />
      )}
      <span className="min-w-0 flex-1">
        {detail.action} {detail.updated.toLocaleString()} {noun}
        {failed > 0 && ` · ${failed.toLocaleString()} could not be processed`}
      </span>
    </Frame>
  )
}

function Frame({
  tone,
  onDismiss,
  children,
}: {
  tone: 'busy' | 'ok' | 'error'
  onDismiss?: () => void
  children: React.ReactNode
}) {
  const toneClass = {
    busy: 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text)]',
    ok: 'border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-text-muted)]',
    error: 'border-[var(--color-negative)] bg-[var(--color-negative-tint)] text-[var(--color-negative)]',
  }[tone]

  return (
    <div className={`flex items-center gap-3 border-b px-5 py-2.5 text-sm ${toneClass}`}>
      {children}
      {onDismiss && (
        <button
          type="button"
          onClick={onDismiss}
          className="shrink-0 rounded px-2 py-0.5 text-xs font-medium opacity-70 transition-opacity hover:opacity-100"
        >
          Dismiss
        </button>
      )}
    </div>
  )
}
