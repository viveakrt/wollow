import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertCircle, Archive, Loader2, Mail, MailX, Search, Trash2 } from 'lucide-react'
import {
  bulkArchiveSenders,
  bulkDeleteSenders,
  bulkFlagSenders,
  getAccounts,
  getBulkSenderStatus,
  getSenders,
  markSenderUnsubscribed,
  resubscribeSender,
  unsubscribeSender,
  type Sender,
} from '../api'
import { Sidebar } from '../components/MailSidebar'
import { BulkJobBanner } from '../components/BulkJobBanner'
import { Spinner } from '../../../platform/components/Spinner'
import { ApiError } from '../../../platform/api/client'

const FOLDER = 'INBOX'

// Per-row state for the unsubscribe flow, keyed by sender email. Separate
// from the senders query because it tracks an in-flight action and, for the
// mailto fallback, a "did you actually send it?" prompt the query has no
// concept of.
type UnsubState =
  | { kind: 'loading' }
  | { kind: 'mailtoPending'; mailto: string }
  | { kind: 'error'; message: string }

export function SendersPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [selectedAccountId, setSelectedAccountId] = useState<number | null>(null)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [unsubState, setUnsubState] = useState<Record<string, UnsubState>>({})
  // Whether a bulk job is being watched. A failure that only stops the spinner
  // is indistinguishable from one that did nothing, which is exactly how a
  // timing-out delete used to present — so the job's progress and its outcome
  // are both rendered.
  const [bulkWatching, setBulkWatching] = useState(false)
  // An error from *starting* the job, as opposed to one the job itself hit.
  const [bulkError, setBulkError] = useState<string | null>(null)
  // A finished job's banner stays until dismissed; the status itself is kept,
  // so this is separate from the data rather than a refetch.
  const [bulkDismissed, setBulkDismissed] = useState(false)

  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: getAccounts })
  const accounts = accountsQuery.data ?? []

  useEffect(() => {
    if (selectedAccountId === null && accounts.length > 0) {
      setSelectedAccountId(accounts[0].id)
    }
  }, [accounts, selectedAccountId])

  const sendersQuery = useQuery({
    queryKey: ['senders', selectedAccountId],
    queryFn: () => getSenders(selectedAccountId as number, FOLDER),
    enabled: selectedAccountId !== null,
  })

  const senders: Sender[] = sendersQuery.data ?? []

  const filteredSenders = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return senders
    return senders.filter(
      (s) =>
        s.email.toLowerCase().includes(q) ||
        s.name.toLowerCase().includes(q) ||
        s.domain.toLowerCase().includes(q),
    )
  }, [senders, search])

  // Selections that scrolled out of the current search can't stay selected.
  useEffect(() => {
    const emails = new Set(filteredSenders.map((s) => s.email))
    setSelected((prev) => {
      const next = new Set<string>()
      prev.forEach((email) => {
        if (emails.has(email)) next.add(email)
      })
      return next.size === prev.size ? prev : next
    })
  }, [filteredSenders])

  const invalidateSenders = () => {
    queryClient.invalidateQueries({ queryKey: ['senders', selectedAccountId] })
    // Bulk actions remove or re-flag indexed messages, so the inbox list and
    // its smart-view counts are stale too.
    queryClient.invalidateQueries({ queryKey: ['messages', selectedAccountId] })
    queryClient.invalidateQueries({ queryKey: ['insights', selectedAccountId] })
  }

  // Bulk actions run detached on the server, so the page polls for progress.
  // Polling is on only while a job runs, so an idle Senders page is quiet.
  const bulkStatusQuery = useQuery({
    queryKey: ['senders-bulk', selectedAccountId],
    queryFn: () => getBulkSenderStatus(selectedAccountId as number),
    enabled: selectedAccountId !== null && bulkWatching,
    refetchInterval: (query) => (query.state.data?.running ? 600 : false),
  })

  const bulkStatus = bulkStatusQuery.data
  const bulkRunning = Boolean(bulkStatus?.running)

  // The list is only refreshed once the job stops, not on every poll: a
  // refetch per tick would fight the job for the database and make the page
  // flicker for the whole run.
  useEffect(() => {
    if (bulkWatching && bulkStatus && !bulkStatus.running) {
      setBulkWatching(false)
      invalidateSenders()
    }
    // invalidateSenders closes over the account id, which is in the deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bulkWatching, bulkStatus?.running, bulkStatus?.finishedAt, selectedAccountId])

  /** Starts watching a job the server just accepted. */
  const onBulkStarted = () => {
    setSelected(new Set())
    setBulkError(null)
    setBulkDismissed(false)
    setBulkWatching(true)
    bulkStatusQuery.refetch()
  }

  const onBulkError = (err: unknown) =>
    setBulkError(err instanceof ApiError ? err.message : 'The action could not be started.')

  const bulkFlagMutation = useMutation({
    mutationFn: ({ emails, flag, value }: { emails: string[]; flag: '\\Seen' | '\\Flagged'; value: boolean }) =>
      bulkFlagSenders(selectedAccountId as number, emails, flag, value),
    onSuccess: onBulkStarted,
    onError: onBulkError,
  })
  const bulkArchiveMutation = useMutation({
    mutationFn: (emails: string[]) => bulkArchiveSenders(selectedAccountId as number, emails),
    onSuccess: onBulkStarted,
    onError: onBulkError,
  })
  const bulkDeleteMutation = useMutation({
    mutationFn: (emails: string[]) => bulkDeleteSenders(selectedAccountId as number, emails),
    onSuccess: onBulkStarted,
    onError: onBulkError,
  })

  const bulkBusy =
    bulkRunning ||
    bulkFlagMutation.isPending ||
    bulkArchiveMutation.isPending ||
    bulkDeleteMutation.isPending

  function toggleRow(email: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(email)) next.delete(email)
      else next.add(email)
      return next
    })
  }

  function toggleAll() {
    setSelected((prev) =>
      prev.size === filteredSenders.length ? new Set() : new Set(filteredSenders.map((s) => s.email)),
    )
  }

  const selectedCount = senders
    .filter((s) => selected.has(s.email))
    .reduce((sum, s) => sum + s.count, 0)

  function handleBulkDelete() {
    if (selected.size === 0) return
    if (
      !confirm(
        `Delete all ${selectedCount} message${selectedCount !== 1 ? 's' : ''} from ${selected.size} sender${
          selected.size !== 1 ? 's' : ''
        }?\n\nThey move to Trash on your mail server, so they leave Gmail (or whichever provider you use) — not just this app.`,
      )
    )
      return
    bulkDeleteMutation.mutate([...selected])
  }

  async function handleUnsubscribe(email: string) {
    if (!selectedAccountId) return
    setUnsubState((prev) => ({ ...prev, [email]: { kind: 'loading' } }))
    try {
      const result = await unsubscribeSender(selectedAccountId, email)
      setUnsubState((prev) => {
        const next = { ...prev }
        delete next[email]
        return next
      })
      if (result.status === 'manual' && result.mailto) {
        window.location.href = result.mailto
        setUnsubState((prev) => ({ ...prev, [email]: { kind: 'mailtoPending', mailto: result.mailto! } }))
      } else {
        invalidateSenders()
      }
    } catch (err) {
      const message = err instanceof ApiError ? err.message : 'Unsubscribe failed'
      setUnsubState((prev) => ({ ...prev, [email]: { kind: 'error', message } }))
    }
  }

  async function handleConfirmMailtoSent(email: string) {
    if (!selectedAccountId) return
    await markSenderUnsubscribed(selectedAccountId, email)
    setUnsubState((prev) => {
      const next = { ...prev }
      delete next[email]
      return next
    })
    invalidateSenders()
  }

  async function handleMarkUnsubscribedAnyway(email: string) {
    if (!selectedAccountId) return
    await markSenderUnsubscribed(selectedAccountId, email)
    setUnsubState((prev) => {
      const next = { ...prev }
      delete next[email]
      return next
    })
    invalidateSenders()
  }

  async function handleResubscribe(email: string) {
    if (!selectedAccountId) return
    await resubscribeSender(selectedAccountId, email)
    invalidateSenders()
  }

  if (accountsQuery.isLoading) {
    return (
      <div className="flex h-full w-full items-center justify-center bg-[var(--color-bg)]">
        <Spinner className="h-6 w-6 text-[var(--color-accent-2)]" />
      </div>
    )
  }

  if (accounts.length === 0) {
    return (
      <div className="flex h-full w-full flex-col items-center justify-center bg-[var(--color-bg)] px-6 text-center">
        <p className="text-lg font-medium text-[var(--color-text)]">No accounts connected yet</p>
        <p className="mt-1 text-sm text-[var(--color-text-muted)]">Connect a mailbox to get started.</p>
      </div>
    )
  }

  return (
    <div className="flex h-full w-full overflow-hidden bg-[var(--color-bg)]">
      <Sidebar
        accounts={accounts}
        selectedAccountId={selectedAccountId}
        onSelectAccount={setSelectedAccountId}
        insights={undefined}
        activeView={null}
        onSelectView={() => {}}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-5 py-3">
          <div className="flex items-center gap-3">
            <h1 className="text-xl font-semibold text-[var(--color-text)]">Senders</h1>
            <span className="text-sm text-[var(--color-text-muted)]">
              {filteredSenders.length} of {senders.length}
            </span>
            <div className="relative ml-auto w-full max-w-xs">
              <Search
                size={15}
                className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-text-subtle)]"
              />
              <input
                type="search"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Filter by email, name, or domain…"
                className="w-full rounded-lg border border-[var(--color-border-strong)] bg-[var(--color-bg)] py-1.5 pl-8 pr-3 text-sm placeholder:text-[var(--color-text-subtle)] focus:border-[var(--color-accent)] focus:bg-[var(--color-surface)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
              />
            </div>
          </div>
        </header>

        {bulkError && (
          <div className="flex items-center gap-2 border-b border-[var(--color-negative)] bg-[var(--color-negative-tint)] px-5 py-2.5 text-sm text-[var(--color-negative)]">
            <AlertCircle size={15} />
            <span className="min-w-0 flex-1">{bulkError}</span>
            <button
              type="button"
              onClick={() => setBulkError(null)}
              className="shrink-0 rounded px-2 py-0.5 text-xs font-medium opacity-70 hover:opacity-100"
            >
              Dismiss
            </button>
          </div>
        )}

        {bulkStatus && !bulkError && !bulkDismissed && (
          <BulkJobBanner status={bulkStatus} onDismiss={() => setBulkDismissed(true)} />
        )}

        <div className="min-h-0 flex-1 overflow-y-auto">
          {sendersQuery.isLoading && (
            <div className="flex justify-center py-16">
              <Spinner className="h-6 w-6 text-[var(--color-accent-2)]" />
            </div>
          )}

          {!sendersQuery.isLoading && senders.length === 0 && (
            <div className="px-4 py-20 text-center text-sm text-[var(--color-text-muted)]">
              No messages indexed yet. Hit Sync to pull your mailbox in.
            </div>
          )}

          {!sendersQuery.isLoading && senders.length > 0 && (
            <>
              <div className="flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2">
                <input
                  type="checkbox"
                  checked={filteredSenders.length > 0 && selected.size === filteredSenders.length}
                  onChange={toggleAll}
                  aria-label={selected.size === filteredSenders.length ? 'Deselect all' : 'Select all'}
                  className="h-3.5 w-3.5 accent-[var(--color-accent)]"
                />
                {selected.size > 0 ? (
                  <>
                    <span className="text-sm font-medium text-[var(--color-text)]">
                      {selected.size} sender{selected.size !== 1 ? 's' : ''} · {selectedCount} message
                      {selectedCount !== 1 ? 's' : ''}
                    </span>
                    <div className="flex-1" />
                    <button
                      type="button"
                      onClick={() => bulkFlagMutation.mutate({ emails: [...selected], flag: '\\Seen', value: true })}
                      disabled={bulkBusy}
                      className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                    >
                      Mark read
                    </button>
                    <button
                      type="button"
                      onClick={() => bulkFlagMutation.mutate({ emails: [...selected], flag: '\\Seen', value: false })}
                      disabled={bulkBusy}
                      className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                    >
                      Mark unread
                    </button>
                    <button
                      type="button"
                      onClick={() => bulkArchiveMutation.mutate([...selected])}
                      disabled={bulkBusy}
                      className="flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                    >
                      {bulkArchiveMutation.isPending ? (
                        <Loader2 size={13} className="animate-spin" />
                      ) : (
                        <Archive size={13} />
                      )}
                      Archive all
                    </button>
                    <button
                      type="button"
                      onClick={handleBulkDelete}
                      disabled={bulkBusy}
                      className="flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-negative)] transition hover:bg-[var(--color-negative-tint)] disabled:opacity-50"
                    >
                      {bulkDeleteMutation.isPending ? (
                        <Loader2 size={13} className="animate-spin" />
                      ) : (
                        <Trash2 size={13} />
                      )}
                      Delete all
                    </button>
                    <button
                      type="button"
                      onClick={() => setSelected(new Set())}
                      className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-subtle)] transition hover:bg-[var(--color-bg)]"
                    >
                      Clear
                    </button>
                  </>
                ) : (
                  <span className="text-xs text-[var(--color-text-subtle)]">Select all</span>
                )}
              </div>

              <div className="divide-y divide-[var(--color-border)] bg-[var(--color-surface)]">
                {filteredSenders.map((sender) => {
                  const state = unsubState[sender.email]
                  const isUnsubscribed = Boolean(sender.unsubscribedAt)

                  return (
                    <div key={sender.email} className="flex items-start gap-3 px-4 py-4">
                      <input
                        type="checkbox"
                        checked={selected.has(sender.email)}
                        onChange={() => toggleRow(sender.email)}
                        className="mt-1 h-3.5 w-3.5 shrink-0 accent-[var(--color-accent)]"
                      />

                      <button
                        onClick={() =>
                          navigate(
                            `/mail?accountId=${selectedAccountId}&sender=${encodeURIComponent(sender.email)}`,
                          )
                        }
                        className="min-w-0 flex-1 text-left"
                      >
                        <div className="flex items-center gap-2">
                          <Mail size={16} className="shrink-0 text-[var(--color-text-muted)]" />
                          <div className="min-w-0">
                            {sender.name && (
                              <p className="truncate font-medium text-[var(--color-text)]">{sender.name}</p>
                            )}
                            <p className="truncate text-sm text-[var(--color-text-muted)]">{sender.email}</p>
                          </div>
                        </div>
                      </button>

                      <div className="flex shrink-0 flex-col items-end gap-1.5">
                        <div className="flex items-center gap-2">
                          <span className="rounded-lg bg-[var(--color-hover)] px-2.5 py-1 text-sm font-semibold text-[var(--color-text)]">
                            {sender.count}
                          </span>
                          {sender.lastSeen && (
                            <span className="text-xs text-[var(--color-text-subtle)]">
                              {new Date(sender.lastSeen).toLocaleDateString()}
                            </span>
                          )}
                        </div>

                        {isUnsubscribed ? (
                          <div className="flex items-center gap-1.5">
                            <span className="flex items-center gap-1 rounded-full bg-[var(--color-hover)] px-2 py-0.5 text-[11px] font-medium text-[var(--color-text-muted)]">
                              <MailX size={11} />
                              Unsubscribed
                            </span>
                            <button
                              type="button"
                              onClick={() => handleResubscribe(sender.email)}
                              className="text-[11px] text-[var(--color-text-subtle)] underline-offset-2 hover:text-[var(--color-text-muted)] hover:underline"
                            >
                              Undo
                            </button>
                          </div>
                        ) : state?.kind === 'loading' ? (
                          <span className="flex items-center gap-1 text-xs text-[var(--color-text-subtle)]">
                            <Loader2 size={12} className="animate-spin" />
                            Unsubscribing…
                          </span>
                        ) : state?.kind === 'mailtoPending' ? (
                          <div className="flex items-center gap-1.5 text-xs">
                            <span className="text-[var(--color-text-subtle)]">Opened your mail app</span>
                            <button
                              type="button"
                              onClick={() => handleConfirmMailtoSent(sender.email)}
                              className="font-medium text-[var(--color-accent-2)] hover:underline"
                            >
                              Mark done
                            </button>
                          </div>
                        ) : state?.kind === 'error' ? (
                          <div className="flex items-center gap-1.5 text-xs">
                            <span className="text-[var(--color-negative)]">{state.message}</span>
                            <button
                              type="button"
                              onClick={() => handleMarkUnsubscribedAnyway(sender.email)}
                              className="font-medium text-[var(--color-text-muted)] hover:underline"
                            >
                              Mark anyway
                            </button>
                          </div>
                        ) : (
                          <button
                            type="button"
                            onClick={() => handleUnsubscribe(sender.email)}
                            className="flex items-center gap-1 rounded-lg border border-[var(--color-border-strong)] px-2 py-1 text-[11px] font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] hover:text-[var(--color-text)]"
                          >
                            <MailX size={11} />
                            Unsubscribe
                          </button>
                        )}
                      </div>

                      <button
                        type="button"
                        onClick={() => bulkDeleteMutation.mutate([sender.email])}
                        title="Delete all messages from this sender"
                        aria-label="Delete all messages from this sender"
                        className="mt-1 shrink-0 rounded p-1.5 text-[var(--color-text-subtle)] transition hover:bg-[var(--color-negative-tint)] hover:text-[var(--color-negative)]"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  )
                })}
              </div>
            </>
          )}
        </div>
      </main>
    </div>
  )
}
