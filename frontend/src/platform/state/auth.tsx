import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { getSettings } from '../api/platform'

export type AuthStatus = 'unknown' | 'authenticated' | 'unauthenticated'

interface AuthContextValue {
  status: AuthStatus
  setAuthenticated: () => void
  setUnauthenticated: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>('unknown')

  const setAuthenticated = useCallback(() => setStatus('authenticated'), [])
  const setUnauthenticated = useCallback(() => setStatus('unauthenticated'), [])

  // The session lives in an HttpOnly cookie, so the client cannot read it — on
  // a reload or a deep link it has to ask the server whether it is still valid.
  // Without this probe every refresh bounces to the login screen even though
  // the cookie is fine.
  useEffect(() => {
    let cancelled = false
    getSettings()
      .then(() => {
        if (!cancelled) setStatus('authenticated')
      })
      .catch(() => {
        if (!cancelled) setStatus('unauthenticated')
      })
    return () => {
      cancelled = true
    }
  }, [])

  const value = useMemo(
    () => ({ status, setAuthenticated, setUnauthenticated }),
    [status, setAuthenticated, setUnauthenticated],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within an AuthProvider')
  return ctx
}
