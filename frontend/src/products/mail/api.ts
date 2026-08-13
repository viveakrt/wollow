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

export function getMessage(accountId: number, messageId: string, folder: string): Promise<Message> {
  return api.get(`/api/mail/accounts/${accountId}/messages/${messageId}`, { folder })
}

export function deleteMessage(accountId: number, messageId: string, folder: string): Promise<{ ok: true }> {
  return api.delete(`/api/mail/accounts/${accountId}/messages/${messageId}`, { folder })
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
