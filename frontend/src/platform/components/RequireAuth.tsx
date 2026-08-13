import type { ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../state/auth'
import { Spinner } from './Spinner'

/**
 * Guards a route until the session probe in AuthProvider has resolved. The
 * 'unknown' state must render a placeholder rather than redirect — bouncing
 * straight to /login would log the user out on every refresh and break every
 * deep link into a product.
 */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { status } = useAuth()

  if (status === 'unknown') return <AuthPending />
  if (status !== 'authenticated') return <Navigate to="/login" replace />

  return <>{children}</>
}

export function RedirectIfAuthed({ children }: { children: ReactNode }) {
  const { status } = useAuth()

  if (status === 'unknown') return <AuthPending />
  if (status === 'authenticated') return <Navigate to="/mail" replace />

  return <>{children}</>
}

function AuthPending() {
  return (
    <div className="flex h-full w-full items-center justify-center bg-[var(--color-bg)]">
      <Spinner className="h-6 w-6 text-[var(--color-accent)]" />
    </div>
  )
}
