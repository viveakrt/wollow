import type { MouseEvent } from 'react'
import { ACTION_LABELS, type MessageSummary } from '../types'
import { PRIORITY_STYLES, categoryStyle } from '../theme/categories'

interface MessageRowProps {
  accountId: number
  message: MessageSummary
  onOpen: () => void
  onToggleFlag: () => void
  onDelete: () => void
  onSelectCategory?: (category: string) => void
}

export function MessageRow({
  message,
  onOpen,
  onToggleFlag,
  onDelete,
  onSelectCategory,
}: MessageRowProps) {
  function stop(event: MouseEvent) {
    event.stopPropagation()
  }

  const priority = message.priority
  const showAction = message.action && message.action !== 'no_action' && message.action !== 'read_only'
  const cat = message.category ? categoryStyle(message.category) : null

  return (
    <div
      onClick={onOpen}
      className="group flex cursor-pointer items-start gap-3 border-b border-[var(--color-border)] px-4 py-3 transition hover:bg-[var(--color-bg)]"
    >
      {/* Priority stripe doubles as the unread indicator's anchor. */}
      <span
        aria-hidden
        title={priority ? `Priority: ${priority}` : undefined}
        className={`mt-1.5 h-8 w-1 shrink-0 rounded-full ${priorityBar(priority)}`}
      />

      <button
        type="button"
        onClick={(e) => {
          stop(e)
          onToggleFlag()
        }}
        title={message.flagged ? 'Unflag' : 'Flag'}
        aria-label={message.flagged ? 'Unflag message' : 'Flag message'}
        className="mt-0.5 shrink-0 rounded p-1 text-[var(--color-text-subtle)] transition hover:bg-[var(--color-border)] hover:text-[var(--color-tint-orange)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]"
      >
        <StarIcon filled={message.flagged} />
      </button>

      <div className="min-w-0 flex-1">
        <div className="flex items-baseline justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <p
              className={`truncate text-sm ${
                message.seen ? 'font-normal text-[var(--color-text)]' : 'font-semibold text-[var(--color-text)]'
              }`}
            >
              {message.subject || '(no subject)'}
            </p>
            {cat && (
              <button
                type="button"
                onClick={(e) => {
                  stop(e)
                  if (message.category) onSelectCategory?.(message.category)
                }}
                className="shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium transition hover:brightness-95"
                style={{ backgroundColor: cat.bg, color: cat.fg }}
                title={message.subcategory ? `${cat.label} · ${message.subcategory}` : cat.label}
              >
                {cat.label}
              </button>
            )}
            {showAction && (
              <span className="shrink-0 rounded-full border border-[var(--color-border-strong)] px-2 py-0.5 text-[11px] font-medium text-[var(--color-text-muted)]">
                {ACTION_LABELS[message.action as string] ?? message.action}
              </span>
            )}
          </div>
          <span className="shrink-0 text-xs text-[var(--color-text-subtle)]">{formatDate(message.date)}</span>
        </div>

        <p className="mt-0.5 truncate text-sm text-[var(--color-text-muted)]">{message.from}</p>

        {(message.aiSummary || message.snippet) && (
          <p className="mt-1 truncate text-xs text-[var(--color-text-subtle)]">
            {message.aiSummary || message.snippet}
          </p>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-1">
        <button
          type="button"
          onClick={(e) => {
            stop(e)
            onDelete()
          }}
          title="Delete"
          aria-label="Delete message"
          className="rounded p-1.5 text-[var(--color-text-subtle)] transition hover:bg-[var(--color-negative-tint)] hover:text-[var(--color-negative)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-negative)]"
        >
          <TrashIcon />
        </button>
      </div>
    </div>
  )
}

function priorityBar(priority: string | undefined): string {
  if (!priority) return 'bg-transparent'
  return PRIORITY_STYLES[priority]?.bar ?? 'bg-transparent'
}

function formatDate(date: string): string {
  const d = new Date(date)
  if (Number.isNaN(d.getTime())) return date
  const now = new Date()
  const sameYear = d.getFullYear() === now.getFullYear()
  return d.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: sameYear ? undefined : 'numeric',
  })
}

function StarIcon({ filled }: { filled: boolean }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill={filled ? 'currentColor' : 'none'}
      stroke="currentColor"
      strokeWidth="1.75"
      className={`h-4 w-4 ${filled ? 'text-[var(--color-tint-orange)]' : ''}`}
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M11.48 3.499a.562.562 0 0 1 1.04 0l2.125 5.111a.563.563 0 0 0 .475.345l5.518.442c.499.04.701.663.321.988l-4.204 3.602a.563.563 0 0 0-.182.557l1.285 5.385a.562.562 0 0 1-.84.61l-4.725-2.885a.562.562 0 0 0-.586 0L6.98 21.539a.562.562 0 0 1-.84-.61l1.285-5.385a.562.562 0 0 0-.182-.557l-4.204-3.602a.562.562 0 0 1 .321-.988l5.518-.442a.563.563 0 0 0 .475-.345L11.48 3.5Z"
      />
    </svg>
  )
}

function TrashIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" className="h-4 w-4">
      <path strokeLinecap="round" strokeLinejoin="round" d="m14.74 9-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 0 1-2.244 2.077H8.084a2.25 2.25 0 0 1-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 0 0-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 0 1 3.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 0 0-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 0 0-7.5 0" />
    </svg>
  )
}
