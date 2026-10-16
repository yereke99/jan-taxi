// The admin panel's API client: cookie session (credentials: 'include'),
// X-CSRF-Token on state-changing requests, and a hook that sends the operator
// back to the login page when the session expires.

import { csrfHeaders, setCsrfToken } from './csrf'
import { API_BASE, createHttpClient, isApiError, type ApiError, type HttpClient, type HttpMethod, type Query } from './http'

export { ApiError, isAbortError, isApiError, type Query, type RequestOptions } from './http'

let onUnauthorized: ((err: ApiError) => void) | null = null

/** Registered once in main.ts (clears the session and redirects to login). */
export function setUnauthorizedHandler(fn: ((err: ApiError) => void) | null): void {
  onUnauthorized = fn
}

const base = createHttpClient({
  credentials: 'include',
  auth: null,
  headers: (method) => csrfHeaders(method),
  onUnauthorized: (err) => onUnauthorized?.(err),
})

let csrfRefresh: Promise<boolean> | null = null

/** Re-reads the CSRF token from /admin/auth/me (e.g. after storage was cleared). */
function refreshCsrf(): Promise<boolean> {
  csrfRefresh ??= base
    .get<{ csrf_token?: unknown }>('/admin/auth/me')
    .then((me) => {
      const token = typeof me?.csrf_token === 'string' ? me.csrf_token : ''
      if (token) setCsrfToken(token)
      return token !== ''
    })
    .catch(() => false)
    .finally(() => {
      csrfRefresh = null
    })
  return csrfRefresh
}

/** A state-changing request rejected with csrf_failed is retried once with a fresh token. */
async function withCsrfRetry<T>(method: HttpMethod, run: () => Promise<T>): Promise<T> {
  try {
    return await run()
  } catch (err) {
    if (method === 'GET' || !isApiError(err) || err.code !== 'csrf_failed') throw err
    if (!(await refreshCsrf())) throw err
    return run()
  }
}

export const api: HttpClient = {
  ...base,
  request: (method, path, body, opts) => withCsrfRetry(method, () => base.request(method, path, body, opts)),
  post: (path, body, opts) => withCsrfRetry('POST', () => base.post(path, body, opts)),
  put: (path, body, opts) => withCsrfRetry('PUT', () => base.put(path, body, opts)),
  patch: (path, body, opts) => withCsrfRetry('PATCH', () => base.patch(path, body, opts)),
  delete: (path, opts) => withCsrfRetry('DELETE', () => base.delete(path, opts)),
  upload: (path, form, opts) => withCsrfRetry('POST', () => base.upload(path, form, opts)),
}

/** Admin list envelope (§10). */
export interface Page<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

export const DEFAULT_PAGE_SIZE = 25

/**
 * Converts a URL returned by the API (`/api/v1/admin/files/…`) into a path
 * for the client, which adds `/api/v1` itself. Other values pass unchanged.
 */
export function apiPath(url: string): string {
  return url.startsWith(`${API_BASE}/`) ? url.slice(API_BASE.length) : url
}

/**
 * Downloads a file through the API client (session cookie, error envelope)
 * and hands it to the browser as a download. Resolves with the file name used.
 */
export async function downloadFile(path: string, query: Query | undefined, fallbackName: string): Promise<string> {
  const res = await api.blob(apiPath(path), { query, timeoutMs: 120_000 })
  const name = res.filename ?? fallbackName
  const href = URL.createObjectURL(res.blob)
  try {
    const a = document.createElement('a')
    a.href = href
    a.download = name
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    // Give the browser a moment to start the download before revoking.
    setTimeout(() => URL.revokeObjectURL(href), 10_000)
  }
  return name
}
