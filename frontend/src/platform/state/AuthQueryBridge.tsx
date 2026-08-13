import { useEffect } from 'react'
import { useAuth } from './auth'
import { setUnauthorizedListener } from '../api/client'

/**
 * Wires the API client's 401 hook to the auth context, so any unauthorized
 * response from either product flips auth status to 'unauthenticated', which
 * the route guards then act on.
 */
export function AuthQueryBridge() {
  const { setUnauthenticated } = useAuth()

  useEffect(() => {
    setUnauthorizedListener(setUnauthenticated)
    return () => setUnauthorizedListener(null)
  }, [setUnauthenticated])

  return null
}
