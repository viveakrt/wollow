import { api } from '../../platform/api/client'
import type { Account, Message, MessageSummary, NewAccountInput } from './types'

export function getAccounts(): Promise<Account[]> {
  return api.get('/api/mail/accounts')
}

export function createAccount(input: NewAccountInput): Promise<{ id: number }> {
  return api.post('/api/mail/accounts', input)
}

export function deleteAccount(id: number): Promise<{ ok: true }> {
  return api.delete(`/api/mail/accounts/${id}`)
}

export interface MessageFilters {
  view?: string | null
  category?: string | null
  priority?: string | null
  sender?: string | null
  q?: string | null
}

export function getMessages(
  accountId: number,
  folder: string,
  limit: number,
  offset: number,
  filters: MessageFilters = {},
): Promise<MessageSummary[]> {
  const query: Record<string, string | number> = { folder, limit, offset }
  for (const [key, value] of Object.entries(filters)) {
    if (value) query[key] = value
  }
  return api.get(`/api/mail/accounts/${accountId}/messages`, query)
}

export interface Insights {
  totals: { messages: number; unread: number; flagged: number; classified: number }
  categories: { key: string; count: number }[]
  priorities: { key: string; count: number }[]
  senders: { email: string; name: string; domain: string; count: number; lastSeen: string }[]
  smartViews: Record<string, number>
}

export function getInsights(accountId: number, folder = 'INBOX'): Promise<Insights> {
  return api.get(`/api/mail/accounts/${accountId}/insights`, { folder })
}

export interface Sender {
  email: string
  name: string
  domain: string
  count: number
  lastSeen: string
  unsubscribedAt?: string
  unsubscribeMethod?: 'http' | 'manual'
}

export function getSenders(accountId: number, folder = 'INBOX'): Promise<Sender[]> {
  return api.get(`/api/mail/accounts/${accountId}/senders`, { folder })
}

export interface UnsubscribeResult {
  status: 'unsubscribed' | 'manual'
  method: 'http' | 'mailto' | 'manual'
  mailto?: string
}

export function unsubscribeSender(accountId: number, email: string): Promise<UnsubscribeResult> {
  return api.post(`/api/mail/accounts/${accountId}/senders/unsubscribe`, { email })
}

export function markSenderUnsubscribed(accountId: number, email: string): Promise<UnsubscribeResult> {
  return api.post(`/api/mail/accounts/${accountId}/senders/mark-unsubscribed`, { email })
}

export function resubscribeSender(accountId: number, email: string): Promise<{ ok: true }> {
  return api.post(`/api/mail/accounts/${accountId}/senders/resubscribe`, { email })
}

/**
 * Sender-level bulk actions are detached jobs, not synchronous calls.
 *
 * They address every message from a sender — thousands in a real mailbox — so
 * holding an HTTP request open for one meant the socket died before the work
 * finished. These start the job; `getBulkSenderStatus` reports its progress.
 */
export interface BulkJobStart {
  started: boolean
  /** How many messages the job will process. */
  total?: number
  status: BulkJobStatus
}

export interface BulkJobStatus {
  running: boolean
  startedAt?: string
  finishedAt?: string
  error?: string
  progress?: { done: number; total: number; label?: string }
  detail?: {
    action: string
    total: number
    updated: number
    failed?: string[]
  }
}

export function bulkFlagSenders(
  accountId: number,
  emails: string[],
  flag: '\\Seen' | '\\Flagged',
  value: boolean,
): Promise<BulkJobStart> {
  return api.post(`/api/mail/accounts/${accountId}/senders/bulk-flag`, { emails, flag, value })
}

export function bulkDeleteSenders(accountId: number, emails: string[]): Promise<BulkJobStart> {
  return api.post(`/api/mail/accounts/${accountId}/senders/bulk-delete`, { emails })
}

export function bulkArchiveSenders(accountId: number, emails: string[]): Promise<BulkJobStart> {
  return api.post(`/api/mail/accounts/${accountId}/senders/bulk-archive`, { emails })
}

export function getBulkSenderStatus(accountId: number): Promise<BulkJobStatus> {
  return api.get(`/api/mail/accounts/${accountId}/senders/bulk-status`)
}

export function getMessage(accountId: number, messageId: string, folder: string): Promise<Message> {
  return api.get(`/api/mail/accounts/${accountId}/messages/${messageId}`, { folder })
}

export function deleteMessage(accountId: number, messageId: string, folder: string): Promise<{ ok: true }> {
  return api.delete(`/api/mail/accounts/${accountId}/messages/${messageId}`, { folder })
}

/**
 * URL for one MIME part. Same-origin, so the session cookie rides along on the
 * `<img>` and `<a download>` requests the browser makes for it without any
 * extra plumbing.
 *
 * `partId` accepts either a part number from `attachments` or a bare
 * Content-ID, which is what lets a `cid:` reference be rewritten to a URL
 * without first looking it up.
 */
export function messagePartUrl(
  accountId: number,
  messageId: string,
  folder: string,
  partId: string,
  download = false,
): string {
  const params = new URLSearchParams({ folder })
  if (download) params.set('download', '1')
  return `/api/mail/accounts/${accountId}/messages/${encodeURIComponent(messageId)}/parts/${encodeURIComponent(partId)}?${params}`
}

export function setMessageFlag(
  accountId: number,
  messageId: string,
  folder: string,
  flag: '\\Seen' | '\\Flagged',
  value: boolean,
): Promise<{ ok: true }> {
  return api.post(`/api/mail/accounts/${accountId}/messages/${messageId}/flag`, { flag, value }, { folder })
}

export interface BulkMessagesResult {
  updated: number
  failed?: string[]
}

export function bulkDeleteMessages(
  accountId: number,
  folder: string,
  ids: string[],
): Promise<BulkMessagesResult> {
  return api.post(`/api/mail/accounts/${accountId}/messages/bulk-delete`, { folder, ids })
}

export function bulkSetMessageFlag(
  accountId: number,
  folder: string,
  ids: string[],
  flag: '\\Seen' | '\\Flagged',
  value: boolean,
): Promise<BulkMessagesResult> {
  return api.post(`/api/mail/accounts/${accountId}/messages/bulk-flag`, { folder, ids, flag, value })
}

export function summarizeMessage(
  accountId: number,
  messageId: string,
  folder: string,
): Promise<{ summary: string }> {
  return api.post(`/api/mail/accounts/${accountId}/messages/${messageId}/summarize`, undefined, { folder })
}

export interface SyncStatus {
  running: boolean
  startedAt?: string
  finishedAt?: string
  error?: string
  stored: number
  detail?: { added: number; updated: number; deleted: number; total: number } | null
}

export interface ClassifyStatus {
  total: number
  classified: number
  pending: number
  running: boolean
  error?: string
}

export function startSync(accountId: number, folder = 'INBOX'): Promise<{ started: boolean }> {
  return api.post(`/api/mail/accounts/${accountId}/sync`, undefined, { folder })
}

export function getSyncStatus(accountId: number, folder = 'INBOX'): Promise<SyncStatus> {
  return api.get(`/api/mail/accounts/${accountId}/sync/status`, { folder })
}

export function startClassify(accountId: number): Promise<{ started: boolean }> {
  return api.post(`/api/mail/accounts/${accountId}/classify`)
}

export function getClassifyStatus(accountId: number): Promise<ClassifyStatus> {
  return api.get(`/api/mail/accounts/${accountId}/classify/status`)
}
