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
}

export interface Message extends MessageSummary {
  bodyText: string
  bodyHtml: string
}
