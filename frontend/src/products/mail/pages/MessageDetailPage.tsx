import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Sparkles, Star, Trash2 } from 'lucide-react'
import clsx from 'clsx'
import {
  deleteMessage,
  getMessage,
  messagePartUrl,
  setMessageFlag,
  summarizeMessage,
} from '../api'
import { Spinner } from '../../../platform/components/Spinner'
import { MoneyLinkChip } from '../components/MoneyLinkChip'
import { EmailBody } from '../components/EmailBody'
import { AttachmentList } from '../components/AttachmentList'
import { ApiError } from '../../../platform/api/client'
import type { Attachment } from '../types'

const DEFAULT_FOLDER = 'INBOX'

export function MessageDetailPage() {
  const { accountId, messageId } = useParams<{ accountId: string; messageId: string }>()
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const accountIdNum = Number(accountId)
  const folder = searchParams.get('folder') || DEFAULT_FOLDER
  const [summary, setSummary] = useState<string | null>(null)

  const messageQuery = useQuery({
    queryKey: ['message', accountIdNum, messageId, folder],
    queryFn: () => getMessage(accountIdNum, messageId as string, folder),
    enabled: Boolean(accountId) && Boolean(messageId),
  })

  const message = messageQuery.data

  // Opening a message marks it read server-side, so the list behind it is now
  // stale. (v5 dropped useQuery's onSuccess; this is the documented
  // replacement.)
  const loaded = messageQuery.isSuccess
  useEffect(() => {
    if (loaded) queryClient.invalidateQueries({ queryKey: ['messages', accountIdNum, folder] })
  }, [loaded, queryClient, accountIdNum, folder])

  const deleteMutation = useMutation({
    mutationFn: () => deleteMessage(accountIdNum, messageId as string, folder),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['messages', accountIdNum, folder] })
      navigate('/mail', { replace: true })
    },
  })

  const flagMutation = useMutation({
    mutationFn: (value: boolean) =>
      setMessageFlag(accountIdNum, messageId as string, folder, '\\Flagged', value),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['message', accountIdNum, messageId, folder] })
      queryClient.invalidateQueries({ queryKey: ['messages', accountIdNum, folder] })
    },
  })

  const summarizeMutation = useMutation({
    mutationFn: () => summarizeMessage(accountIdNum, messageId as string, folder),
    onSuccess: (result) => setSummary(result.summary),
  })

  const partUrl = useCallback(
    (attachment: Attachment, download: boolean) =>
      messagePartUrl(accountIdNum, messageId as string, folder, attachment.partId, download),
    [accountIdNum, messageId, folder],
  )

  // cid: references address a part by Content-ID rather than part number; the
  // API resolves either, so no manifest lookup is needed here.
  const resolveCid = useCallback(
    (contentId: string) => messagePartUrl(accountIdNum, messageId as string, folder, contentId),
    [accountIdNum, messageId, folder],
  )

  return (
    <div className="mx-auto h-full w-full max-w-5xl overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-3xl">
        <Link
          to="/mail"
          className="mb-4 inline-flex items-center gap-1 text-sm font-medium text-[var(--color-accent-2)] hover:text-[var(--color-accent)]"
        >
          <ArrowLeft size={16} />
          Back to inbox
        </Link>

        {messageQuery.isLoading && (
          <div className="flex justify-center py-16">
            <Spinner className="h-6 w-6 text-[var(--color-accent-2)]" />
          </div>
        )}

        {messageQuery.isError && (
          <div className="rounded-lg bg-[var(--color-negative-tint)] px-4 py-3 text-sm text-[var(--color-negative)]">
            {messageQuery.error instanceof ApiError
              ? messageQuery.error.message
              : 'Failed to load message.'}
          </div>
        )}

        {message && (
          <div className="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] shadow-sm">
            <div className="flex items-start justify-between gap-4 border-b border-[var(--color-border)] px-6 py-5">
              <div className="min-w-0">
                <h1 className="text-lg font-semibold text-[var(--color-text)]">
                  {message.subject || '(no subject)'}
                </h1>
                <p className="mt-1 truncate text-sm text-[var(--color-text-muted)]">{message.from}</p>
                {message.to && (
                  <p className="mt-0.5 truncate text-xs text-[var(--color-text-subtle)]">
                    to {message.to}
                  </p>
                )}
                <p className="mt-0.5 text-xs text-[var(--color-text-subtle)]">
                  {formatDate(message.date)}
                </p>
                {message.moneyLink && (
                  <div className="mt-3">
                    <MoneyLinkChip link={message.moneyLink} />
                  </div>
                )}
              </div>

              <div className="flex shrink-0 items-center gap-1.5">
                <IconButton
                  label={message.flagged ? 'Unstar' : 'Star'}
                  pending={flagMutation.isPending}
                  active={message.flagged}
                  onClick={() => flagMutation.mutate(!message.flagged)}
                >
                  <Star size={16} fill={message.flagged ? 'currentColor' : 'none'} />
                </IconButton>
                <IconButton
                  label="Summarize"
                  pending={summarizeMutation.isPending}
                  onClick={() => summarizeMutation.mutate()}
                >
                  <Sparkles size={16} />
                </IconButton>
                <IconButton
                  label="Delete"
                  destructive
                  pending={deleteMutation.isPending}
                  onClick={() => deleteMutation.mutate()}
                >
                  <Trash2 size={16} />
                </IconButton>
              </div>
            </div>

            {(summary || summarizeMutation.isError) && (
              <div className="border-b border-[var(--color-border)] px-6 py-4">
                {summary ? (
                  <div className="rounded-lg bg-[var(--color-accent-tint)] px-4 py-3">
                    <p className="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--color-accent-2)]">
                      AI summary
                    </p>
                    <p className="text-sm text-[var(--color-text)]">{summary}</p>
                  </div>
                ) : (
                  <p className="text-sm text-[var(--color-negative)]">
                    {summarizeMutation.error instanceof ApiError
                      ? summarizeMutation.error.message
                      : 'Could not summarize this message.'}
                  </p>
                )}
              </div>
            )}

            <div className="px-6 py-5">
              {message.bodyHtml || message.bodyText ? (
                <EmailBody
                  html={message.bodyHtml}
                  text={message.bodyText}
                  resolveCid={resolveCid}
                  messageKey={`${accountIdNum}:${folder}:${messageId}`}
                />
              ) : (
                <p className="text-sm italic text-[var(--color-text-muted)]">
                  This message has no body content.
                </p>
              )}
            </div>

            <AttachmentList attachments={message.attachments ?? []} urlFor={partUrl} />
          </div>
        )}
      </div>
    </div>
  )
}

function IconButton({
  label,
  onClick,
  pending,
  active,
  destructive,
  children,
}: {
  label: string
  onClick: () => void
  pending?: boolean
  active?: boolean
  destructive?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      onClick={onClick}
      disabled={pending}
      className={clsx(
        'flex h-8 w-8 items-center justify-center rounded-lg border transition disabled:opacity-50',
        active
          ? 'border-[var(--color-warning,#f59e0b)] text-[var(--color-warning,#f59e0b)]'
          : 'border-[var(--color-border-strong)] text-[var(--color-text)]',
        destructive
          ? 'hover:border-[var(--color-negative)] hover:bg-[var(--color-negative-tint)] hover:text-[var(--color-negative)]'
          : 'hover:bg-[var(--color-hover)]',
      )}
    >
      {pending ? <Spinner className="h-4 w-4" /> : children}
    </button>
  )
}

function formatDate(date: string): string {
  const d = new Date(date)
  if (Number.isNaN(d.getTime())) return date
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}
