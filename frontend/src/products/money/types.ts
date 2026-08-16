/** account_type values the UI knows how to label and total. */
export type AccountType =
  | 'bank'
  | 'credit_card'
  | 'wallet'
  | 'cash'
  | 'investment'
  | 'loan'
  | 'ppf'
  | 'fd'
  | 'family'
  | 'other'

/** Types whose balance is money owed rather than money held. */
export const LIABILITY_TYPES: ReadonlySet<string> = new Set(['credit_card', 'loan'])

export const ACCOUNT_TYPE_LABELS: Record<string, string> = {
  bank: 'Bank account',
  credit_card: 'Credit card',
  wallet: 'Wallet',
  cash: 'Cash',
  investment: 'Investment',
  loan: 'Loan',
  ppf: 'PPF',
  fd: 'Fixed deposit',
  family: 'Family account',
  other: 'Other',
}

/** How a type='transfer' transaction is classified. */
export type TransferKind = 'self' | 'investment' | 'family'

export const TRANSFER_KIND_LABELS: Record<string, string> = {
  self: 'Between my accounts',
  investment: 'To investment',
  family: 'To family',
}

export interface Account {
  id: number
  name: string
  bank: string
  accountType: string
  accountNumber: string
  currency: string
  openingBalance: number
  currentBalance: number
  /** Cards only; 0 means the alerts never reported one. */
  creditLimit: number
  ifsc: string
  branch: string
  /** manual | statement ('email' exists only on rows predating manual-only accounts) */
  source: string
  /** Whether this account's balance moves the net worth figures. */
  includeInNetworth: boolean
  createdAt: string
  updatedAt: string
}

/**
 * A sender Money knows how to attribute mail to.
 *
 * `issuer` is the value stored in an account's `bank` field — alerts attach by
 * matching against it, so the form must submit this rather than the display
 * name.
 */
export interface Institution {
  issuer: string
  name: string
  defaultType: string
}

export type InvestmentKind =
  | 'fd'
  | 'rd'
  | 'ppf'
  | 'epf'
  | 'nps'
  | 'mutual_fund'
  | 'stock'
  | 'us_stock'
  | 'bond'
  | 'gold'
  | 'other'

export const INVESTMENT_KIND_LABELS: Record<string, string> = {
  fd: 'Fixed deposit',
  rd: 'Recurring deposit',
  ppf: 'PPF',
  epf: 'EPF',
  nps: 'NPS',
  mutual_fund: 'Mutual fund',
  stock: 'Indian stocks',
  us_stock: 'US stocks',
  bond: 'Bonds',
  gold: 'Gold',
  other: 'Other',
}

/** Tabs on the Investments page. `kinds` empty means "everything". */
export const INVESTMENT_TABS: { id: string; label: string; kinds: string[] }[] = [
  { id: 'all', label: 'All holdings', kinds: [] },
  { id: 'us_stock', label: 'US stocks', kinds: ['us_stock'] },
  { id: 'stock', label: 'Indian stocks', kinds: ['stock'] },
  { id: 'mutual_fund', label: 'Mutual funds', kinds: ['mutual_fund'] },
  { id: 'deposits', label: 'Deposits', kinds: ['fd', 'rd', 'ppf', 'epf', 'nps'] },
  { id: 'other', label: 'Other', kinds: ['bond', 'gold', 'other'] },
]

export interface InvestmentTrade {
  id: number
  investmentId: number
  side: 'buy' | 'sell'
  shares: number
  price: number
  amount: number
  currency: string
  tradeDate: string
  orderType: string
  source: string
  createdAt: string
}

export interface Investment {
  id: number
  accountId?: number
  kind: string
  institution: string
  name: string
  identifier: string
  currency: string
  investedAmount: number
  currentValue: number
  maturityAmount?: number
  interestRate?: number
  units?: number
  /** Last known price per unit, and when it was taken. */
  lastPrice?: number
  lastPriceAt: string
  /** Derived: currentValue − investedAmount, and the same as a percentage. */
  gain: number
  gainPercent: number
  /**
   * Whether currentValue reflects a real price. False means it is the cost
   * standing in for one, so a zero gain is an absence of data, not a flat
   * return.
   */
  priced: boolean
  startDate: string
  maturityDate: string
  status: 'active' | 'matured' | 'closed'
  source: string
  notes: string
  createdAt: string
  updatedAt: string
}

export interface InvestmentSummary {
  /** Rupee holdings only — see byCurrency for the rest. */
  totalInvested: number
  totalValue: number
  gain: number
  count: number
  /**
   * Per-currency totals. Kept apart because adding a dollar holding into a
   * rupee total overstates it by the exchange rate.
   */
  byCurrency: {
    currency: string
    count: number
    invested: number
    value: number
    gain: number
  }[]
  byKind: { kind: string; count: number; invested: number; value: number }[]
  maturingSoon: Investment[]
}

