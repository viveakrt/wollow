import type {
  Account,
  Category,
  Transaction,
  DashboardSummary,
  ImportPreview,
  EmailAccount,
  Bill,
  SyncResult,
  TransferSuggestion,
  PDFPassword,
} from './types'

const BASE = '/api'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: options?.body instanceof FormData ? undefined : { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || `Request failed: ${res.status}`)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

export const api = {
  accounts: {
    list: () => request<Account[]>('/accounts/'),
    get: (id: number) => request<Account>(`/accounts/${id}`),
    create: (data: Partial<Account>) =>
      request<Account>('/accounts/', { method: 'POST', body: JSON.stringify(data) }),
    update: (id: number, data: Partial<Account>) =>
      request<Account>(`/accounts/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    delete: (id: number) => request<void>(`/accounts/${id}`, { method: 'DELETE' }),
    bulkDelete: (ids: number[]) =>
      request<{ deleted: number }>('/accounts/bulk-delete', {
        method: 'POST',
        body: JSON.stringify({ ids }),
      }),
  },
  categories: {
    list: () => request<Category[]>('/categories/'),
    create: (data: Partial<Category>) =>
      request<Category>('/categories/', { method: 'POST', body: JSON.stringify(data) }),
  },
  transactions: {
    list: (params?: Record<string, string | number | undefined>) => {
      const qs = new URLSearchParams()
      Object.entries(params || {}).forEach(([k, v]) => {
        if (v !== undefined && v !== '') qs.set(k, String(v))
      })
      const suffix = qs.toString() ? `?${qs.toString()}` : ''
      return request<Transaction[]>(`/transactions/${suffix}`)
    },
    get: (id: number) => request<Transaction>(`/transactions/${id}`),
    create: (data: Partial<Transaction>) =>
      request<Transaction>('/transactions/', { method: 'POST', body: JSON.stringify(data) }),
    update: (id: number, data: Partial<Transaction>) =>
      request<Transaction>(`/transactions/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    delete: (id: number) => request<void>(`/transactions/${id}`, { method: 'DELETE' }),
    bulkDelete: (ids: number[]) =>
      request<{ deleted: number }>('/transactions/bulk-delete', {
        method: 'POST',
        body: JSON.stringify({ ids }),
      }),
    bulkCategorize: (ids: number[], categoryId: number | null) =>
      request<{ updated: number }>('/transactions/bulk-categorize', {
        method: 'POST',
        body: JSON.stringify({ ids, categoryId }),
      }),
    linkTransfer: (txnIdA: number, txnIdB: number) =>
      request<{ linked: boolean }>('/transactions/link-transfer', {
        method: 'POST',
        body: JSON.stringify({ txnIdA, txnIdB }),
      }),
    unlinkTransfer: (id: number) =>
      request<{ unlinked: boolean }>(`/transactions/${id}/unlink-transfer`, { method: 'POST' }),
  },
  transferSuggestions: {
    list: () => request<TransferSuggestion[]>('/transfer-suggestions/'),
    scan: () => request<{ suggestionsCreated: number }>('/transfer-suggestions/scan', { method: 'POST' }),
    confirm: (id: number) =>
      request<{ confirmed: boolean }>(`/transfer-suggestions/${id}/confirm`, { method: 'POST' }),
    dismiss: (id: number) =>
      request<{ dismissed: boolean }>(`/transfer-suggestions/${id}/dismiss`, { method: 'POST' }),
  },
  dashboard: {
    summary: (from?: string, to?: string) => {
      const qs = new URLSearchParams()
      if (from) qs.set('from', from)
      if (to) qs.set('to', to)
      const suffix = qs.toString() ? `?${qs.toString()}` : ''
      return request<DashboardSummary>(`/dashboard/summary${suffix}`)
    },
  },
  import: {
    hdfcPreview: (file: File) => {
      const form = new FormData()
      form.append('file', file)
      return request<ImportPreview>('/import/hdfc/preview', { method: 'POST', body: form })
    },
    hdfcCommit: (payload: unknown) =>
      request<{ batchId: number; accountId: number; importedRows: number; duplicateRows: number }>(
        '/import/hdfc/commit',
        { method: 'POST', body: JSON.stringify(payload) },
      ),
  },
  emailAccounts: {
    list: () => request<EmailAccount[]>('/email-accounts/'),
    connect: (email: string, appPassword: string) =>
      request<{ id: number; email: string }>('/email-accounts/', {
        method: 'POST',
        body: JSON.stringify({ email, appPassword }),
      }),
    delete: (id: number) => request<void>(`/email-accounts/${id}`, { method: 'DELETE' }),
    sync: (id: number) => request<SyncResult>(`/email-accounts/${id}/sync`, { method: 'POST' }),
  },
  bills: {
    list: () => request<Bill[]>('/bills'),
  },
  pdfPasswords: {
    list: () => request<PDFPassword[]>('/pdf-passwords/'),
    set: (issuer: string, password: string) =>
      request<{ saved: boolean }>('/pdf-passwords/', {
        method: 'POST',
        body: JSON.stringify({ issuer, password }),
      }),
    parsePending: () =>
      request<{ parsed: number; failed: number }>('/pdf-attachments/parse-pending', { method: 'POST' }),
  },
}
