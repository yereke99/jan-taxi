import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

let initData: string | undefined
vi.mock('../telegram', () => ({ getRawInitData: () => initData }))

function launch(user: number, hash: string): string {
  return new URLSearchParams({ user: JSON.stringify({ id: user }), auth_date: '1', hash }).toString()
}

function tokenResponse(token: string): Response {
  return new Response(JSON.stringify({ token, expires_at: new Date(Date.now() + 3_600_000).toISOString() }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('authenticate', () => {
  beforeEach(() => {
    vi.resetModules()
    sessionStorage.clear()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('reuses the stored token after a reload of the same launch', async () => {
    initData = launch(1, 'aaa')
    const fetchMock = vi.fn(async () => tokenResponse('t1'))
    vi.stubGlobal('fetch', fetchMock)
    const first = await import('./auth')
    expect((await first.authenticate('passenger')).token).toBe('t1')

    vi.resetModules()
    const reloaded = await import('./auth')
    expect((await reloaded.authenticate('passenger')).token).toBe('t1')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('never reuses a token stored by another launch', async () => {
    initData = launch(1, 'aaa')
    vi.stubGlobal('fetch', vi.fn(async () => tokenResponse('t1')))
    expect((await (await import('./auth')).authenticate('passenger')).token).toBe('t1')

    // Same storage, new launch (e.g. another account in Telegram Web).
    vi.resetModules()
    initData = launch(2, 'bbb')
    const fetchMock = vi.fn(async () => tokenResponse('t2'))
    vi.stubGlobal('fetch', fetchMock)
    expect((await (await import('./auth')).authenticate('passenger')).token).toBe('t2')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