export interface ParsedDeposit {
  kind: string
  institution: string
  name: string
  identifier: string
  branch: string
  currency: string
  investedAmount: number
  maturityAmount: number
  interestRate: number
  startDate: string
  maturityDate: string
  dedupeKey: string
  isDuplicate: boolean
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
  /** 'self' | 'investment' | 'family' when type is 'transfer'; absent otherwise. */
  transferKind?: string
  /** Who or what a transfer went to ("Mom", "Zerodha"). */
  counterparty?: string
  createdAt: string

  /** Set when this transaction came from a bank/card alert rather than an import. */
  sourceEmail?: SourceEmail

  /** The model's structured read of this transaction, if one has been run. */
  ai?: TransactionClassification
}

/**
 * One AI reading of a transaction — the Money counterpart of Mail's
 * per-message classification. Advisory: what the model thinks the transaction
 * is, kept apart from what it actually is.
 */
export interface TransactionClassification {
  category: string
  subcategory: string
  merchant: string
  paymentMethod: string
  /** expense | income | transfer */
  nature: string
  /** self | investment | family, when nature is 'transfer' */
  transferKind: string
  counterparty: string
  isRecurring: boolean
  isBill: boolean
  isRefund: boolean
  needsReview: boolean
  confidence: number
  summary: string
  model: string
  classifiedAt: string
  /** Whether the suggestion has been written through to the transaction. */
  applied: boolean
}

export interface ClassifyStatus {
  total: number
  classified: number
  pending: number
  needsReview: number
  running: boolean
  progress?: { done: number; total: number; label?: string }
  error?: string
  detail?: { classified: number; applied: number; failed: number }
}

/** Addresses a message the way the Mail API does, so we can link straight to it. */
export interface SourceEmail {
  mailAccountId: number
  uid: number
  subject: string
  sender: string
  receivedAt: string
}

export interface UpcomingBill {
  id: number
  issuer: string
  cardLast4: string
  totalDue?: number
  minimumDue?: number
  dueDate: string
  status: 'unpaid' | 'paid'
  sourceEmail?: SourceEmail
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
  /** Assets minus liabilities over counted accounts, investments included. */
  netWorth: number
  totalAssets: number
  totalLiabilities: number
  /** Spendable-now subset of assets: bank, wallet and cash balances. */
  liquidAssets: number
  /** Rupee holdings only; foreign ones are listed separately. */
  investmentValue: number
  /**
   * Holdings priced in another currency, each with the rate used to bring it
   * into investmentValue and where that rate came from.
   */
  foreignHoldings: {
    currency: string
    value: number
    invested: number
    count: number
    /** Rupees per unit; 0 when no rate is known, so it was left out. */
    rate: number
    valueInr: number
    rateSource: string
    rateNote: string
    rateAsOf: string
  }[]
  /** How much of investmentValue came from converting foreign holdings. */
  foreignConvertedInr: number
  /** Held but excluded from the total because no rate is known. */
  unconvertedCurrencies: string[]
  totalIncome: number
  totalExpenses: number
  totalSavings: number
  /** Accounts that exist but are switched out of the totals. */
  excludedAccounts: number
  transactionCount: number
  cashFlow: {
    income: number
    expenses: number
    selfTransfers: number
    toInvestments: number
    toFamily: number
  }
  netWorthTrend: { month: string; netWorth: number }[]
  accounts: {
    id: number
    name: string
    accountType: string
    bank: string
    currentBalance: number
    creditLimit: number
    includeInNetworth: boolean
  }[]
  expenseBreakdown: { categoryName: string; color: string; amount: number }[]
  topMerchants: { merchant: string; amount: number; count: number }[]
  upcomingBills: UpcomingBill[]
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
  balances: number
  unrecognized: number
  duplicates: number
  /** Parsed fine but name an account you haven't added; they import once you do. */
  pendingAccount: number
  /** Could not be fetched or recorded this pass; retried on the next one. */
  failed: number
}

export interface ImportPreview {
  /** Which confirm step applies: a transaction export, or a deposit summary. */
  kind: 'statement' | 'deposits'
  fileName: string
  bank: string
  /** The account type the file looks like it belongs to; the user can override. */
  accountType: string
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
  /** Populated instead of `transactions` when `kind` is 'deposits'. */
  deposits?: ParsedDeposit[]
}

/** An exchange rate used to value foreign holdings in rupees. */
export interface FXRate {
  currency: string
  inrPerUnit: number
  asOf: string
  /** 'manual' when you set it, 'derived' when read from your own forex data. */
  source: string
  note: string
}
