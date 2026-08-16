import { api as http } from '../../platform/api/client'
import type {
  Account,
  Category,
  ClassifyStatus,
  Transaction,
  DashboardSummary,
  ImportPreview,
  Institution,
  Investment,
  InvestmentSummary,
  InvestmentTrade,
  TradeInput,
  ParsedDeposit,
  EmailAccount,
  Bill,
  FXRate,
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
  // The senders Money can attribute mail to. The add-account form offers these
  // so a hand-entered account stores the issuer code alerts match against.
  institutions: {
    list: () => http.get<Institution[]>(`${BASE}/institutions`),
  },
  investments: {
    list: (params?: Record<string, string | undefined>) =>
      http.get<Investment[]>(`${BASE}/investments`, params),
    summary: () => http.get<InvestmentSummary>(`${BASE}/investments/summary`),
    create: (data: Partial<Investment>) => http.post<Investment>(`${BASE}/investments`, data),
    update: (id: number, data: Partial<Investment>) =>
      http.put<Investment>(`${BASE}/investments/${id}`, data),
    delete: (id: number) => http.delete<void>(`${BASE}/investments/${id}`),
    // The orders that built a position — the evidence behind its average cost.
    trades: (id: number) => http.get<InvestmentTrade[]>(`${BASE}/investments/${id}/trades`),
    // A trade-backed holding's units/invested/current value are DERIVED —
    // these are the only way to actually change them; editing the holding
    // itself only ever touches its descriptive fields for such a holding.
    addTrade: (id: number, data: TradeInput) =>
      http.post<Investment>(`${BASE}/investments/${id}/trades`, data),
    updateTrade: (id: number, tradeId: number, data: TradeInput) =>
      http.put<Investment>(`${BASE}/investments/${id}/trades/${tradeId}`, data),
    deleteTrade: (id: number, tradeId: number) =>
      http.delete<Investment>(`${BASE}/investments/${id}/trades/${tradeId}`),
    // Money has no market feed, so a holding is valued by entering its price.
    setPrice: (id: number, price: number, asOf?: string) =>
      http.post<Investment>(`${BASE}/investments/${id}/price`, { price, asOf }),
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
    // The category also applies to every other transaction sharing these
    // narrations; `matched` is how many extra rows that covered.
    bulkCategorize: (ids: number[], categoryId: number | null) =>
      http.post<{ updated: number; matched: number }>(`${BASE}/transactions/bulk-categorize`, {
        ids,
        categoryId,
      }),
    // Reclassify rows as transfers of a kind: 'self' between own accounts,
    // 'investment' into a holding, 'family' to a family member.
    bulkMarkTransfer: (ids: number[], kind: string, counterparty = '') =>
      http.post<{ updated: number }>(`${BASE}/transactions/bulk-mark-transfer`, {
        ids,
        kind,
        counterparty,
      }),
    // AI classification runs detached — one model call per transaction — so
    // starting it returns immediately and the client polls the status.
    // Passing ids re-classifies exactly those; omitting them covers every
    // transaction never classified before.
    classify: (ids?: number[]) =>
      http.post<{ started: boolean }>(`${BASE}/transactions/classify`, { ids: ids ?? [] }),
    classifyStatus: () => http.get<ClassifyStatus>(`${BASE}/transactions/classify/status`),
    // Write a stored suggestion through to the transaction, including the
    // type/transfer change the background pass deliberately leaves alone.
    applyClassification: (id: number) =>
      http.post<{ applied: boolean }>(`${BASE}/transactions/${id}/apply-classification`),
    dismissClassification: (id: number) =>
      http.post<{ dismissed: boolean }>(`${BASE}/transactions/${id}/dismiss-classification`),
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
    // One upload for any supported file; the response's `kind` says which
    // commit step applies.
    preview: (file: File) => upload<ImportPreview>('/import/hdfc/preview', file),
    hdfcCommit: (payload: unknown) =>
      http.post<{ batchId: number; accountId: number; importedRows: number; duplicateRows: number }>(
        `${BASE}/import/hdfc/commit`,
        payload,
      ),
    depositsCommit: (fileName: string, deposits: ParsedDeposit[]) =>
      http.post<{ imported: number; updated: number }>(`${BASE}/import/deposits/commit`, {
        fileName,
        deposits,
      }),
  },
  // Mailboxes are connected and removed on the Mail side — one credential
  // store, and a "disconnect" here would discard the whole message index.
  emailAccounts: {
    list: () => http.get<EmailAccount[]>(`${BASE}/email-accounts`),
    sync: (id: number) => http.post<SyncResult>(`${BASE}/email-accounts/${id}/sync`),
  },
  // Exchange rates that bring foreign holdings into the rupee net worth.
  // Either the user set one, or it was derived from their own forex
  // remittances; nothing here is fetched from a market feed.
  fxRates: {
    list: () => http.get<FXRate[]>(`${BASE}/fx-rates`),
    set: (currency: string, inrPerUnit: number, asOf?: string) =>
      http.post<{ saved: boolean }>(`${BASE}/fx-rates`, { currency, inrPerUnit, asOf }),
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
