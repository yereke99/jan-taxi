// CSRF token for the admin session (ARCHITECTURE §4.2). The login response
// returns it; it is kept in memory and in sessionStorage (survives a reload of
// the tab, not a new tab) and sent as X-CSRF-Token on every non-GET request.

const STORAGE_KEY = 'jt.admin.csrf'

let token: string | null = null

function read(): string | null {
  try {
    return sessionStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

export function getCsrfToken(): string | null {
  token ??= read()
  return token
}

export function setCsrfToken(value: string | null): void {
  token = value && value.length > 0 ? value : null
  try {
    if (token) sessionStorage.setItem(STORAGE_KEY, token)
    else sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // Storage disabled: the in-memory token still protects this page's requests.
  }
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

/** Headers to add for a request with this method. */
export function csrfHeaders(method: string): Record<string, string> {
  const t = getCsrfToken()
  return t && !SAFE_METHODS.has(method.toUpperCase()) ? { 'X-CSRF-Token': t } : {}
}
