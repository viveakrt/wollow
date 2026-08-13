// Category visual identity.
//
// Colours come from a validated categorical palette (checked for CVD
// separation, chroma, lightness band and normal-vision separation against a
// white surface). Assignment is by fixed slot, never cycled at render time,
// so a category is always the same colour everywhere in the app.

export interface CategoryStyle {
  label: string
  /** Solid series colour — used for chart marks and legend swatches. */
  color: string
  /** Tinted background for inline pills. */
  bg: string
  /** Readable ink on that tint. */
  fg: string
  icon: string
}

const SERIES = {
  blue: '#2a78d6',
  orange: '#eb6834',
  aqua: '#1baf7a',
  yellow: '#eda100',
  magenta: '#e87ba4',
  green: '#008300',
  violet: '#4a3aa7',
  red: '#e34948',
} as const

export const CATEGORY_STYLES: Record<string, CategoryStyle> = {
  work: { label: 'Work', color: SERIES.blue, bg: '#e3edfb', fg: '#1b4f8e', icon: '💼' },
  jobs_career: { label: 'Jobs & Career', color: SERIES.violet, bg: '#e9e7f7', fg: '#3c2f86', icon: '🧑‍💼' },
  finance: { label: 'Finance', color: SERIES.green, bg: '#dff0df', fg: '#0a5c0a', icon: '💰' },
  bills_payments: { label: 'Bills & Payments', color: SERIES.red, bg: '#fbe4e4', fg: '#9c2c2b', icon: '🧾' },
  shopping: { label: 'Shopping', color: SERIES.orange, bg: '#fce7de', fg: '#a53f19', icon: '🛒' },
  orders_delivery: { label: 'Orders & Delivery', color: SERIES.yellow, bg: '#fbf0d9', fg: '#8a5e00', icon: '📦' },
  travel_booking: { label: 'Travel', color: SERIES.aqua, bg: '#dcf3ea', fg: '#0d6b4a', icon: '✈️' },
  home_services: { label: 'Home & Services', color: SERIES.magenta, bg: '#fbe8ef', fg: '#93365a', icon: '🏠' },
  personal: { label: 'Personal', color: SERIES.magenta, bg: '#fbe8ef', fg: '#93365a', icon: '👤' },
  education: { label: 'Education', color: SERIES.blue, bg: '#e3edfb', fg: '#1b4f8e', icon: '🎓' },
  health: { label: 'Health', color: SERIES.aqua, bg: '#dcf3ea', fg: '#0d6b4a', icon: '🏥' },
  legal_government: { label: 'Legal & Government', color: SERIES.violet, bg: '#e9e7f7', fg: '#3c2f86', icon: '⚖️' },
  marketing_promotions: { label: 'Promotions', color: SERIES.orange, bg: '#fce7de', fg: '#a53f19', icon: '📣' },
  newsletters: { label: 'Newsletters', color: SERIES.yellow, bg: '#fbf0d9', fg: '#8a5e00', icon: '📰' },
  notifications: { label: 'Notifications', color: SERIES.blue, bg: '#e3edfb', fg: '#1b4f8e', icon: '🔔' },
  security: { label: 'Security', color: SERIES.red, bg: '#fbe4e4', fg: '#9c2c2b', icon: '🔐' },
  documents: { label: 'Documents', color: SERIES.violet, bg: '#e9e7f7', fg: '#3c2f86', icon: '📄' },
  meetings_calendar: { label: 'Meetings', color: SERIES.aqua, bg: '#dcf3ea', fg: '#0d6b4a', icon: '📅' },
  communities_social: { label: 'Social', color: SERIES.magenta, bg: '#fbe8ef', fg: '#93365a', icon: '🤝' },
  automated_system: { label: 'Automated', color: SERIES.blue, bg: '#eef2f7', fg: '#44546a', icon: '🤖' },
  spam: { label: 'Spam', color: SERIES.red, bg: '#fbe4e4', fg: '#9c2c2b', icon: '🗑️' },
  other: { label: 'Other', color: '#64748b', bg: '#f1f5f9', fg: '#334155', icon: '❓' },
}

const FALLBACK: CategoryStyle = CATEGORY_STYLES.other

export function categoryStyle(category: string | undefined): CategoryStyle {
  if (!category) return FALLBACK
  return CATEGORY_STYLES[category] ?? { ...FALLBACK, label: humanize(category) }
}

/** Distinct chart colours, assigned in validated slot order. */
export const CHART_SERIES: string[] = [
  SERIES.blue,
  SERIES.orange,
  SERIES.aqua,
  SERIES.yellow,
  SERIES.magenta,
  SERIES.green,
]
export const CHART_OTHER = '#cbd5e1'

export interface PriorityStyle {
  label: string
  bar: string
  dot: string
  text: string
}

// Status colours are reserved and never reused as series colours.
export const PRIORITY_STYLES: Record<string, PriorityStyle> = {
  critical: { label: 'Critical', bar: 'bg-[#d03b3b]', dot: '#d03b3b', text: 'text-[#d03b3b]' },
  high: { label: 'High', bar: 'bg-[#ec835a]', dot: '#ec835a', text: 'text-[#b8562f]' },
  medium: { label: 'Medium', bar: 'bg-[#fab219]', dot: '#fab219', text: 'text-[#8a5e00]' },
  low: {
    label: 'Low',
    bar: 'bg-[var(--color-border-strong)]',
    dot: '#cbd5e1',
    text: 'text-[var(--color-text-muted)]',
  },
  noise: {
    label: 'Noise',
    bar: 'bg-[var(--color-border)]',
    dot: '#e2e8f0',
    text: 'text-[var(--color-text-subtle)]',
  },
}

function humanize(key: string): string {
  return key
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ')
}
