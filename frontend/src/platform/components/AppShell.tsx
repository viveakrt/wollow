import type { ReactNode } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { IndianRupee, LogOut, Mail, Settings } from 'lucide-react'
import clsx from 'clsx'
import { logout } from '../api/platform'
import { useAuth } from '../state/auth'
import { ThemeToggle } from './ThemeToggle'
import type { ProductId } from '../types'

interface Product {
  id: ProductId
  label: string
  to: string
  icon: typeof Mail
}

// The rail is the only place a product is named. Adding a third product means
// adding a row here and a route branch in App.tsx — nothing else in the shell.
const PRODUCTS: Product[] = [
  { id: 'mail', label: 'Mail', to: '/mail', icon: Mail },
  { id: 'money', label: 'Money', to: '/money', icon: IndianRupee },
]

/**
 * The frame both products render inside: a product rail pinned to the left, the
 * active product's own sidebar next to it, then content. Switching products is
 * a route change inside the same React tree, so the session, the query cache,
 * and the theme all survive it.
 */
export function AppShell({ sidebar, children }: { sidebar?: ReactNode; children: ReactNode }) {
  const navigate = useNavigate()
  const location = useLocation()
  const { setUnauthenticated } = useAuth()

  const logoutMutation = useMutation({
    mutationFn: logout,
    onSettled: () => {
      setUnauthenticated()
      navigate('/login', { replace: true })
    },
  })

  return (
    <div className="flex h-full min-h-0">
      <nav
        aria-label="Products"
        className="flex w-14 shrink-0 flex-col items-center gap-1 border-r border-[var(--color-border)] bg-[var(--color-surface)] py-3"
      >
        <Link
          to="/"
          aria-label="Wollow home"
          className="mb-2 flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--color-accent)] text-sm font-bold text-white"
        >
          W
        </Link>

        {PRODUCTS.map((product) => {
          const active = location.pathname.startsWith(product.to)
          return (
            <NavLink
              key={product.id}
              to={product.to}
              title={product.label}
              aria-label={product.label}
              aria-current={active ? 'page' : undefined}
              className={clsx(
                'flex h-10 w-10 items-center justify-center rounded-lg transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]',
                active
                  ? 'bg-[var(--color-accent-tint)] text-[var(--color-accent-2)]'
                  : 'text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]',
              )}
            >
              <product.icon size={19} />
            </NavLink>
          )
        })}

        <div className="mt-auto flex flex-col items-center gap-1">
          <ThemeToggle />
          <NavLink
            to="/settings"
            title="Settings"
            aria-label="Settings"
            className={({ isActive }) =>
              clsx(
                'flex items-center justify-center rounded-lg p-2 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]',
                isActive
                  ? 'bg-[var(--color-accent-tint)] text-[var(--color-accent-2)]'
                  : 'text-[var(--color-text-muted)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)]',
              )
            }
          >
            <Settings size={18} />
          </NavLink>
          <button
            type="button"
            title="Log out"
            aria-label="Log out"
            onClick={() => logoutMutation.mutate()}
            disabled={logoutMutation.isPending}
            className="flex items-center justify-center rounded-lg p-2 text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] disabled:opacity-50"
          >
            <LogOut size={18} />
          </button>
        </div>
      </nav>

      {sidebar}

      {/* Scrolling is the product's call: Mail runs its own fixed-height panes,
          Money scrolls a single document column. */}
      <main className="flex min-w-0 flex-1 overflow-hidden">{children}</main>
    </div>
  )
}
