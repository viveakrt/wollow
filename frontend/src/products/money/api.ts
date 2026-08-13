import { api as http } from '../../platform/api/client'
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

// Money's routes moved under /api/money in the merge, and the trailing slashes
// chi used to require are gone — stdlib ServeMux treats "/accounts" and
// "/accounts/" as different patterns.
const BASE = '/api/money'

// Statement upload is the one multipart request; everything else goes through
// the shared JSON client, which carries the session cookie and reports 401s to
// the auth bridge.
async function upload<T>(path: string, file: File): Promise<T> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    credentials: 'include',
    body: form,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || `Request failed: ${res.status}`)
  }
  return res.json()
}

export const api = {
  accounts: {
    list: () => http.get<Account[]>(`${BASE}/accounts`),
    get: (id: number) => http.get<Account>(`${BASE}/accounts/${id}`),
    create: (data: Partial<Account>) => http.post<Account>(`${BASE}/accounts`, data),
    update: (id: number, data: Partial<Account>) => http.put<Account>(`${BASE}/accounts/${id}`, data),
    delete: (id: number) => http.delete<void>(`${BASE}/accounts/${id}`),
    bulkDelete: (ids: number[]) => http.post<{ deleted: number }>(`${BASE}/accounts/bulk-delete`, { ids }),
  },
  categories: {
    list: () => http.get<Category[]>(`${BASE}/categories`),
    create: (data: Partial<Category>) => http.post<Category>(`${BASE}/categories`, data),
  },
  transactions: {
    list: (params?: Record<string, string | number | undefined>) =>
      http.get<Transaction[]>(`${BASE}/transactions`, params),
    get: (id: number) => http.get<Transaction>(`${BASE}/transactions/${id}`),
    create: (data: Partial<Transaction>) => http.post<Transaction>(`${BASE}/transactions`, data),
    update: (id: number, data: Partial<Transaction>) =>
      http.put<Transaction>(`${BASE}/transactions/${id}`, data),
    delete: (id: number) => http.delete<void>(`${BASE}/transactions/${id}`),
    bulkDelete: (ids: number[]) =>
      http.post<{ deleted: number }>(`${BASE}/transactions/bulk-delete`, { ids }),
    bulkCategorize: (ids: number[], categoryId: number | null) =>
      http.post<{ updated: number }>(`${BASE}/transactions/bulk-categorize`, { ids, categoryId }),
    linkTransfer: (txnIdA: number, txnIdB: number) =>
      http.post<{ linked: boolean }>(`${BASE}/transactions/link-transfer`, { txnIdA, txnIdB }),
    unlinkTransfer: (id: number) =>
      http.post<{ unlinked: boolean }>(`${BASE}/transactions/${id}/unlink-transfer`),
  },
  transferSuggestions: {
    list: () => http.get<TransferSuggestion[]>(`${BASE}/transfer-suggestions`),
    scan: () => http.post<{ suggestionsCreated: number }>(`${BASE}/transfer-suggestions/scan`),
    confirm: (id: number) =>
      http.post<{ confirmed: boolean }>(`${BASE}/transfer-suggestions/${id}/confirm`),
    dismiss: (id: number) =>
      http.post<{ dismissed: boolean }>(`${BASE}/transfer-suggestions/${id}/dismiss`),
  },
  dashboard: {
    summary: (from?: string, to?: string) =>
      http.get<DashboardSummary>(`${BASE}/dashboard/summary`, { from, to }),
  },
  import: {
    hdfcPreview: (file: File) => upload<ImportPreview>('/import/hdfc/preview', file),
    hdfcCommit: (payload: unknown) =>
      http.post<{ batchId: number; accountId: number; importedRows: number; duplicateRows: number }>(
        `${BASE}/import/hdfc/commit`,
        payload,
      ),
  },
  // Mailboxes are connected and removed on the Mail side — one credential
  // store, and a "disconnect" here would discard the whole message index.
  emailAccounts: {
    list: () => http.get<EmailAccount[]>(`${BASE}/email-accounts`),
    sync: (id: number) => http.post<SyncResult>(`${BASE}/email-accounts/${id}/sync`),
  },
  bills: {
    list: () => http.get<Bill[]>(`${BASE}/bills`),
  },
  pdfPasswords: {
    list: () => http.get<PDFPassword[]>(`${BASE}/pdf-passwords`),
    set: (issuer: string, password: string) =>
      http.post<{ saved: boolean }>(`${BASE}/pdf-passwords`, { issuer, password }),
    parsePending: () =>
      http.post<{ parsed: number; failed: number }>(`${BASE}/pdf-attachments/parse-pending`),
  },
}
