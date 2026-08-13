import { Link, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteMessage, getMessage } from '../api'
import { Spinner } from '../../../platform/components/Spinner'
import { ApiError } from '../../../platform/api/client'

const FOLDER = 'INBOX'

export function MessageDetailPage() {
  const { accountId, messageId } = useParams<{ accountId: string; messageId: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const accountIdNum = Number(accountId)

  const messageQuery = useQuery({
    queryKey: ['message', accountIdNum, messageId, FOLDER],
    queryFn: () => getMessage(accountIdNum, messageId as string, FOLDER),
    enabled: Boolean(accountId) && Boolean(messageId),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteMessage(accountIdNum, messageId as string, FOLDER),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['messages', accountIdNum, FOLDER] })
      navigate('/mail', { replace: true })
    },
  })

  return (
    <div className="mx-auto h-full w-full max-w-5xl overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-3xl">
        <Link to="/mail" className="mb-4 inline-flex items-center gap-1 text-sm font-medium text-[var(--color-accent-2)] hover:text-[var(--color-accent)]">
          <BackIcon />
          Back to inbox
        </Link>

        {messageQuery.isLoading && (
          <div className="flex justify-center py-16">
            <Spinner className="h-6 w-6 text-[var(--color-accent-2)]" />
          </div>
        )}

        {messageQuery.isError && (
          <div className="rounded-lg bg-[var(--color-negative-tint)] px-4 py-3 text-sm text-[var(--color-negative)]">
            {messageQuery.error instanceof ApiError ? messageQuery.error.message : 'Failed to load message.'}
          </div>
        )}

        {messageQuery.data && (
          <div className="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] shadow-sm">
            <div className="flex items-start justify-between gap-4 border-b border-[var(--color-border)] px-6 py-5">
              <div className="min-w-0">
                <h1 className="text-lg font-semibold text-[var(--color-text)]">{messageQuery.data.subject || '(no subject)'}</h1>
                <p className="mt-1 text-sm text-[var(--color-text-muted)]">{messageQuery.data.from}</p>
                <p className="mt-0.5 text-xs text-[var(--color-text-subtle)]">{formatDate(messageQuery.data.date)}</p>
              </div>
              <button
                type="button"
                onClick={() => deleteMutation.mutate()}
                disabled={deleteMutation.isPending}
                className="flex shrink-0 items-center gap-1.5 rounded-lg border border-[var(--color-border-strong)] px-3 py-1.5 text-sm font-medium text-[var(--color-text)] transition hover:border-[var(--color-negative)] hover:bg-[var(--color-negative-tint)] hover:text-[var(--color-negative)] disabled:opacity-50"
              >
                {deleteMutation.isPending ? <Spinner className="h-4 w-4" /> : <TrashIcon />}
                Delete
              </button>
            </div>

            <div className="px-6 py-5">
              {messageQuery.data.bodyText ? (
                <pre className="whitespace-pre-wrap break-words font-sans text-sm leading-relaxed text-[var(--color-text)]">
                  {messageQuery.data.bodyText}
                </pre>
              ) : messageQuery.data.bodyHtml ? (
                <p className="text-sm italic text-[var(--color-text-muted)]">This message has no plain-text body.</p>
              ) : (
                <p className="text-sm italic text-[var(--color-text-muted)]">This message has no body content.</p>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function formatDate(date: string): string {
  const d = new Date(date)
  if (Number.isNaN(d.getTime())) return date
  return d.toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  })
}

function BackIcon() {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" className="h-4 w-4">
      <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18" />
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
