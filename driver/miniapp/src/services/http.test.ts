// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it, vi } from 'vitest'
import { ApiError, buildUrl, createHttpClient, isAbortError, toApiError, type AuthProvider } from './http'

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

function authStub(initial: string | null, next: string | null = 'fresh-token'): AuthProvider & { reauthenticate: ReturnType<typeof vi.fn> } {
  let token = initial
  return {
    getToken: () => token,
    reauthenticate: vi.fn(async () => {
      token = next
      return next !== null
    }),
  }
}

describe('toApiError', () => {
  it('parses the error envelope', async () => {
    const err = await toApiError(
      json(422, {
        error: { code: 'validation_failed', message: 'validation failed', fields: { comment: 'too_long' }, details: { max: 120 } },
      }),
    )
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(422)
    expect(err.code).toBe('validation_failed')
    expect(err.message).toBe('validation failed')
    expect(err.fields).toEqual({ comment: 'too_long' })
    expect(err.details).toEqual({ max: 120 })
  })

  it('falls back to a status-derived code for non-JSON bodies', async () => {
    const err = await toApiError(new Response('<html>Bad gateway</html>', { status: 502 }))
    expect(err.code).toBe('internal')
    expect(err.status).toBe(502)
    expect(err.fields).toEqual({})
  })

  it('records Retry-After on 429', async () => {
    const err = await toApiError(json(429, { error: { code: 'too_many_requests', message: 'slow down' } }, { 'Retry-After': '7' }))
    expect(err.code).toBe('too_many_requests')
    expect(err.details.retry_after_s).toBe(7)
  })

  it('ignores malformed field values', async () => {
    const err = await toApiError(json(400, { error: { code: 'bad_request', fields: { a: 1, b: 'x' } } }))
    expect(err.fields).toEqual({ b: 'x' })
    expect(err.message).toBe('HTTP 400')
  })
})

describe('buildUrl', () => {
  it('joins base, path and query, skipping empty values', () => {
    expect(buildUrl('/api/v1', '/orders', { scope: 'active', limit: 20, before: null, x: undefined })).toBe(
      '/api/v1/orders?scope=active&limit=20',
    )
    expect(buildUrl('/api/v1/', 'me')).toBe('/api/v1/me')
    expect(buildUrl('/api/v1', '/a', { id: ['1', '2'] })).toBe('/api/v1/a?id=1&id=2')
  })
})

describe('createHttpClient', () => {
  it('sends JSON with the bearer token and parses the response', async () => {
    const fetchImpl = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => json(200, { ok: true }))
    const client = createHttpClient({ fetchImpl, auth: authStub('t1') })
    const res = await client.post<{ ok: boolean }>('/orders', { fare: 1500 })
    expect(res).toEqual({ ok: true })
    const [url, init] = fetchImpl.mock.calls[0]!
    expect(url).toBe('/api/v1/orders')
    const headers = init!.headers as Record<string, string>
    expect(headers.Authorization).toBe('Bearer t1')
    expect(headers['Content-Type']).toBe('application/json')
    expect(init!.body).toBe('{"fare":1500}')
  })

  it('throws ApiError with the envelope on failure', async () => {
    const fetchImpl = vi.fn(async () => json(409, { error: { code: 'order_already_taken', message: 'taken' } }))
    const client = createHttpClient({ fetchImpl, auth: authStub('t1') })
    await expect(client.post('/driver/orders/x/accept')).rejects.toMatchObject({ status: 409, code: 'order_already_taken' })
  })

  it('re-authenticates once on 401 and retries the request with the new token', async () => {
    const auth = authStub('stale')
    const fetchImpl = vi
      .fn<(url: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()
      .mockResolvedValueOnce(json(401, { error: { code: 'unauthorized', message: 'expired' } }))
      .mockResolvedValueOnce(json(200, { id: 'me' }))
    const client = createHttpClient({ fetchImpl, auth })
    await expect(client.get('/me')).resolves.toEqual({ id: 'me' })
    expect(auth.reauthenticate).toHaveBeenCalledTimes(1)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
    const retryHeaders = fetchImpl.mock.calls[1]![1]!.headers as Record<string, string>
    expect(retryHeaders.Authorization).toBe('Bearer fresh-token')
  })

  it('does not loop when the retry is also 401', async () => {
    const auth = authStub('stale')
    const onUnauthorized = vi.fn()
    const fetchImpl = vi.fn(async () => json(401, { error: { code: 'unauthorized', message: 'no' } }))
    const client = createHttpClient({ fetchImpl, auth, onUnauthorized })
    await expect(client.get('/me')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(auth.reauthenticate).toHaveBeenCalledTimes(1)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('gives up without retrying when re-authentication fails', async () => {
    const auth = authStub('stale', null)
    const fetchImpl = vi.fn(async () => json(401, { error: { code: 'unauthorized', message: 'no' } }))
    const client = createHttpClient({ fetchImpl, auth })
    await expect(client.get('/me')).rejects.toBeInstanceOf(ApiError)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
  })

  it('shares a single re-authentication between concurrent 401s', async () => {
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    let token = 'stale'
    const auth: AuthProvider & { reauthenticate: ReturnType<typeof vi.fn> } = {
      getToken: () => token,
      reauthenticate: vi.fn(async () => {
        await gate
        token = 'fresh'
        return true
      }),
    }
    const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      const h = init!.headers as Record<string, string>
      return h.Authorization === 'Bearer fresh' ? json(200, { ok: 1 }) : json(401, { error: { code: 'unauthorized' } })
    })
    const client = createHttpClient({ fetchImpl, auth })
    const a = client.get('/a')
    const b = client.get('/b')
    await vi.waitFor(() => expect(auth.reauthenticate).toHaveBeenCalledTimes(1))
    release()
    await expect(Promise.all([a, b])).resolves.toEqual([{ ok: 1 }, { ok: 1 }])
    expect(auth.reauthenticate).toHaveBeenCalledTimes(1)
  })

  it('maps network failures to ApiError(0, network_error)', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    })
    const client = createHttpClient({ fetchImpl, auth: null })
    await expect(client.get('/x')).rejects.toMatchObject({ status: 0, code: 'network_error', isNetworkError: true })
  })

  it('propagates caller aborts as AbortError', async () => {
    const fetchImpl = vi.fn(
      (_url: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init!.signal!.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
        }),
    )
    const client = createHttpClient({ fetchImpl, auth: null })
    const ctrl = new AbortController()
    const p = client.get('/slow', { signal: ctrl.signal })
    ctrl.abort()
    const err = await p.catch((e: unknown) => e)
    expect(isAbortError(err)).toBe(true)
  })

  it('times out with ApiError(0, timeout)', async () => {
    vi.useFakeTimers()
    try {
      const fetchImpl = vi.fn(
        (_url: RequestInfo | URL, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            init!.signal!.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
          }),
      )
      const client = createHttpClient({ fetchImpl, auth: null })
      const p = client.get('/slow', { timeoutMs: 1000 }).catch((e: unknown) => e)
      await vi.advanceTimersByTimeAsync(1001)
      await expect(p).resolves.toMatchObject({ status: 0, code: 'timeout' })
    } finally {
      vi.useRealTimers()
    }
  })

  it('resolves null for 204 via getOptional', async () => {
    const fetchImpl = vi.fn(async () => new Response(null, { status: 204 }))
    const client = createHttpClient({ fetchImpl, auth: null })
    await expect(client.getOptional('/driver/orders/active')).resolves.toBeNull()
  })
})
