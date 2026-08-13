import type { ClassifyStatus, Insights, SyncStatus } from '../api'
import { CategoryDonut } from './CategoryDonut'
import { Spinner } from '../../../platform/components/Spinner'
import { PRIORITY_STYLES } from '../theme/categories'

interface InsightsPanelProps {
  insights?: Insights
  sync?: SyncStatus
  classify?: ClassifyStatus
  selectedCategory: string | null
  onSelectCategory: (category: string | null) => void
  onSelectSender: (email: string | null) => void
  selectedSender: string | null
  onSync: () => void
  onClassify: () => void
}

export function InsightsPanel({
  insights,
  sync,
  classify,
  selectedCategory,
  onSelectCategory,
  onSelectSender,
  selectedSender,
  onSync,
  onClassify,
}: InsightsPanelProps) {
  const totals = insights?.totals
  const views = insights?.smartViews ?? {}

  return (
    <aside className="hidden w-80 shrink-0 overflow-y-auto border-l border-[var(--color-border)] bg-[var(--color-surface-2)] p-4 xl:block">
      <section>
        <h2 className="text-sm font-semibold text-[var(--color-text)]">Inbox Summary</h2>
        <div className="mt-2 grid grid-cols-2 gap-2">
          <StatTile label="Indexed" value={totals?.messages} tone="slate" />
          <StatTile label="Unread" value={totals?.unread} tone="indigo" />
          <StatTile label="Needs action" value={views.needs_action} tone="amber" />
          <StatTile label="Needs reply" value={views.needs_reply} tone="rose" />
        </div>
      </section>

      <section className="mt-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-[var(--color-text)]">AI Categorization</h2>
          {classify?.running && <Spinner className="h-3.5 w-3.5 text-[var(--color-accent-2)]" />}
        </div>
        <div className="mt-2">
          <CategoryDonut
            data={insights?.categories ?? []}
            selected={selectedCategory}
            onSelect={onSelectCategory}
          />
        </div>
        {classify && classify.pending > 0 && (
          <p className="mt-2 text-[11px] text-[var(--color-text-subtle)]">
            {classify.pending.toLocaleString()} still to classify
          </p>
        )}
      </section>

      {insights && insights.priorities.length > 0 && (
        <section className="mt-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
          <h2 className="text-sm font-semibold text-[var(--color-text)]">Priority</h2>
          <ul className="mt-2 space-y-1.5">
            {insights.priorities.map((p) => {
              const style = PRIORITY_STYLES[p.key]
              return (
                <li key={p.key} className="flex items-center gap-2">
                  <span
                    aria-hidden
                    className="h-2 w-2 shrink-0 rounded-full"
                    style={{ backgroundColor: style?.dot ?? '#cbd5e1' }}
                  />
                  <span className="min-w-0 flex-1 truncate text-xs text-[var(--color-text-muted)]">
                    {style?.label ?? p.key}
                  </span>
                  <span className="shrink-0 text-xs tabular-nums text-[var(--color-text-subtle)]">
                    {p.count.toLocaleString()}
                  </span>
                </li>
              )
            })}
          </ul>
        </section>
      )}

      {insights && insights.senders.length > 0 && (
        <section className="mt-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
          <h2 className="text-sm font-semibold text-[var(--color-text)]">Top Senders</h2>
          <ul className="mt-2 space-y-1.5">
            {insights.senders.slice(0, 6).map((s) => {
              const max = insights.senders[0]?.count || 1
              const active = selectedSender === s.email
              return (
                <li key={s.email}>
                  <button
                    type="button"
                    onClick={() => onSelectSender(active ? null : s.email)}
                    className={`w-full rounded px-1 py-1 text-left transition hover:bg-[var(--color-bg)] ${
                      active ? 'bg-[var(--color-surface-2)]' : ''
                    }`}
                    title={s.email}
                  >
                    <div className="flex items-baseline justify-between gap-2">
                      <span className="min-w-0 truncate text-xs text-[var(--color-text)]">
                        {s.name || s.email}
                      </span>
                      <span className="shrink-0 text-[11px] tabular-nums text-[var(--color-text-subtle)]">
                        {s.count.toLocaleString()}
                      </span>
                    </div>
                    <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-surface-2)]">
                      <div
                        className="h-full rounded-full bg-[var(--color-accent-2)]"
                        style={{ width: `${(s.count / max) * 100}%` }}
                      />
                    </div>
                  </button>
                </li>
              )
            })}
          </ul>
        </section>
      )}

      <section className="mt-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-3">
        <h2 className="text-sm font-semibold text-[var(--color-text)]">Quick Actions</h2>
        <div className="mt-2 grid grid-cols-1 gap-2">
          <QuickAction
            title={sync?.running ? 'Syncing…' : 'Sync mailbox'}
            subtitle={
              sync?.running
                ? `${(sync.stored ?? 0).toLocaleString()} indexed so far`
                : 'Pull new mail from IMAP'
            }
            busy={sync?.running}
            onClick={onSync}
          />
          <QuickAction
            title={classify?.running ? 'Classifying…' : 'Classify with AI'}
            subtitle={
              classify && classify.pending > 0
                ? `${classify.pending.toLocaleString()} pending`
                : 'All messages classified'
            }
            busy={classify?.running}
            disabled={!classify || classify.pending === 0}
            onClick={onClassify}
          />
        </div>
        {(sync?.error || classify?.error) && (
          <p className="mt-2 text-[11px] text-[var(--color-negative)]">{sync?.error || classify?.error}</p>
        )}
      </section>
    </aside>
  )
}

const TONES: Record<string, string> = {
  slate: 'text-[var(--color-text)]',
  indigo: 'text-[var(--color-accent-2)]',
  amber: 'text-[var(--color-tint-orange)]',
  rose: 'text-rose-600',
}

function StatTile({
  label,
  value,
  tone,
}: {
  label: string
  value: number | undefined
  tone: keyof typeof TONES | string
}) {
  return (
    <div className="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2">
      <p className={`text-lg font-semibold ${TONES[tone] ?? TONES.slate}`}>
        {value === undefined ? '—' : value.toLocaleString()}
      </p>
      <p className="text-[11px] text-[var(--color-text-muted)]">{label}</p>
    </div>
  )
}

function QuickAction({
  title,
  subtitle,
  onClick,
  busy,
  disabled,
}: {
  title: string
  subtitle: string
  onClick: () => void
  busy?: boolean
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy || disabled}
      className="flex items-center gap-2 rounded-lg border border-[var(--color-border)] px-3 py-2 text-left transition hover:bg-[var(--color-bg)] disabled:opacity-60"
    >
      {busy && <Spinner className="h-3.5 w-3.5 shrink-0 text-[var(--color-accent-2)]" />}
      <span className="min-w-0">
        <span className="block truncate text-xs font-medium text-[var(--color-text)]">{title}</span>
        <span className="block truncate text-[11px] text-[var(--color-text-subtle)]">{subtitle}</span>
      </span>
    </button>
  )
}
