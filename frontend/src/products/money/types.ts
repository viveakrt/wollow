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
  /** Set when the account was retired: hidden by default, history kept, no longer matched by mail. */
  archivedAt: string
  createdAt: string
  updatedAt: string
}

/** Account types a parser rule can attach its mail to. */
export const RULE_ACCOUNT_TYPES: { value: string; label: string }[] = [
  { value: 'bank', label: 'Bank account' },
  { value: 'credit_card', label: 'Credit card' },
  { value: 'wallet', label: 'Wallet' },
  { value: 'loan', label: 'Loan' },
  { value: 'investment', label: 'Investment' },
]

// ---- User-defined email parsers -------------------------------------------

export type ParserRuleKind = 'transaction' | 'bill' | 'balance' | 'trade'

export const PARSER_KIND_LABELS: Record<ParserRuleKind, string> = {
  transaction: 'Transaction alert',
  bill: 'Card statement (bill)',
  balance: 'Balance update',
  trade: 'Investment order',
}

export const PARSER_KIND_HELP: Record<ParserRuleKind, string> = {
  transaction: 'A debit or credit on a bank account, wallet or card. Becomes a transaction.',
  bill: 'A credit card statement: amounts due and the due date. Becomes a bill reminder.',
  balance: 'A balance-only alert. Recorded as the account balance on that date, never as a transaction.',
  trade: 'A broker order confirmation. Becomes (or updates) a holding, never a ledger transaction.',
}

export type ParserFieldKind = 'amount' | 'number' | 'last4' | 'date' | 'text' | 'word'

/** One value a kind of rule can extract, from GET /parser-rules/fields. */
export interface ParserFieldSpec {
  name: string
  label: string
  kind: ParserFieldKind
  required: boolean
}

/** A derived field: where the value sat in the sample and the text that anchors it. */
export interface ParserRuleField {
  name: string
  kind: ParserFieldKind
  prefix: string
  suffix: string
  atLineEnd: boolean
  occurrence: number
  start: number
  end: number
  sample: string
}

/** A value the user marked in the sample: rune (code point) offsets plus the selected text. */
export interface LabeledSpan {
  name: string
  start: number
  end: number
  sample: string
}

export interface ParserAttributes {
  direction?: 'expense' | 'income'
  paymentMethod?: string
  currency?: string
  side?: 'buy' | 'sell'
  instrumentKind?: string
  broker?: string
}

export interface ParserSample {
  mailAccountId: number
  uid: number
  folder: string
  rfcMessageId: string
  subject: string
  from: string
  date: string
  /** The normalized text the rule is defined on. Absent in list responses. */
  text?: string
  fromDomain?: string
  hasPdf?: boolean
  /** The registry's guess for the sender, when it knows it. */
  issuer?: string
  defaultKind?: string
}

export interface ParserRule {
  id: number
  name: string
  kind: ParserRuleKind
  enabled: boolean
  priority: number
  issuer: string
  senderDomain: string
  senderEmail: string
  subjectContains: string
  bodyContains: string
  /** 0 when the account is found from the extracted digits. */
  accountId: number
  accountType: string
  attributes: ParserAttributes
  fields: ParserRuleField[]
  sample: ParserSample
  matchCount: number
  lastMatchedAt: string
  createdAt: string
  updatedAt: string
  /** List responses only: whether the stored sample text exists. */
  hasSample?: boolean
}

/** What POST/PUT /parser-rules and POST /parser-rules/test accept. */
export interface ParserRuleInput {
  name: string
  kind: ParserRuleKind
  enabled: boolean
  priority: number
  issuer: string
  senderDomain: string
  senderEmail: string
  subjectContains: string
  bodyContains: string
  accountId: number
  accountType: string
  attributes: ParserAttributes
  sample: ParserSample
  /** Marked values; the server derives fields from them. */
  spans?: LabeledSpan[]
  /** Already-derived fields, when no spans are sent. */
  fields?: ParserRuleField[]
}

export interface ParserTestResult {
  uid: number
  subject: string
  date: string
  matched: boolean
  values?: Record<string, string>
  reason?: string
}

export interface ParserTestResponse {
  fields: ParserRuleField[]
  results: ParserTestResult[]
}

/** Mail held because it names an account nobody has registered. */
export interface PendingAccountGroup {
  issuer: string
  name: string
  last4: string
  kind: string
  count: number
  lastSeen: string
  latestSubject: string
  sample: { mailAccountId: number; uid: number }
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
  /** INR per unit of the trade's currency on its trade date, once looked up. */
  fxRate?: number
  createdAt: string
}

/** What POST/PUT .../trades accepts. Amount is derived server-side from
 * shares × price when omitted, and vice versa. */
