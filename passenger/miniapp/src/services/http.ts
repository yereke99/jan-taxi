// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Typed fetch wrapper for /api/v1 (ARCHITECTURE §10):
//   * JSON in/out, snake_case payloads passed through untouched;
//   * errors are always ApiError {status, code, message, fields, details},
//     parsed from {"error":{...}} or synthesised for network/timeouts;
//   * Bearer token from the auth service; on 401 the client re-runs the
//     Telegram initData authentication once and retries the request once;
//   * AbortSignal support and a per-request timeout.

import { uuidv4 } from '../utils/id'

export const API_BASE = '/api/v1'

const DEFAULT_TIMEOUT_MS = 20_000
const UPLOAD_TIMEOUT_MS = 90_000

export class ApiError extends Error {
  override readonly name = 'ApiError'

  constructor(
    /** HTTP status; 0 for network failures and timeouts. */
    readonly status: number,
    /** Machine code from the API (translate by this), or a client-side code. */
    readonly code: string,
    message: string,
    /** Per-field reason codes, e.g. {"comment": "too_long"}. */
    readonly fields: Readonly<Record<string, string>> = {},
    readonly details: Readonly<Record<string, unknown>> = {},
  ) {
    super(message)
  }

  /** True when the request never reached the server or timed out. */
  get isNetworkError(): boolean {
    return this.status === 0
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}

/** True when the caller aborted the request through its AbortSignal. */
export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

/** Code used when a response carries no parsable error envelope. */
export function fallbackErrorCode(status: number): string {
  switch (status) {
    case 400:
      return 'bad_request'
    case 401:
      return 'unauthorized'
    case 403:
      return 'forbidden'
    case 404:
      return 'not_found'
    case 409:
      return 'conflict'
    case 422:
      return 'validation_failed'
    case 429:
      return 'too_many_requests'
    default:
      return status >= 500 ? 'internal' : 'bad_request'
  }
}

function asStringRecord(v: unknown): Record<string, string> {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return {}
  const out: Record<string, string> = {}
  for (const [k, val] of Object.entries(v)) if (typeof val === 'string') out[k] = val
  return out
}

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? { ...(v as Record<string, unknown>) } : {}
}

/** Builds an ApiError from a non-2xx response. Never throws. */
export async function toApiError(res: Response): Promise<ApiError> {
  let body: unknown = null
  try {
    const text = await res.text()
    body = text ? JSON.parse(text) : null
  } catch {
    body = null
  }
  const env = asRecord(asRecord(body).error)
  const code = typeof env.code === 'string' && env.code ? env.code : fallbackErrorCode(res.status)
  const message = typeof env.message === 'string' && env.message ? env.message : `HTTP ${res.status}`
  const details = asRecord(env.details)
  const retryAfter = Number(res.headers.get('Retry-After'))
  if (res.status === 429 && Number.isFinite(retryAfter) && retryAfter > 0 && details.retry_after_s === undefined) {
    details.retry_after_s = retryAfter
  }
  return new ApiError(res.status, code, message, asStringRecord(env.fields), details)
}

export type QueryValue = string | number | boolean | null | undefined
export type Query = Record<string, QueryValue | readonly QueryValue[]>

export interface RequestOptions {
  query?: Query
  signal?: AbortSignal
  headers?: Record<string, string>
  /** Milliseconds; 0 disables the timeout. */
  timeoutMs?: number
}

export interface AuthProvider {
  getToken(): string | null
  /** Re-authenticates (Telegram initData) and resolves true on success. */
  reauthenticate(): Promise<boolean>
}

