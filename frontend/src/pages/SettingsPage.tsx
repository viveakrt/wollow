import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings } from '../platform/api/platform'
import { Spinner } from '../platform/components/Spinner'
import { ApiError } from '../platform/api/client'
import type { AiProvider, SettingsInput } from '../platform/types'

const PROVIDER_OPTIONS: { value: AiProvider; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'lmstudio', label: 'LM Studio (local)' },
  { value: 'custom', label: 'Custom (OpenAI-compatible)' },
]

export function SettingsPage() {
  const queryClient = useQueryClient()
  const settingsQuery = useQuery({ queryKey: ['settings'], queryFn: getSettings })

  const [form, setForm] = useState<SettingsInput>({
    aiProvider: 'none',
    modelName: '',
    baseUrl: '',
    apiKey: '',
  })
  const [hasApiKey, setHasApiKey] = useState(false)
  const [validationError, setValidationError] = useState<string | null>(null)
  const [showSuccess, setShowSuccess] = useState(false)

  useEffect(() => {
    if (settingsQuery.data) {
      setForm({
        aiProvider: settingsQuery.data.aiProvider,
        modelName: settingsQuery.data.modelName,
        baseUrl: settingsQuery.data.baseUrl,
        apiKey: '',
      })
      setHasApiKey(settingsQuery.data.hasApiKey)
    }
  }, [settingsQuery.data])

  const saveMutation = useMutation({
    mutationFn: () => updateSettings(form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['settings'] })
      setForm((prev) => ({ ...prev, apiKey: '' }))
      setShowSuccess(true)
      window.setTimeout(() => setShowSuccess(false), 3000)
    },
  })

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    setValidationError(null)

    if (form.aiProvider === 'custom' && form.baseUrl.trim().length === 0) {
      setValidationError('Base URL is required for a custom provider.')
      return
    }

    saveMutation.mutate()
  }

  const showBaseUrl = form.aiProvider === 'lmstudio' || form.aiProvider === 'custom'
  const baseUrlRequired = form.aiProvider === 'custom'

  const errorMessage =
    validationError ??
    (saveMutation.error instanceof ApiError
      ? saveMutation.error.message
      : saveMutation.error
        ? 'Something went wrong. Please try again.'
        : null)

  if (settingsQuery.isLoading) {
    return (
      <div className="mx-auto w-full max-w-5xl px-4 py-6 sm:px-6">
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6 text-[var(--color-accent-2)]" />
        </div>
      </div>
    )
  }

  return (
    <div className="mx-auto w-full max-w-5xl px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-lg">
        <h1 className="text-xl font-semibold text-[var(--color-text)]">Settings</h1>
        <p className="mt-1 text-sm text-[var(--color-text-muted)]">Configure the AI provider used for message summarization.</p>

        {settingsQuery.isError && (
          <div className="mt-4 rounded-lg bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]">
            {settingsQuery.error instanceof ApiError ? settingsQuery.error.message : 'Failed to load settings.'}
          </div>
        )}

        <form onSubmit={handleSubmit} className="mt-6 space-y-4 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6 shadow-sm">
          <div>
            <label htmlFor="aiProvider" className="mb-1.5 block text-sm font-medium text-[var(--color-text)]">
              AI provider
            </label>
            <select
              id="aiProvider"
              value={form.aiProvider}
              onChange={(e) => setForm((prev) => ({ ...prev, aiProvider: e.target.value as AiProvider }))}
              className={inputClass}
            >
              {PROVIDER_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label htmlFor="modelName" className="mb-1.5 block text-sm font-medium text-[var(--color-text)]">
              Model name
            </label>
            <input
              id="modelName"
              type="text"
              value={form.modelName}
              onChange={(e) => setForm((prev) => ({ ...prev, modelName: e.target.value }))}
              className={inputClass}
              placeholder="e.g. claude-sonnet-4-5"
            />
          </div>

          {showBaseUrl && (
            <div>
              <label htmlFor="baseUrl" className="mb-1.5 block text-sm font-medium text-[var(--color-text)]">
                Base URL {baseUrlRequired ? '(required)' : '(optional)'}
              </label>
              <input
                id="baseUrl"
                type="text"
                value={form.baseUrl}
                onChange={(e) => setForm((prev) => ({ ...prev, baseUrl: e.target.value }))}
                className={inputClass}
                placeholder={form.aiProvider === 'lmstudio' ? 'http://localhost:1234/v1' : 'https://your-server/v1'}
              />
            </div>
          )}

          <div>
            <label htmlFor="apiKey" className="mb-1.5 block text-sm font-medium text-[var(--color-text)]">
              API key
            </label>
            <input
              id="apiKey"
              type="password"
              value={form.apiKey}
              onChange={(e) => setForm((prev) => ({ ...prev, apiKey: e.target.value }))}
              className={inputClass}
              placeholder={hasApiKey ? 'Leave blank to keep existing key' : ''}
            />
          </div>

          {errorMessage && (
            <p className="rounded-lg bg-[var(--color-negative-tint)] px-3 py-2 text-sm text-[var(--color-negative)]" role="alert">
              {errorMessage}
            </p>
          )}

          {showSuccess && (
            <p className="rounded-lg bg-[var(--color-positive-tint)] px-3 py-2 text-sm text-[var(--color-positive)]" role="status">
              Settings saved.
            </p>
          )}

          <button
            type="submit"
            disabled={saveMutation.isPending}
            className="flex w-full items-center justify-center gap-2 rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-white shadow-sm transition hover:bg-[var(--color-accent)] focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {saveMutation.isPending && <Spinner />}
            {saveMutation.isPending ? 'Saving...' : 'Save settings'}
          </button>
        </form>
      </div>
    </div>
  )
}

const inputClass =
  'w-full rounded-lg border border-[var(--color-border-strong)] px-3 py-2 text-sm shadow-sm focus:border-[var(--color-accent)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]'
