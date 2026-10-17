// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Mini App authentication (ARCHITECTURE §4.1): the raw, signed initData goes
// to POST /auth/telegram/{passenger|driver}; the API answers with a short-lived
// app token. The token lives in memory and in sessionStorage (survives a
// WebView reload, not a new launch). The stored token is bound to the launch
// (the initData `hash`): where the storage outlives a launch (Telegram Web
// keeps the iframe origin's sessionStorage in the browser tab, e.g. after an
// account switch) another launch never reuses it. initDataUnsafe is never
// sent or trusted.

import { getRawInitData } from '../telegram'
import { ApiError, bindAuth, createHttpClient } from './http'

export type AppKind = 'passenger' | 'driver'

export interface AuthSession {
  token: string
  /** RFC 3339 expiry from the API. */
  expires_at: string
}

interface StoredSession extends AuthSession {
  /** initData `hash` of the launch that obtained the token. */
  launch: string
}

/** Refresh slightly early so a request never leaves with an expiring token. */
const EXPIRY_MARGIN_MS = 60_000

let appKind: AppKind | null = null
let session: AuthSession | null = null
let inFlight: Promise<AuthSession> | null = null

// Authentication itself must not carry a token or trigger the 401 retry.
const publicClient = createHttpClient({ auth: null })

function storageKey(app: AppKind): string {
  return `jt.session.${app}`
}

function expiresAtMs(s: AuthSession): number {
  const t = Date.parse(s.expires_at)
  return Number.isFinite(t) ? t : 0
}

function isFresh(s: AuthSession | null): s is AuthSession {
  return s !== null && expiresAtMs(s) - Date.now() > EXPIRY_MARGIN_MS
}

function isSession(v: unknown): v is AuthSession {
  if (!v || typeof v !== 'object') return false
  const o = v as Record<string, unknown>
  return typeof o.token === 'string' && o.token.length > 0 && typeof o.expires_at === 'string'
}

/** Identifies the current launch: the `hash` field of the signed initData. */
function launchKey(): string | null {
  const raw = getRawInitData()
  if (!raw) return null
  try {
    return new URLSearchParams(raw).get('hash') || null
  } catch {
    return null
  }
}

function readStored(app: AppKind): AuthSession | null {
  try {
    const raw = sessionStorage.getItem(storageKey(app))
    const parsed: unknown = raw ? JSON.parse(raw) : null
    if (!isSession(parsed)) return null
    const launch = (parsed as Partial<StoredSession>).launch
    const current = launchKey()
    if (!current || launch !== current) return null
    return { token: parsed.token, expires_at: parsed.expires_at }
  } catch {
    return null
  }
}

function writeStored(app: AppKind, s: AuthSession | null): void {
  try {
    const launch = launchKey()
    if (s && launch) sessionStorage.setItem(storageKey(app), JSON.stringify({ ...s, launch } satisfies StoredSession))
    else sessionStorage.removeItem(storageKey(app))
  } catch {
    // Storage can be disabled in some WebViews; the in-memory token still works.
  }
}

/**
 * Returns a valid session, authenticating with Telegram initData when needed.
 * Rejects with ApiError: code 'not_in_telegram' when no initData is available,
 * otherwise the API's error (e.g. 'unauthorized' for a stale initData).
 */
export function authenticate(app: AppKind, opts: { force?: boolean } = {}): Promise<AuthSession> {
  if (appKind !== app) session = null
  appKind = app
  if (!opts.force) {
    const cached = isFresh(session) ? session : readStored(app)
    if (isFresh(cached)) {
      session = cached
      return Promise.resolve(cached)
    }
  }
  inFlight ??= (async () => {
    const initData = getRawInitData()
    if (!initData) throw new ApiError(401, 'not_in_telegram', 'Telegram initData is unavailable')
    const res = await publicClient.post<unknown>(`/auth/telegram/${app}`, { init_data: initData })
    if (!isSession(res)) throw new ApiError(500, 'bad_response', 'auth response has no token')
    session = { token: res.token, expires_at: res.expires_at }
    writeStored(app, session)
    return session
  })().finally(() => {
    inFlight = null
  })
  return inFlight
}

/** Current bearer token, or null when unauthenticated/expired. */
export function getToken(): string | null {
  return session && expiresAtMs(session) > Date.now() ? session.token : null
}

/** Drops the token (memory and sessionStorage). */
export function clearSession(): void {
  session = null
  if (appKind) writeStored(appKind, null)
}

/** Forces a new initData authentication; resolves false when it fails. */
export async function reauthenticate(): Promise<boolean> {
  if (!appKind) return false
  const app = appKind
  clearSession()
  try {
    await authenticate(app, { force: true })
    return true
  } catch {
    return false
  }
}

bindAuth({ getToken, reauthenticate })
