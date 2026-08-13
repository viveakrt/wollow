export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }

  get isUnauthorized(): boolean {
    return this.status === 401
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  query?: Record<string, string | number | undefined>
}

type UnauthorizedListener = () => void

let unauthorizedListener: UnauthorizedListener | null = null

/**
 * Registers the callback fired whenever any request comes back 401.
 *
 * This lives in the client rather than in TanStack Query's QueryCache so that
 * every caller is covered — Money's pages predate the shared query client and
 * some still fetch directly, and a 401 has to route them to the login screen
 * just the same.
 */
export function setUnauthorizedListener(listener: UnauthorizedListener | null) {
  unauthorizedListener = listener
}

function buildQueryString(query?: Record<string, string | number | undefined>): string {
  if (!query) return ''
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined) params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `?${qs}` : ''
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, query } = options

  const response = await fetch(`${path}${buildQueryString(query)}`, {
    method,
    credentials: 'include',
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  let payload: unknown = null
  const text = await response.text()
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = null
    }
  }

  if (!response.ok) {
    const message =
      payload && typeof payload === 'object' && 'error' in payload && typeof (payload as { error: unknown }).error === 'string'
        ? (payload as { error: string }).error
        : `Request failed with status ${response.status}`
    if (response.status === 401) unauthorizedListener?.()
    throw new ApiError(response.status, message)
  }

  return payload as T
}

export const api = {
  get: <T>(path: string, query?: Record<string, string | number | undefined>) =>
    request<T>(path, { method: 'GET', query }),
  post: <T>(path: string, body?: unknown, query?: Record<string, string | number | undefined>) =>
    request<T>(path, { method: 'POST', body, query }),
  put: <T>(path: string, body?: unknown, query?: Record<string, string | number | undefined>) =>
    request<T>(path, { method: 'PUT', body, query }),
  delete: <T>(path: string, query?: Record<string, string | number | undefined>) =>
    request<T>(path, { method: 'DELETE', query }),
}
