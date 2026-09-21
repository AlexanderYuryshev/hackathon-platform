// On the server the API address must come from runtime configuration
// (process.env.API_URL), not from build-time env: the same bundle may be
// deployed behind different proxies. In the browser everything is same-origin.
const API_BASE = import.meta.env.SSR
  ? (typeof process !== 'undefined' && process.env?.API_URL) || import.meta.env.VITE_API_URL || 'http://localhost:8080'
  : ''

let csrfToken: string | null =
  typeof sessionStorage !== 'undefined' ? sessionStorage.getItem('csrf') : null

export function setCsrfToken(token: string) {
  csrfToken = token
  if (typeof sessionStorage !== 'undefined') {
    sessionStorage.setItem('csrf', token)
  }
}

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

export interface SsrHeaders {
  headers?: Record<string, string>
}

// Resolves the incoming request cookies during SSR (request-scoped via
// TanStack Start's AsyncLocalStorage). Without them the backend sees an
// anonymous user and would leak e.g. the leaderboard to a judge inside
// server-rendered HTML (breaking judge blindness + hydration). On the client
// this is always empty: the browser sends cookies automatically.
export async function ssrRequestHeaders(): Promise<SsrHeaders> {
  if (!import.meta.env.SSR) {
    return {}
  }
  try {
    const { getRequest } = await import('@tanstack/react-start/server')
    const cookie = getRequest().headers.get('cookie')
    return cookie ? { headers: { cookie } } : {}
  } catch {
    return {}
  }
}

async function request<T>(method: string, path: string, body?: unknown, opts?: SsrHeaders): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  // Forward the incoming request cookies during SSR only: without them the
  // backend sees an anonymous user and would leak e.g. the leaderboard to a
  // judge inside server-rendered HTML (breaking judge blindness + hydration).
  // Browsers forbid setting the Cookie header from JS, so this is a no-op there.
  if (import.meta.env.SSR && opts?.headers) {
    for (const [k, v] of Object.entries(opts.headers)) {
      headers[k] = v
    }
  }
  if (csrfToken && method !== 'GET') {
    headers['X-CSRF-Token'] = csrfToken
  }
  const res = await fetch(`${API_BASE}/api${path}`, {
    method,
    headers,
    credentials: 'include',
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let code = 'error'
    let message = res.statusText
    try {
      const data = await res.json()
      code = data.error ?? code
      message = data.message ?? message
    } catch {}
    throw new ApiError(res.status, code, message)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return res.json() as Promise<T>
}

export const api = {
  get: <T>(path: string, opts?: SsrHeaders) => request<T>('GET', path, undefined, opts),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {}),
}

export interface User {
  id: string
  email: string
  name: string
}

export async function me(): Promise<{ user: User; csrf_token: string }> {
  const res = await api.get<{ user: User; csrf_token: string }>('/me')
  if (res.csrf_token) {
    setCsrfToken(res.csrf_token)
  }
  return res
}

export async function login(email: string, password: string) {
  const res = await api.post<{ user: User; csrf_token: string }>('/auth/login', {
    email,
    password,
  })
  setCsrfToken(res.csrf_token)
  return res
}

export async function logout() {
  await api.post('/auth/logout')
  csrfToken = null
  if (typeof sessionStorage !== 'undefined') {
    sessionStorage.removeItem('csrf')
  }
}
