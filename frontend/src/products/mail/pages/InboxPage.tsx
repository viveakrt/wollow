import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  bulkDeleteMessages,
  bulkSetMessageFlag,
  deleteMessage,
  getAccounts,
  getClassifyStatus,
  getInsights,
  getMessages,
  getSyncStatus,
  setMessageFlag,
  startClassify,
  startSync,
  type MessageFilters,
} from '../api'
import { MessageRow } from '../components/MessageRow'
import { Sidebar, SMART_VIEWS } from '../components/MailSidebar'
import { InsightsPanel } from '../components/InsightsPanel'
import { Spinner } from '../../../platform/components/Spinner'
import { ApiError } from '../../../platform/api/client'
import { categoryStyle } from '../theme/categories'
import type { MessageSummary } from '../types'

const PAGE_SIZE = 50
const FOLDER = 'INBOX'

export function InboxPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  // Read once on mount: a deep link from the Senders page ("view all mail
  // from this sender") arrives as ?accountId=&sender=, seeding these filters
  // the same way the rest of the page manages them from then on.
  const [searchParams] = useSearchParams()

  const [selectedAccountId, setSelectedAccountId] = useState<number | null>(() => {
    const raw = searchParams.get('accountId')
    const parsed = raw ? Number(raw) : NaN
    return Number.isFinite(parsed) ? parsed : null
  })
  const [view, setView] = useState<string | null>(null)
  const [category, setCategory] = useState<string | null>(null)
  const [sender, setSender] = useState<string | null>(() => searchParams.get('sender'))
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const loadMoreRef = useRef<HTMLDivElement | null>(null)

  const accountsQuery = useQuery({ queryKey: ['accounts'], queryFn: getAccounts })
  const accounts = useMemo(() => accountsQuery.data ?? [], [accountsQuery.data])

  useEffect(() => {
    if (selectedAccountId === null && accounts.length > 0) {
      setSelectedAccountId(accounts[0].id)
    }
  }, [accounts, selectedAccountId])

  // Debounce search so typing doesn't fire a query per keystroke.
  useEffect(() => {
    const t = setTimeout(() => setSearch(searchInput.trim()), 300)
    return () => clearTimeout(t)
  }, [searchInput])

  const filters: MessageFilters = { view, category, sender, q: search || null }
  const filterKey = JSON.stringify(filters)

  const messagesQuery = useInfiniteQuery({
    queryKey: ['messages', selectedAccountId, FOLDER, filterKey],
    queryFn: ({ pageParam }) =>
      getMessages(selectedAccountId as number, FOLDER, PAGE_SIZE, pageParam, filters),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      if (lastPage.length < PAGE_SIZE) return undefined
      return allPages.reduce((sum, page) => sum + page.length, 0)
    },
    enabled: selectedAccountId !== null,
  })

  const insightsQuery = useQuery({
    queryKey: ['insights', selectedAccountId],
    queryFn: () => getInsights(selectedAccountId as number, FOLDER),
    enabled: selectedAccountId !== null,
  })

  const syncStatusQuery = useQuery({
    queryKey: ['syncStatus', selectedAccountId],
    queryFn: () => getSyncStatus(selectedAccountId as number, FOLDER),
    enabled: selectedAccountId !== null,
    refetchInterval: (q) => (q.state.data?.running ? 3000 : 30000),
  })

  const classifyStatusQuery = useQuery({
    queryKey: ['classifyStatus', selectedAccountId],
    queryFn: () => getClassifyStatus(selectedAccountId as number),
    enabled: selectedAccountId !== null,
    refetchInterval: (q) => (q.state.data?.running ? 3000 : 30000),
  })

  // Background passes change the underlying data; refresh the list and the
  // whole-mailbox aggregates as they make progress.
  const syncRunning = syncStatusQuery.data?.running ?? false
  const classifiedCount = classifyStatusQuery.data?.classified ?? 0
  useEffect(() => {
    queryClient.invalidateQueries({ queryKey: ['messages', selectedAccountId] })
    queryClient.invalidateQueries({ queryKey: ['insights', selectedAccountId] })
  }, [syncRunning, classifiedCount, queryClient, selectedAccountId])

  const messages: MessageSummary[] = useMemo(
    () => messagesQuery.data?.pages.flat() ?? [],
    [messagesQuery.data],
  )

  // Rows that scrolled out of the current filter/page set can't stay selected.
  useEffect(() => {
    const ids = new Set(messages.map((m) => m.id))
    setSelected((prev) => {
      const next = new Set<string>()
      prev.forEach((id) => {
        if (ids.has(id)) next.add(id)
      })
      return next.size === prev.size ? prev : next
    })
  }, [messages])

  useEffect(() => {
    const el = loadMoreRef.current
    if (!el) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting && messagesQuery.hasNextPage && !messagesQuery.isFetchingNextPage) {
          messagesQuery.fetchNextPage()
        }
      },
      { rootMargin: '200px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [messagesQuery])

  function patchMessages(fn: (m: MessageSummary) => MessageSummary | null) {
    queryClient.setQueryData<{ pages: MessageSummary[][]; pageParams: unknown[] } | undefined>(
      ['messages', selectedAccountId, FOLDER, filterKey],
      (old) => {
        if (!old) return old
        return {
          ...old,
          pages: old.pages.map((page) => page.map(fn).filter((m): m is MessageSummary => m !== null)),
        }
      },
    )
  }

  const flagMutation = useMutation({
    mutationFn: ({ messageId, value }: { messageId: string; value: boolean }) =>
      setMessageFlag(selectedAccountId as number, messageId, FOLDER, '\\Flagged', value),
    onSuccess: (_d, v) => patchMessages((m) => (m.id === v.messageId ? { ...m, flagged: v.value } : m)),
  })

  const deleteMutation = useMutation({
    mutationFn: (messageId: string) => deleteMessage(selectedAccountId as number, messageId, FOLDER),
    onSuccess: (_d, messageId) => patchMessages((m) => (m.id === messageId ? null : m)),
  })

  const bulkDeleteMutation = useMutation({
    mutationFn: (ids: string[]) => bulkDeleteMessages(selectedAccountId as number, FOLDER, ids),
    onSuccess: (result, ids) => {
      const failed = new Set(result.failed ?? [])
      const succeeded = new Set(ids.filter((id) => !failed.has(id)))
      patchMessages((m) => (succeeded.has(m.id) ? null : m))
      setSelected(new Set())
    },
  })

  const bulkFlagMutation = useMutation({
    mutationFn: ({ ids, flag, value }: { ids: string[]; flag: '\\Seen' | '\\Flagged'; value: boolean }) =>
      bulkSetMessageFlag(selectedAccountId as number, FOLDER, ids, flag, value).then((result) => ({
        result,
        flag,
        value,
      })),
    onSuccess: ({ result, flag, value }, { ids }) => {
      const failed = new Set(result.failed ?? [])
      const succeeded = new Set(ids.filter((id) => !failed.has(id)))
      const key = flag === '\\Seen' ? 'seen' : 'flagged'
      patchMessages((m) => (succeeded.has(m.id) ? { ...m, [key]: value } : m))
      setSelected(new Set())
    },
  })

  const bulkBusy = bulkDeleteMutation.isPending || bulkFlagMutation.isPending

  function toggleSelectRow(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleSelectAll() {
    setSelected((prev) => (prev.size === messages.length ? new Set() : new Set(messages.map((m) => m.id))))
  }

  function handleBulkDelete() {
    if (selected.size === 0) return
    if (
      !confirm(
        `Delete ${selected.size} message${selected.size !== 1 ? 's' : ''}? They move to Trash on your mail server.`,
      )
    )
      return
    bulkDeleteMutation.mutate([...selected])
  }

  const syncMutation = useMutation({
    mutationFn: () => startSync(selectedAccountId as number, FOLDER),
    onSuccess: () => syncStatusQuery.refetch(),
  })

  const classifyMutation = useMutation({
    mutationFn: () => startClassify(selectedAccountId as number),
    onSuccess: () => classifyStatusQuery.refetch(),
  })

  function handleOpen(message: MessageSummary) {
    if (selectedAccountId === null) return
    // Opening the message is what marks it read now — the detail request sets
    // \Seen server-side and mirrors it into the index, so flagging it from here
    // would only cost a second IMAP session for the same result.
    navigate(`/mail/messages/${selectedAccountId}/${message.id}?folder=${encodeURIComponent(FOLDER)}`)
  }

  function resetFilters() {
    setView(null)
    setCategory(null)
    setSender(null)
    setSearchInput('')
    setSearch('')
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
        <Link
          to="/mail/accounts/new"
          className="mt-5 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-[var(--color-accent)]"
        >
          Connect account
        </Link>
      </div>
    )
  }

  const insights = insightsQuery.data
  const activeViewLabel =
    SMART_VIEWS.find((v) => v.id === view)?.label ??
    (view ? view.replace(/_/g, ' ') : 'Inbox')
  const hasFilters = Boolean(view || category || sender || search)

  return (
    <div className="flex h-full w-full overflow-hidden bg-[var(--color-bg)]">
      <Sidebar
        accounts={accounts}
        selectedAccountId={selectedAccountId}
        onSelectAccount={(id) => {
          setSelectedAccountId(id)
          resetFilters()
        }}
        insights={insights}
        activeView={view}
        onSelectView={(v) => {
          setView(v)
          setCategory(null)
          setSender(null)
        }}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="border-b border-[var(--color-border)] bg-[var(--color-surface)] px-5 py-3">
          <div className="flex items-center gap-3">
            <input
              type="search"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder="Search subject or sender…"
              className="w-full max-w-md rounded-lg border border-[var(--color-border-strong)] bg-[var(--color-bg)] px-3 py-1.5 text-sm placeholder:text-[var(--color-text-subtle)] focus:border-[var(--color-accent)] focus:bg-[var(--color-surface)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
            />
            <div className="ml-auto flex items-center gap-2">
              <button
                type="button"
                onClick={() => syncMutation.mutate()}
                disabled={syncStatusQuery.data?.running}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--color-border-strong)] bg-[var(--color-surface)] px-3 py-1.5 text-sm font-medium text-[var(--color-text)] transition hover:bg-[var(--color-bg)] disabled:opacity-60"
              >
                {syncStatusQuery.data?.running && <Spinner className="h-3.5 w-3.5" />}
                {syncStatusQuery.data?.running ? 'Syncing…' : 'Sync'}
              </button>
              <button
                type="button"
                onClick={() => classifyMutation.mutate()}
                disabled={
                  classifyStatusQuery.data?.running || (classifyStatusQuery.data?.pending ?? 0) === 0
                }
                className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-sm font-medium text-white shadow-sm transition hover:bg-[var(--color-accent)] disabled:opacity-60"
              >
                {classifyStatusQuery.data?.running && <Spinner className="h-3.5 w-3.5" />}
                Classify
              </button>
            </div>
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold capitalize text-[var(--color-text)]">{activeViewLabel}</h1>
            {insights && view === null && !category && !sender && (
              <span className="text-sm text-[var(--color-text-subtle)]">
                {insights.totals.unread.toLocaleString()} unread
              </span>
            )}

            {category && (
              <Chip label={categoryStyle(category).label} onClear={() => setCategory(null)} />
            )}
            {sender && <Chip label={sender} onClear={() => setSender(null)} />}
            {search && <Chip label={`“${search}”`} onClear={() => { setSearchInput(''); setSearch('') }} />}
            {hasFilters && (
              <button
                type="button"
                onClick={resetFilters}
                className="text-xs text-[var(--color-text-subtle)] underline-offset-2 hover:text-[var(--color-text-muted)] hover:underline"
              >
                Clear all
              </button>
            )}
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto">
          {messagesQuery.isLoading && (
            <div className="flex justify-center py-16">
              <Spinner className="h-6 w-6 text-[var(--color-accent-2)]" />
            </div>
          )}

          {messagesQuery.isError && (
            <div className="px-4 py-8 text-center text-sm text-[var(--color-negative)]">
              {messagesQuery.error instanceof ApiError
                ? messagesQuery.error.message
                : 'Failed to load messages.'}
            </div>
          )}

          {!messagesQuery.isLoading && !messagesQuery.isError && messages.length === 0 && (
            <div className="px-4 py-20 text-center text-sm text-[var(--color-text-muted)]">
              {hasFilters
                ? 'Nothing matches these filters.'
                : syncStatusQuery.data?.running
                  ? 'Syncing your mailbox…'
                  : 'No messages indexed yet. Hit Sync to pull your mailbox in.'}
            </div>
          )}

          {messages.length > 0 && (
            <div className="flex items-center gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-2">
              <input
                type="checkbox"
                checked={selected.size === messages.length}
                onChange={toggleSelectAll}
                aria-label={selected.size === messages.length ? 'Deselect all' : 'Select all'}
                className="h-3.5 w-3.5 accent-[var(--color-accent)]"
              />
              {selected.size > 0 ? (
                <>
                  <span className="text-sm font-medium text-[var(--color-text)]">{selected.size} selected</span>
                  <div className="flex-1" />
                  <button
                    type="button"
                    onClick={() => bulkFlagMutation.mutate({ ids: [...selected], flag: '\\Seen', value: true })}
                    disabled={bulkBusy}
                    className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                  >
                    Mark read
                  </button>
                  <button
                    type="button"
                    onClick={() => bulkFlagMutation.mutate({ ids: [...selected], flag: '\\Seen', value: false })}
                    disabled={bulkBusy}
                    className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                  >
                    Mark unread
                  </button>
                  <button
                    type="button"
                    onClick={() => bulkFlagMutation.mutate({ ids: [...selected], flag: '\\Flagged', value: true })}
                    disabled={bulkBusy}
                    className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                  >
                    Flag
                  </button>
                  <button
                    type="button"
                    onClick={() => bulkFlagMutation.mutate({ ids: [...selected], flag: '\\Flagged', value: false })}
                    disabled={bulkBusy}
                    className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-text-muted)] transition hover:bg-[var(--color-bg)] disabled:opacity-50"
                  >
                    Unflag
                  </button>
                  <button
                    type="button"
                    onClick={handleBulkDelete}
                    disabled={bulkBusy}
                    className="rounded-lg px-2.5 py-1 text-xs font-medium text-[var(--color-negative)] transition hover:bg-[var(--color-negative-tint)] disabled:opacity-50"
                  >
                    {bulkDeleteMutation.isPending ? <Spinner className="h-3 w-3" /> : 'Delete'}
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
          )}

          <div className="bg-[var(--color-surface)]">
            {messages.map((message) => (
              <MessageRow
                key={message.id}
                accountId={selectedAccountId as number}
                message={message}
                selected={selected.has(message.id)}
                onToggleSelect={() => toggleSelectRow(message.id)}
                onOpen={() => handleOpen(message)}
                onToggleFlag={() =>
                  flagMutation.mutate({ messageId: message.id, value: !message.flagged })
                }
                onDelete={() => deleteMutation.mutate(message.id)}
                onSelectCategory={setCategory}
              />
            ))}
          </div>

          <div ref={loadMoreRef} />

          {messagesQuery.isFetchingNextPage && (
            <div className="flex justify-center py-4">
              <Spinner className="h-5 w-5 text-[var(--color-accent-2)]" />
            </div>
          )}

          {!messagesQuery.hasNextPage && messages.length > 0 && (
            <div className="py-4 text-center text-xs text-[var(--color-text-subtle)]">No more messages</div>
          )}
        </div>
      </main>

      <InsightsPanel
        insights={insights}
        sync={syncStatusQuery.data}
        classify={classifyStatusQuery.data}
        selectedCategory={category}
        onSelectCategory={(c) => {
          setCategory(c)
          setView(null)
        }}
        selectedSender={sender}
        onSelectSender={(s) => {
          setSender(s)
          setView(null)
        }}
        onSync={() => syncMutation.mutate()}
        onClassify={() => classifyMutation.mutate()}
      />
    </div>
  )
}

function Chip({ label, onClear }: { label: string; onClear: () => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[var(--color-border)] px-2.5 py-0.5 text-xs text-[var(--color-text)]">
      <span className="max-w-[180px] truncate">{label}</span>
      <button
        type="button"
        onClick={onClear}
        aria-label={`Remove filter ${label}`}
        className="text-[var(--color-text-muted)] transition hover:text-[var(--color-text)]"
      >
        ×
      </button>
    </span>
  )
}
