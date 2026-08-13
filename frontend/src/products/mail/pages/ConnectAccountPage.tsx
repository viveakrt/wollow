import { useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createAccount } from '../api'
import { ApiError } from '../../../platform/api/client'
import { Spinner } from '../../../platform/components/Spinner'
import type { NewAccountInput } from '../types'

const initialForm: NewAccountInput = {
  label: '',
  imapHost: '',
  imapPort: 993,
  smtpHost: '',
  smtpPort: 587,
  username: '',
  password: '',
  useTls: true,
}

export function ConnectAccountPage() {
  const [form, setForm] = useState<NewAccountInput>(initialForm)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const createMutation = useMutation({
    mutationFn: () => createAccount(form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accounts'] })
      navigate('/mail', { replace: true })
    },
  })

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    createMutation.mutate()
  }

  function updateField<K extends keyof NewAccountInput>(key: K, value: NewAccountInput[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  const errorMessage =
    createMutation.error instanceof ApiError
      ? createMutation.error.message
      : createMutation.error
        ? 'Something went wrong. Please try again.'
        : null

  return (
    <div className="mx-auto h-full w-full max-w-5xl overflow-y-auto px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-lg">
        <h1 className="text-xl font-semibold text-[var(--color-text)]">Connect an account</h1>
        <p className="mt-1 text-sm text-[var(--color-text-muted)]">
          Enter your mail server details. We'll verify the connection before saving.
        </p>

        <form onSubmit={handleSubmit} className="mt-6 space-y-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6 shadow-sm">
          <Field label="Label">
            <input
              type="text"
              required
              value={form.label}
              onChange={(e) => updateField('label', e.target.value)}
              className={inputClass}
              placeholder="Personal Gmail"
            />
          </Field>

          <div className="grid grid-cols-2 gap-4">
            <Field label="IMAP host">
              <input
                type="text"
                required
                value={form.imapHost}
                onChange={(e) => updateField('imapHost', e.target.value)}
                className={inputClass}
                placeholder="imap.example.com"
              />
            </Field>
            <Field label="IMAP port">
              <input
                type="number"
                required
                value={form.imapPort}
                onChange={(e) => updateField('imapPort', Number(e.target.value))}
                className={inputClass}
              />
            </Field>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <Field label="SMTP host">
              <input
                type="text"
                required
                value={form.smtpHost}
                onChange={(e) => updateField('smtpHost', e.target.value)}
                className={inputClass}
                placeholder="smtp.example.com"
              />
            </Field>
            <Field label="SMTP port">
              <input
                type="number"
                required
                value={form.smtpPort}
                onChange={(e) => updateField('smtpPort', Number(e.target.value))}
                className={inputClass}
              />
            </Field>
          </div>

          <Field label="Username">
            <input
              type="text"
              required
              value={form.username}
              onChange={(e) => updateField('username', e.target.value)}
              className={inputClass}
              placeholder="you@example.com"
            />
          </Field>

          <Field label="Password">
            <input
              type="password"
              required
              value={form.password}
              onChange={(e) => updateField('password', e.target.value)}
              className={inputClass}
            />
          </Field>

          <label className="flex items-center gap-2 text-sm text-[var(--color-text)]">
            <input
              type="checkbox"
              checked={form.useTls}
              onChange={(e) => updateField('useTls', e.target.checked)}
              className="h-4 w-4 rounded border-[var(--color-border-strong)] text-[var(--color-accent-2)] focus:ring-[var(--color-accent)]"
            />
            Use TLS
          </label>

          {errorMessage && (
            <p className="rounded-lg bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]" role="alert">
              {errorMessage}
            </p>
          )}

          <button
            type="submit"
            disabled={createMutation.isPending}
            className="flex w-full items-center justify-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-[var(--color-accent)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {createMutation.isPending && <Spinner />}
            {createMutation.isPending ? 'Verifying connection...' : 'Connect account'}
          </button>
        </form>
      </div>
    </div>
  )
}

const inputClass =
  'w-full rounded-lg border border-[var(--color-border-strong)] px-3 py-2 text-sm shadow-sm focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]'

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <label className="mb-1.5 block text-sm font-medium text-[var(--color-text)]">{label}</label>
      {children}
    </div>
  )
}
