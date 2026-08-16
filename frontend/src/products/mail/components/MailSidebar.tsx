import { Link } from 'react-router-dom'
import {
  AlertTriangle,
  CircleDot,
  CornerUpLeft,
  Inbox,
  Landmark,
  Mail,
  Megaphone,
  Newspaper,
  Package,
  Plane,
  Plus,
  Receipt,
  ShieldCheck,
  Star,
  Zap,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import clsx from 'clsx'
import type { Account } from '../types'
import type { Insights } from '../api'

export interface SmartView {
  id: string
  label: string
  icon: LucideIcon
}

// Order is deliberate: triage views first (what needs me), then money and
// logistics, then the low-priority bulk.
export const SMART_VIEWS: SmartView[] = [
  { id: 'important', label: 'Important', icon: AlertTriangle },
  { id: 'needs_action', label: 'Needs Action', icon: Zap },
  { id: 'needs_reply', label: 'Needs Reply', icon: CornerUpLeft },
  { id: 'bills', label: 'Bills & Payments', icon: Receipt },
  { id: 'finance', label: 'Finance', icon: Landmark },
  { id: 'orders', label: 'Orders & Delivery', icon: Package },
  { id: 'travel', label: 'Travel', icon: Plane },
  { id: 'security', label: 'Security', icon: ShieldCheck },
  { id: 'newsletters', label: 'Newsletters', icon: Newspaper },
  { id: 'promotions', label: 'Promotions', icon: Megaphone },
]

interface SidebarProps {
  accounts: Account[]
  selectedAccountId: number | null
  onSelectAccount: (id: number) => void
  insights?: Insights
  activeView: string | null
  onSelectView: (view: string | null) => void
}

// Settings and log out live on the product rail, not here — they are platform
// concerns and would otherwise be duplicated in every product's sidebar.
export function Sidebar({
  accounts,
  selectedAccountId,
  onSelectAccount,
  insights,
  activeView,
  onSelectView,
}: SidebarProps) {
  const counts = insights?.smartViews ?? {}
  const totals = insights?.totals

  return (
    <aside className="flex h-full w-56 shrink-0 flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)]">
      <div className="flex h-14 shrink-0 items-center px-5">
        <span className="text-base font-semibold tracking-tight">Mail</span>
      </div>

      <nav className="min-h-0 flex-1 overflow-y-auto px-3 pb-2">
        <NavItem
          label="Inbox"
          icon={Inbox}
          count={totals?.messages}
          active={activeView === null}
          onClick={() => onSelectView(null)}
        />
        <NavItem
          label="Unread"
          icon={CircleDot}
          count={counts.unread}
          active={activeView === 'unread'}
          onClick={() => onSelectView('unread')}
        />
        <NavItem
          label="Starred"
          icon={Star}
          count={counts.starred}
          active={activeView === 'starred'}
          onClick={() => onSelectView('starred')}
        />

        <p className="mt-4 px-3 pb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--color-text-subtle)]">
          Smart Views
        </p>
        {SMART_VIEWS.map((view) => (
          <NavItem
            key={view.id}
            label={view.label}
            icon={view.icon}
            count={counts[view.id]}
            active={activeView === view.id}
            onClick={() => onSelectView(view.id)}
          />
        ))}

        <p className="mt-4 px-3 pb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--color-text-subtle)]">
          Browse
        </p>
        <Link
          to="/mail/senders"
          className={clsx(
            'flex w-full items-center gap-2 rounded-lg px-3 py-1.5 text-left text-sm transition-colors',
            'text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]',
          )}
        >
          <Mail size={15} />
          <span>Senders</span>
        </Link>

        <p className="mt-4 px-3 pb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--color-text-subtle)]">
          Mailboxes
        </p>
        {accounts.map((account) => (
          <button
            key={account.id}
            type="button"
            onClick={() => onSelectAccount(account.id)}
            className={clsx(
              'flex w-full items-center gap-2 rounded-lg px-3 py-1.5 text-left text-sm transition-colors',
              selectedAccountId === account.id
                ? 'bg-[var(--color-accent-tint)] font-medium text-[var(--color-accent-2)]'
                : 'text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]',
            )}
          >
            <span className="truncate">{account.label}</span>
          </button>
        ))}
        <Link
          to="/mail/accounts/new"
          className="mt-1 flex w-full items-center gap-2 rounded-lg px-3 py-1.5 text-sm text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]"
        >
          <Plus size={15} />
          Add mailbox
        </Link>
      </nav>

      {totals && (
        <div className="shrink-0 border-t border-[var(--color-border)] px-4 py-3">
          <div className="flex items-center justify-between text-[11px] text-[var(--color-text-subtle)]">
            <span>Classified</span>
            <span className="tabular-nums">{pctOf(totals.classified, totals.messages)}</span>
          </div>
          <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-[var(--color-surface-2)]">
            <div
              className="h-full rounded-full bg-[var(--color-accent)] transition-all"
              style={{
                width: `${totals.messages ? (totals.classified / totals.messages) * 100 : 0}%`,
              }}
            />
          </div>
        </div>
      )}
    </aside>
  )
}

function NavItem({
  label,
  icon: Icon,
  count,
  active,
  onClick,
}: {
  label: string
  icon: LucideIcon
  count?: number
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx(
        'flex w-full items-center gap-2.5 rounded-lg px-3 py-1.5 text-left text-sm transition-colors',
        active
          ? 'bg-[var(--color-accent-tint)] font-medium text-[var(--color-accent-2)]'
          : 'text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]',
      )}
    >
      <Icon size={15} className="shrink-0" />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {count !== undefined && count > 0 && (
        <span className="shrink-0 text-xs tabular-nums text-[var(--color-text-subtle)]">
          {count > 999 ? `${(count / 1000).toFixed(1)}K` : count}
        </span>
      )}
    </button>
  )
}

function pctOf(part: number, whole: number): string {
  if (!whole) return '0%'
  return `${Math.round((part / whole) * 100)}%`
}
