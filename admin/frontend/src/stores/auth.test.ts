import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, setUnauthorizedHandler } from '../services/api'
import { getCsrfToken, setCsrfToken } from '../services/csrf'
import { normalizeAdmin, useAuthStore } from './auth'

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit]

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const ADMIN = { id: 'a1', email: 'ops@jantaxi.kz', full_name: 'Айгерім Сейітқали', role: 'operator' }

function headersOf(call: FetchArgs | undefined): Record<string, string> {
  return (call?.[1]?.headers ?? {}) as Record<string, string>
}

describe('admin auth store', () => {
  let fetchMock: ReturnType<typeof vi.fn<(...args: FetchArgs) => Promise<Response>>>

  beforeEach(() => {
    setActivePinia(createPinia())
    sessionStorage.clear()
    setCsrfToken(null)
    setUnauthorizedHandler(null)
    fetchMock = vi.fn<(...args: FetchArgs) => Promise<Response>>(async (input) => {
      const url = String(input)
      if (url === '/api/v1/admin/auth/login') return json(200, { admin: ADMIN, csrf_token: 'csrf-123' })
      if (url === '/api/v1/admin/auth/me') return json(200, { admin: ADMIN, csrf_token: 'csrf-me' })
      return json(200, { ok: true })
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('keeps the CSRF token from login and sends it only on non-GET requests', async () => {
    const auth = useAuthStore()
    await auth.login('  ops@jantaxi.kz ', 'secret')

    expect(auth.isAuthenticated).toBe(true)
    expect(getCsrfToken()).toBe('csrf-123')
    expect(sessionStorage.getItem('jt.admin.csrf')).toBe('csrf-123')

    const loginCall = fetchMock.mock.calls[0]!
    expect(loginCall[1]?.credentials).toBe('include')
    expect(JSON.parse(String(loginCall[1]?.body))).toEqual({ email: 'ops@jantaxi.kz', password: 'secret' })

    await api.post('/admin/orders/o1/cancel', { reason: 'duplicate' })
    const post = fetchMock.mock.calls.at(-1)
    expect(String(post?.[0])).toBe('/api/v1/admin/orders/o1/cancel')
    expect(headersOf(post)['X-CSRF-Token']).toBe('csrf-123')
    expect(post?.[1]?.credentials).toBe('include')

    await api.patch('/admin/settings', { a: 1 })
    expect(headersOf(fetchMock.mock.calls.at(-1))['X-CSRF-Token']).toBe('csrf-123')

    await api.get('/admin/orders', { query: { page: 1 } })
    const get = fetchMock.mock.calls.at(-1)
    expect(headersOf(get)['X-CSRF-Token']).toBeUndefined()
    expect(get?.[1]?.credentials).toBe('include')
  })

  it('sends the token on logout and clears it afterwards', async () => {
    const auth = useAuthStore()
    await auth.login('ops@jantaxi.kz', 'secret')
    await auth.logout()
    const logout = fetchMock.mock.calls.at(-1)
    expect(String(logout?.[0])).toBe('/api/v1/admin/auth/logout')
    expect(headersOf(logout)['X-CSRF-Token']).toBe('csrf-123')
    expect(auth.isAuthenticated).toBe(false)
    expect(getCsrfToken()).toBeNull()
    expect(sessionStorage.getItem('jt.admin.csrf')).toBeNull()
  })

  it('restores the session and CSRF token from /admin/auth/me', async () => {
    const auth = useAuthStore()
    await auth.bootstrap()
    expect(auth.loaded).toBe(true)
    expect(auth.admin?.email).toBe('ops@jantaxi.kz')
    expect(getCsrfToken()).toBe('csrf-me')
  })

  it('stays signed out when /admin/auth/me returns 401', async () => {
    fetchMock.mockImplementation(async () => json(401, { error: { code: 'unauthorized', message: 'no session' } }))
    const auth = useAuthStore()
    await auth.bootstrap()
    expect(auth.loaded).toBe(true)
    expect(auth.isAuthenticated).toBe(false)
  })

  it('reports a failed login as ApiError and stays signed out', async () => {
    fetchMock.mockImplementation(async () => json(401, { error: { code: 'invalid_credentials', message: 'bad' } }))
    const auth = useAuthStore()
    await expect(auth.login('ops@jantaxi.kz', 'wrong')).rejects.toMatchObject({ status: 401, code: 'invalid_credentials' })
    expect(auth.isAuthenticated).toBe(false)
    expect(getCsrfToken()).toBeNull()
  })

  it('checks permissions from the role when the API omits them', async () => {
    const auth = useAuthStore()
    await auth.login('ops@jantaxi.kz', 'secret')
    expect(auth.can('orders.manage')).toBe(true)
    expect(auth.can('finance.manage')).toBe(false)
    expect(auth.can('config.manage')).toBe(false)
    expect(auth.can(undefined)).toBe(true)
  })

  it('refreshes a rejected CSRF token from /admin/auth/me and retries once', async () => {
    const auth = useAuthStore()
    await auth.login('ops@jantaxi.kz', 'secret')
    let posts = 0
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input)
      if (url === '/api/v1/admin/auth/me') return json(200, { admin: ADMIN, csrf_token: 'csrf-fresh' })
      posts++
      const token = (init?.headers as Record<string, string>)['X-CSRF-Token']
      return token === 'csrf-fresh' ? json(200, { ok: true }) : json(403, { error: { code: 'csrf_failed', message: 'bad token' } })
    })
    await expect(api.post('/admin/orders/o1/cancel', { reason: 'x' })).resolves.toEqual({ ok: true })
    expect(posts).toBe(2)
    expect(getCsrfToken()).toBe('csrf-fresh')
  })

  it('does not retry GET requests or other 403 errors', async () => {
    fetchMock.mockImplementation(async () => json(403, { error: { code: 'forbidden', message: 'no' } }))
    await expect(api.post('/admin/orders/o1/cancel', { reason: 'x' })).rejects.toMatchObject({ status: 403, code: 'forbidden' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('invokes the unauthorized handler on a 401 from any request', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    fetchMock.mockImplementation(async () => json(401, { error: { code: 'unauthorized', message: 'expired' } }))
    await expect(api.get('/admin/orders')).rejects.toMatchObject({ status: 401 })
    expect(handler).toHaveBeenCalledTimes(1)
  })
})

describe('normalizeAdmin', () => {
  it('accepts the bare profile and filters unknown permissions', () => {
    const { admin } = normalizeAdmin({ ...ADMIN, role: 'auditor', permissions: ['audit.view', 'root.everything'] })
    expect(admin?.permissions).toEqual(['audit.view'])
  })

  it('rejects unknown roles', () => {
    expect(normalizeAdmin({ ...ADMIN, role: 'owner' }).admin).toBeNull()
  })
})
