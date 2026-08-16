export interface Account {
  id: number
  label: string
  providerType: string
  imapHost: string
  imapPort: number
  smtpHost: string
  smtpPort: number
  username: string
  useTls: boolean
  createdAt: string
}

export interface NewAccountInput {
  label: string
  imapHost: string
  imapPort: number
  smtpHost: string
  smtpPort: number
  username: string
  password: string
  useTls: boolean
}

export type Priority = 'critical' | 'high' | 'medium' | 'low' | 'noise'

export const CATEGORY_LABELS: Record<string, string> = {
  work: 'Work',
  jobs_career: 'Jobs & Career',
  finance: 'Finance',
  bills_payments: 'Bills & Payments',
  shopping: 'Shopping',
  orders_delivery: 'Orders & Delivery',
  travel_booking: 'Travel',
  home_services: 'Home & Services',
  personal: 'Personal',
  education: 'Education',
  health: 'Health',
  legal_government: 'Legal & Government',
  marketing_promotions: 'Promotions',
  newsletters: 'Newsletters',
  notifications: 'Notifications',
  security: 'Security',
  documents: 'Documents',
  meetings_calendar: 'Meetings',
  communities_social: 'Social',
  automated_system: 'Automated',
  spam: 'Spam',
  other: 'Other',
}

export const ACTION_LABELS: Record<string, string> = {
  no_action: 'No action',
  read_only: 'Read only',
  review: 'Review',
  respond: 'Respond',
  pay: 'Pay',
  book: 'Book',
  download: 'Download',
  verify: 'Verify',
  follow_up: 'Follow up',
  archive: 'Archive',
  unsubscribe: 'Unsubscribe',
}

/**
 * What the Money product made of a message, when finance ingest has seen it.
 * This is the Mail-side half of the cross-product link; the Money side is
 * `sourceEmail` on a transaction.
 */
export interface MoneyLink {
  parsedAs: 'transaction' | 'bill' | 'unrecognized'
  transactionId?: number
  billId?: number
  amount?: number
  dueDate?: string
  issuer?: string
}

export interface MessageSummary {
  id: string
  subject: string
  from: string
  date: string
  seen: boolean
  flagged: boolean
  snippet: string
  size: number

  // Present only once the message has been classified by the background pass.
  classified: boolean
  category?: string
  subcategory?: string
  senderGroup?: string
  priority?: Priority
  action?: string
  requiresResponse: boolean
  confidence?: number
  aiSummary?: string

  moneyLink?: MoneyLink
}

/**
 * One non-body MIME part. Content is never inlined in the message JSON — it is
 * fetched separately by `partId`, so opening mail with a 20 MB PDF on it stays
 * cheap.
 */
export interface Attachment {
  partId: string
  /** Content-ID without angle brackets — what an HTML body's `cid:` refers to. */
  contentId?: string
  fileName: string
  contentType: string
  size: number
  /** Displayed within the body (cid: image) rather than offered as a download. */
  inline: boolean
}

export interface Message extends MessageSummary {
  to: string
  cc: string
  replyTo: string
  bodyText: string
  bodyHtml: string
  attachments: Attachment[]
}
