import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  Wallet,
  ArrowLeftRight,
  Shuffle,
  TrendingUp,
  Upload,
  Settings,
  Receipt,
} from 'lucide-react'
import clsx from 'clsx'

const NAV_ITEMS = [
  { to: '/money', label: 'Dashboard', icon: LayoutDashboard, end: true },
  { to: '/money/accounts', label: 'Accounts', icon: Wallet },
  { to: '/money/transactions', label: 'Transactions', icon: ArrowLeftRight },
  { to: '/money/transfers', label: 'Transfers', icon: Shuffle },
  { to: '/money/investments', label: 'Investments', icon: TrendingUp },
  { to: '/money/bills', label: 'Bills', icon: Receipt },
  { to: '/money/import', label: 'Import Statement', icon: Upload },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  clsx(
    'flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors',
    isActive
      ? 'bg-[var(--color-accent-tint)] text-[var(--color-accent-2)]'
      : 'text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]',
  )

export function MoneySidebar() {
  return (
    <aside className="flex h-full w-56 shrink-0 flex-col border-r border-[var(--color-border)] bg-[var(--color-surface)]">
      <div className="flex h-14 items-center px-5">
        <span className="text-base font-semibold tracking-tight">Money</span>
      </div>

      <nav className="flex-1 space-y-1 overflow-y-auto px-3 pb-4">
        {NAV_ITEMS.map(({ to, label, icon: Icon, end }) => (
          <NavLink key={to} to={to} end={end} className={linkClass}>
            <Icon size={17} />
            {label}
          </NavLink>
        ))}
      </nav>

      <div className="border-t border-[var(--color-border)] px-3 py-4">
        <NavLink to="/money/settings" className={linkClass}>
          <Settings size={17} />
          Money settings
        </NavLink>
      </div>
    </aside>
  )
}
