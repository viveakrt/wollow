import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Search, Mail, Loader2 } from 'lucide-react'
import { api } from '../../api'
import { getMessages } from '../../../mail/api'
import type { MessageSummary } from '../../../mail/types'
import { formatDate } from '../../lib/format'

const PAGE = 50

/**
 * Pick the email a parser will be defined on: one mailbox, filtered by
 * sender or search, newest first. The row shows what Money already made of
 * each message, so an unread one is easy to spot.
 */
export function EmailPicker({
  onPick,
}: {
  onPick: (mailAccountId: number, uid: number) => void
}) {
  const [mailboxId, setMailboxId] = useState<number | null>(null)
  const [sender, setSender] = useState('')
  const [search, setSearch] = useState('')
  const [debounced, setDebounced] = useState({ sender: '', q: '' })

  useEffect(() => {
    const t = setTimeout(() => setDebounced({ sender: sender.trim(), q: search.trim() }), 300)
    return () => clearTimeout(t)
  }, [sender, search])

  const mailboxes = useQuery({ queryKey: ['money', 'email-accounts'], queryFn: api.emailAccounts.list })
  const activeMailbox = mailboxId ?? mailboxes.data?.[0]?.id ?? null

  const messages = useQuery({
    queryKey: ['money', 'parser-picker', activeMailbox, debounced.sender, debounced.q],
    queryFn: () =>
      getMessages(activeMailbox as number, 'INBOX', PAGE, 0, {
        sender: debounced.sender || null,
        q: debounced.q || null,
      }),
    enabled: activeMailbox != null,
  })

  return (
    <div className="space-y-3">
      <div className="flex flex-col gap-2 sm:flex-row">
        <select
          value={activeMailbox ?? ''}
          onChange={(e) => setMailboxId(Number(e.target.value))}
          className={inputClass + ' sm:w-64'}
        >
          {(mailboxes.data ?? []).map((m) => (
            <option key={m.id} value={m.id}>
              {m.email}
            </option>
          ))}
        </select>
        <input
          value={sender}
          onChange={(e) => setSender(e.target.value)}
          placeholder="Exact sender address (optional)"
          className={inputClass + ' flex-1'}
        />
        <div className="relative flex-1">
          <Search
            size={14}
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-text-muted)]"
          />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search subject or sender"
            className={inputClass + ' pl-8'}
          />
        </div>
      </div>

      <div className="max-h-[32rem] overflow-y-auto rounded-lg border border-[var(--color-border)]">
        {mailboxes.isSuccess && mailboxes.data.length === 0 ? (
          <Empty text="No mailbox connected yet — connect one in Mail first." />
        ) : messages.isPending ? (
          <div className="flex items-center justify-center gap-2 py-10 text-sm text-[var(--color-text-muted)]">
            <Loader2 size={14} className="animate-spin" /> Loading messages…
          </div>
        ) : messages.isError ? (
          <Empty text="Could not load messages." />
        ) : messages.data.length === 0 ? (
          <Empty text="No messages match." />
        ) : (
          messages.data.map((m) => (
            <PickerRow
              key={m.id}
              message={m}
              onPick={() => onPick(activeMailbox as number, Number(m.id))}
            />
          ))
        )}
      </div>
    </div>
  )
}

function PickerRow({ message, onPick }: { message: MessageSummary; onPick: () => void }) {
  const link = message.moneyLink
  const status =
    link?.parsedAs === 'unrecognized'
      ? { label: 'No parser matched', tone: 'text-[var(--color-tint-orange)]' }
      : link?.parsedAs === 'pending_account'
        ? { label: 'Waiting for account', tone: 'text-[var(--color-tint-orange)]' }
        : link
          ? { label: 'Already read', tone: 'text-[var(--color-positive)]' }
          : null
  return (
    <button
      type="button"
      onClick={onPick}
      className="flex w-full items-start gap-3 border-b border-[var(--color-border)] px-4 py-3 text-left transition hover:bg-[var(--color-hover)]"
    >
      <Mail size={15} className="mt-0.5 shrink-0 text-[var(--color-text-muted)]" />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline justify-between gap-3">
          <p className="truncate text-sm font-medium">{message.subject || '(no subject)'}</p>
          <span className="shrink-0 text-xs text-[var(--color-text-subtle)]">{formatDate(message.date)}</span>
        </div>
        <p className="mt-0.5 truncate text-xs text-[var(--color-text-muted)]">{message.from}</p>
        <div className="mt-0.5 flex items-center gap-2">
          {status && <span className={`text-[11px] font-medium ${status.tone}`}>{status.label}</span>}
          {message.snippet && (
            <p className="truncate text-xs text-[var(--color-text-subtle)]">{message.snippet}</p>
          )}
        </div>
      </div>
    </button>
  )
}

function Empty({ text }: { text: string }) {
  return <div className="py-10 text-center text-sm text-[var(--color-text-muted)]">{text}</div>
}

const inputClass =
  'w-full px-3 py-2 bg-[var(--color-surface-2)] border border-[var(--color-border)] rounded-lg text-sm focus:outline-none focus:border-[var(--color-accent)]'
