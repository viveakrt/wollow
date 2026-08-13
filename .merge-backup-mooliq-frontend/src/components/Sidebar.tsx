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
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/accounts', label: 'Accounts', icon: Wallet },
  { to: '/transactions', label: 'Transactions', icon: ArrowLeftRight },
  { to: '/transfers', label: 'Transfers', icon: Shuffle },
  { to: '/investments', label: 'Investments', icon: TrendingUp },
  { to: '/bills', label: 'Bills', icon: Receipt },
  { to: '/import', label: 'Import Statement', icon: Upload },
]

export function Sidebar() {
  return (
    <aside className="w-60 shrink-0 border-r border-[var(--color-border)] bg-[var(--color-surface)] flex flex-col h-screen sticky top-0">
      <div className="flex items-center gap-2 px-5 h-16 border-b border-[var(--color-border)]">
        <div className="w-7 h-7 rounded-md bg-gradient-to-br from-violet-500 to-purple-600 flex items-center justify-center">
          <TrendingUp size={16} className="text-white" />
        </div>
        <span className="font-semibold text-lg tracking-tight">Mooliq</span>
      </div>

      <nav className="flex-1 px-3 py-4 space-y-1 overflow-y-auto">
        {NAV_ITEMS.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              clsx(
                'flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors',
                isActive
                  ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent-2)]'
                  : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-white/5',
              )
            }
          >
            <Icon size={17} />
            {label}
          </NavLink>
        ))}
      </nav>

      <div className="px-3 py-4 border-t border-[var(--color-border)]">
        <NavLink
          to="/settings"
          className={({ isActive }) =>
            clsx(
              'flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors',
              isActive
                ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent-2)]'
                : 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] hover:bg-white/5',
            )
          }
        >
          <Settings size={17} />
          Settings
        </NavLink>
      </div>
    </aside>
  )
}