export interface TradeInput {
  side: 'buy' | 'sell'
  shares: number
  price?: number
  amount?: number
  tradeDate: string
  orderType?: string
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
  /** Last known price per unit, and when it was taken (a date, or a timestamp for market prices). */
  lastPrice?: number
  lastPriceAt: string
  /** manual | market | statement */
  priceSource: string
  /** What prices are fetched under ("AAPL", "RELIANCE.NS", "AMFI:<ISIN>"); "none" switches fetching off. */
  quoteSymbol: string
  /** Why the last price fetch failed, if it did. */
  quoteError?: string
  /** Profit locked in by sells, in the holding's own currency. */
  realizedGain: number
  /** Rupee figures: cost and realised profit at each trade's own exchange rate, value at today's. */
  realizedGainInr?: number
  investedInr?: number
  valueInr?: number
  gainInr?: number
  /** Derived: currentValue − investedAmount, and the same as a percentage. */
  gain: number
  gainPercent: number
  /**
   * Whether currentValue reflects a real price. False means it is the cost
   * standing in for one, so a zero gain is an absence of data, not a flat
   * return.
   */
  priced: boolean
  /**
   * Whether units/investedAmount/currentValue are DERIVED from trades. When
   * true, editing those three fields on the holding itself has no effect —
   * the server ignores them and re-derives from investment_trades on every
   * save. Edit the trades instead (add/update/delete under .../trades).
   */
  hasTrades: boolean
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
  /** Every holding in rupees; foreign value at today's rate, cost and realised profit at trade-date rates. */
  totalValueInr: number
  totalInvestedInr: number
  unrealizedGainInr: number
  /** Includes closed holdings — that is where most realised profit sits. */
  realizedGainInr: number
  /** Currencies held but left out of the rupee totals for want of a rate. */
  unconvertedCurrencies: string[]
  /** When the most recent market price was taken; empty if none has been fetched. */
  pricesUpdatedAt: string
}

export interface MarketSearchHit {
  symbol: string
  name: string
  exchange: string
  quoteType: string
}

export interface PriceRefreshResult {
  priced: number
  failed: number
  ratesUpdated: string[]
  tradeRates: number
  errors: string[]
  refreshedAt: string
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

export interface ParsedZerodhaHolding {
  symbol: string
  isin: string
  kind: string // stock | mutual_fund
  units: number
  /** Statement's "Previous Closing Price". */
  price: number
  /** Statement's "Open Value": current market value of units. */
  value: number
  realizedPl: number
  unrealizedPl: number
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
  /** Sum of every account_type='family' account's own balance, independent of netWorth. */
  familyNetWorth: number
  familyAssets: number
  familyLiabilities: number
  familyAccountCount: number
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
  accountName?: string
  issuer: string
  cardLast4: string
  statementPeriod: string
  totalDue?: number
  minimumDue?: number
  dueDate: string
  status: 'unpaid' | 'paid'
  /** YYYY-MM-DD the bill was marked paid; empty while unpaid. */
  paidAt: string
  createdAt: string
}

/** How a payment compares with what a statement asked for. */
export type BillCoverage = 'full' | 'minimum' | 'partial' | 'unknown'

/** Offered after linking a transfer into a card: the payment and that card's unpaid bills. */
export interface BillPaymentSuggestion {
  accountId: number
  accountName: string
  amount: number
  date: string
  bills: (Bill & { coverage: BillCoverage })[]
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
  trades: number
  /** Read by a rule, but naming an account nobody has registered yet. Held until it exists. */
  pendingAccount: number
  unrecognized: number
  duplicates: number
  /** Could not be fetched or recorded this pass; retried on the next one. */
  failed: number
}

/**
 * A rescan clears message links whose transaction, bill, or trade was
 * deleted (typically the account or holding it belonged to was deleted,
 * cascading the transaction away with it), and links left 'unrecognized'
 * before their account existed or before the parser learned their template —
 * then re-ingests them, since an ordinary sync never looks at an
 * already-linked message again.
 */
export interface RescanResult extends SyncResult {
  /** Stale links cleared before re-ingesting — recovered history, not new mail. */
  cleared: number
}

export interface ImportPreview {
  /** Which confirm step applies: a transaction export, a deposit summary, or a Zerodha P&L export. */
  kind: 'statement' | 'deposits' | 'zerodha'
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
  /** Populated instead of `transactions` when `kind` is 'zerodha'. */
  clientId?: string
  zerodhaHoldings?: ParsedZerodhaHolding[]
}

/** An exchange rate used to value foreign holdings in rupees. */
export interface FXRate {
  currency: string
  inrPerUnit: number
  asOf: string
  /** 'manual' when you set it (always wins), 'market' for the live rate, 'derived' from your own forex data. */
  source: string
  note: string
}