export interface HttpClientOptions {
  baseUrl?: string
  /** null disables the Authorization header and the 401 retry. */
  auth?: AuthProvider | null
  fetchImpl?: typeof fetch
  credentials?: RequestCredentials
  /** Extra headers computed per request (e.g. CSRF). */
  headers?: (method: string) => Record<string, string>
  /** Called after a 401 that could not be recovered. */
  onUnauthorized?: (err: ApiError) => void
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface HttpClient {
  request<T>(method: HttpMethod, path: string, body?: unknown, opts?: RequestOptions): Promise<T>
  get<T>(path: string, opts?: RequestOptions): Promise<T>
  /** GET that resolves null on 204 No Content (e.g. "no active order"). */
  getOptional<T>(path: string, opts?: RequestOptions): Promise<T | null>
  post<T>(path: string, body?: unknown, opts?: RequestOptions): Promise<T>
  put<T>(path: string, body?: unknown, opts?: RequestOptions): Promise<T>
  patch<T>(path: string, body?: unknown, opts?: RequestOptions): Promise<T>
  delete<T>(path: string, opts?: RequestOptions): Promise<T>
  /** multipart/form-data POST with a longer timeout. */
  upload<T>(path: string, form: FormData, opts?: RequestOptions): Promise<T>
  /** Authorised GET of a binary resource (e.g. a private JPEG) as a Blob. */
  getBlob(path: string, opts?: RequestOptions): Promise<Blob>
}

/** Joins base, path and query; arrays become repeated keys, null/undefined are skipped. */
export function buildUrl(baseUrl: string, path: string, query?: Query): string {
  const base = baseUrl.replace(/\/+$/, '')
  const p = path.startsWith('/') ? path : `/${path}`
  if (!query) return base + p
  const params = new URLSearchParams()
  for (const [key, raw] of Object.entries(query)) {
    const values = Array.isArray(raw) ? raw : [raw]
    for (const v of values) if (v !== null && v !== undefined) params.append(key, String(v))
  }
  const qs = params.toString()
  return qs ? `${base}${p}?${qs}` : base + p
}

/**
 * Process-wide replacement for fetch() used by every client created without an
 * explicit fetchImpl. Only the presentation build (VITE_DEMO) sets it, to serve
 * the API from in-memory fixtures; production never calls setHttpTransport.
 */
let transport: typeof fetch | null = null

export function setHttpTransport(impl: typeof fetch | null): void {
  transport = impl
}

export function createHttpClient(options: HttpClientOptions = {}): HttpClient {
  const baseUrl = options.baseUrl ?? API_BASE
  const auth = options.auth ?? null
  const fetchImpl = options.fetchImpl ?? ((input: RequestInfo | URL, init?: RequestInit) => (transport ?? fetch)(input, init))
  let reauthInFlight: Promise<boolean> | null = null

  function reauthOnce(): Promise<boolean> {
    if (!auth) return Promise.resolve(false)
    reauthInFlight ??= auth
      .reauthenticate()
      .catch(() => false)
      .finally(() => {
        reauthInFlight = null
      })
    return reauthInFlight
  }

  async function send(method: HttpMethod, path: string, body: unknown, opts: RequestOptions, isUpload: boolean): Promise<Response> {
    const headers: Record<string, string> = { Accept: 'application/json', ...options.headers?.(method), ...opts.headers }
    let payload: BodyInit | undefined
    if (body instanceof FormData) payload = body
    else if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      payload = JSON.stringify(body)
    }
    const token = auth?.getToken()
    if (token) headers.Authorization = `Bearer ${token}`

    const controller = new AbortController()
    const external = opts.signal
    if (external?.aborted) throw external.reason ?? new DOMException('Aborted', 'AbortError')
    const forwardAbort = (): void => controller.abort(external?.reason)
    external?.addEventListener('abort', forwardAbort, { once: true })

    let timedOut = false
    const timeoutMs = opts.timeoutMs ?? (isUpload ? UPLOAD_TIMEOUT_MS : DEFAULT_TIMEOUT_MS)
    const timer =
      timeoutMs > 0
        ? setTimeout(() => {
            timedOut = true
            controller.abort()
          }, timeoutMs)
        : undefined

    try {
      return await fetchImpl(buildUrl(baseUrl, path, opts.query), {
        method,
        headers,
        body: payload,
        signal: controller.signal,
        credentials: options.credentials ?? 'same-origin',
      })
    } catch (err) {
      if (external?.aborted) throw isAbortError(err) ? err : new DOMException('Aborted', 'AbortError')
      if (timedOut) throw new ApiError(0, 'timeout', `${method} ${path} timed out`)
      throw new ApiError(0, 'network_error', `${method} ${path} failed: network error`)
    } finally {
      if (timer !== undefined) clearTimeout(timer)
      external?.removeEventListener('abort', forwardAbort)
    }
  }

  async function parse<T>(res: Response): Promise<T> {
    if (res.status === 204 || res.status === 205) return undefined as T
    const text = await res.text()
    if (!text) return undefined as T
    try {
      return JSON.parse(text) as T
    } catch {
      throw new ApiError(res.status, 'bad_response', 'response is not valid JSON')
    }
  }

  /** Sends with the single 401 re-authentication retry; throws ApiError on non-2xx. */
  async function exchange(method: HttpMethod, path: string, body: unknown, opts: RequestOptions, isUpload: boolean): Promise<Response> {
    let res = await send(method, path, body, opts, isUpload)
    if (res.status === 401 && auth) {
      const recovered = await reauthOnce()
      if (recovered) res = await send(method, path, body, opts, isUpload)
    }
    if (!res.ok) {
      const err = await toApiError(res)
      if (res.status === 401) options.onUnauthorized?.(err)
      throw err
    }
    return res
  }

  async function request<T>(method: HttpMethod, path: string, body?: unknown, opts: RequestOptions = {}, isUpload = false): Promise<T> {
    return parse<T>(await exchange(method, path, body, opts, isUpload))
  }

  async function getBlob(path: string, opts: RequestOptions = {}): Promise<Blob> {
    const res = await exchange('GET', path, undefined, { ...opts, headers: { Accept: '*/*', ...opts.headers } }, false)
    try {
      return await res.blob()
    } catch {
      throw new ApiError(res.status, 'bad_response', 'response body could not be read')
    }
  }

  return {
    request: <T>(method: HttpMethod, path: string, body?: unknown, opts?: RequestOptions) =>
      request<T>(method, path, body, opts),
    get: <T>(path: string, opts?: RequestOptions) => request<T>('GET', path, undefined, opts),
    getOptional: async <T>(path: string, opts?: RequestOptions) => {
      const v = await request<T | undefined>('GET', path, undefined, opts)
      return v === undefined ? null : v
    },
    post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('POST', path, body, opts),
    put: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('PUT', path, body, opts),
    patch: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('PATCH', path, body, opts),
    delete: <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, undefined, opts),
    upload: <T>(path: string, form: FormData, opts?: RequestOptions) => request<T>('POST', path, form, opts, true),
    getBlob,
  }
}

let boundAuth: AuthProvider | null = null

/** Connects the default client to the auth service (done by services/auth). */
export function bindAuth(provider: AuthProvider | null): void {
  boundAuth = provider
}

/** The app-wide API client. */
export const http: HttpClient = createHttpClient({
  auth: {
    getToken: () => boundAuth?.getToken() ?? null,
    reauthenticate: () => (boundAuth ? boundAuth.reauthenticate() : Promise.resolve(false)),
  },
})

/** Fresh idempotency_key for POST /orders, top-ups and other replay-safe creates (§10). */
export function newIdempotencyKey(): string {
  return uuidv4()
}
