export interface Account {
  id: number
  name: string
  bank: string
  accountType: string
  accountNumber: string
  currency: string
  openingBalance: number
  currentBalance: number
  ifsc: string
  branch: string
  createdAt: string
  updatedAt: string
}

export interface Category {
  id: number
  name: string
  type: 'income' | 'expense'
  icon: string
  color: string
  sortOrder: number
}

export interface Transaction {
  id: number
  accountId: number
  accountName?: string
  txnDate: string
  valueDate: string
  narration: string
  refNo: string
  withdrawalAmt: number
  depositAmt: number
  closingBalance?: number
  type: 'income' | 'expense' | 'transfer'
  categoryId?: number
  categoryName?: string
  categoryColor?: string
  merchant: string
  paymentMethod: string
  notes: string
  linkedTxnId?: number
  createdAt: string
}

export interface TransferSuggestion {
  id: number
  txnA: Transaction
  txnB: Transaction
  confidence: number
  status: 'pending' | 'confirmed' | 'dismissed'
  createdAt: string
}

export interface DashboardSummary {
  netWorth: number
  totalIncome: number
  totalExpenses: number
  totalSavings: number
  transactionCount: number
  accounts: {
    id: number
    name: string
    accountType: string
    bank: string
    currentBalance: number
  }[]
  expenseBreakdown: { categoryName: string; color: string; amount: number }[]
  topMerchants: { merchant: string; amount: number; count: number }[]
}

export interface ParsedTransaction {
  txnDate: string
  valueDate: string
  narration: string
  refNo: string
  withdrawalAmt: number
  depositAmt: number
  closingBalance: number
  merchant: string
  paymentMethod: string
  type: 'income' | 'expense'
  suggestedCategory: string
  isDuplicate: boolean
  dedupeHash: string
}

export interface EmailAccount {
  id: number
  email: string
  imapHost: string
  imapPort: number
  lastSyncedAt: string
  lastUid: number
  enabled: boolean
  createdAt: string
}

export interface Bill {
  id: number
  accountId?: number
  issuer: string
  cardLast4: string
  statementPeriod: string
  totalDue?: number
  minimumDue?: number
  dueDate: string
  status: 'unpaid' | 'paid'
  createdAt: string
}

export interface PDFPassword {
  id: number
  issuer: string
  hasValue: boolean
}

export interface SyncResult {
  scanned: number
  transactions: number
  bills: number
  unrecognized: number
  duplicates: number
}

export interface ImportPreview {
  fileName: string
  bank: string
  accountNumber: string
  accountBranch: string
  ifsc: string
  statementFrom: string
  statementTo: string
  openingBalance: number
  closingBalance: number
  totalRows: number
  newRows: number
  duplicateRows: number
  suggestedAccount?: { id: number; name: string }
  transactions: ParsedTransaction[]
}
