import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { login } from '../platform/api/platform'
import { ApiError } from '../platform/api/client'
import { useAuth } from '../platform/state/auth'

export function LoginPage() {
  const [password, setPassword] = useState('')
  const navigate = useNavigate()
  const { setAuthenticated } = useAuth()

  const loginMutation = useMutation({
    mutationFn: () => login(password),
    onSuccess: () => {
      setAuthenticated()
      navigate('/', { replace: true })
    },
  })

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    loginMutation.mutate()
  }

  const errorMessage =
    loginMutation.error instanceof ApiError ? loginMutation.error.message : loginMutation.error ? 'Something went wrong. Please try again.' : null

  return (
    <div className="flex min-h-full items-center justify-center bg-[var(--color-bg)] px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-2">
          <span className="flex h-12 w-12 items-center justify-center rounded-xl bg-[var(--color-accent)] text-xl font-semibold text-white">
            W
          </span>
          <h1 className="text-2xl font-semibold text-[var(--color-text)]">Wollow</h1>
          <p className="text-sm text-[var(--color-text-muted)]">Sign in to manage your inbox</p>
        </div>

        <form onSubmit={handleSubmit} className="rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6 shadow-sm">
          <label htmlFor="password" className="mb-1.5 block text-sm font-medium text-[var(--color-text)]">
            Password
          </label>
          <input
            id="password"
            type="password"
            autoFocus
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-lg border border-[var(--color-border-strong)] px-3 py-2 text-sm shadow-sm focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
            placeholder="Enter your password"
          />

          {errorMessage && (
            <p className="mt-3 rounded-lg bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]" role="alert">
              {errorMessage}
            </p>
          )}

          <button
            type="submit"
            disabled={loginMutation.isPending || password.length === 0}
            className="mt-5 flex w-full items-center justify-center rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-[var(--color-accent)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {loginMutation.isPending ? 'Signing in...' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  )
}
