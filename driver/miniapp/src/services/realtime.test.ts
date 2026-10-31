// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { backoffDelay, CLOSE_POLICY_VIOLATION, createRealtimeClient, type RealtimeOptions, type SocketLike } from './realtime'

class FakeSocket implements SocketLike {
  static instances: FakeSocket[] = []
  readyState = 0
  sent: string[] = []
  closed: { code?: number; reason?: string } | null = null
  onopen: ((ev: Event) => void) | null = null
  onmessage: ((ev: MessageEvent) => void) | null = null
  onclose: ((ev: CloseEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null

  constructor(readonly url: string) {
    FakeSocket.instances.push(this)
  }

  send(data: string): void {
    this.sent.push(data)
  }

  close(code?: number, reason?: string): void {
    if (this.readyState === 3) return
    this.closed = { code, reason }
    this.serverClose(code ?? 1000)
  }

  // --- test helpers ---
  serverOpen(): void {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }

  serverSend(frame: unknown): void {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }

  serverClose(code = 1006, reason = ''): void {
    this.readyState = 3
    this.onclose?.(new CloseEvent('close', { code, reason }))
  }

  frames(): Array<Record<string, unknown>> {
    return this.sent.map((s) => JSON.parse(s) as Record<string, unknown>)
  }
}

function latest(): FakeSocket {
  const s = FakeSocket.instances[FakeSocket.instances.length - 1]
  if (!s) throw new Error('no socket created')
  return s
}

function client(extra: Partial<RealtimeOptions> = {}) {
  return createRealtimeClient({
    url: 'ws://test/api/v1/ws',
    getToken: () => 'app-token',
    createSocket: (url) => new FakeSocket(url),
    random: () => 0.5,
    watchLifecycle: false,
    ...extra,
  })
}

/** Lets pending promise continuations (async onopen) run. */
async function flush(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0)
}

describe('backoffDelay', () => {
  it('grows exponentially from 1s and caps at 30s with jitter', () => {
    const half = (): number => 0.5
    expect(backoffDelay(0, half)).toBe(750) // 1000 * (0.5 + 0.5 * 0.5)
    expect(backoffDelay(1, half)).toBe(1500)
    expect(backoffDelay(4, half)).toBe(12_000)
    expect(backoffDelay(5, half)).toBe(22_500) // capped base 30s
    expect(backoffDelay(20, half)).toBe(22_500)
    expect(backoffDelay(0, () => 0)).toBe(500)
    expect(backoffDelay(10, () => 1)).toBe(30_000)
  })
})

describe('RealtimeClient', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeSocket.instances = []
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('sends the auth frame first and becomes open on ready', async () => {
    const rt = client()
    rt.start()
    const s = latest()
    expect(s.url).toBe('ws://test/api/v1/ws')
    expect(rt.status).toBe('connecting')
    s.serverOpen()
    await flush()
    expect(s.frames()[0]).toEqual({ type: 'auth', token: 'app-token' })
    expect(rt.status).toBe('authenticating')
    s.serverSend({ type: 'ready' })
    expect(rt.status).toBe('open')
    rt.stop()
  })

  it('dispatches events by type and to wildcard subscribers', async () => {
    const rt = client()
    const accepted = vi.fn()
    const any = vi.fn()
    rt.on('order.accepted', accepted)
    rt.on('*', any)
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready' })
    latest().serverSend({ type: 'order.accepted', id: 'e1', ts: '2026-10-16T10:00:00Z', data: { order_id: 'o1' } })
    latest().serverSend({ type: 'wallet.updated', data: {} })
    expect(accepted).toHaveBeenCalledTimes(1)
    expect(accepted.mock.calls[0]![0]).toMatchObject({ type: 'order.accepted', id: 'e1', data: { order_id: 'o1' } })
    expect(any).toHaveBeenCalledTimes(2)
    rt.stop()
  })

  it('answers ping with pong', async () => {
    const rt = client()
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready' })
    latest().serverSend({ type: 'ping' })
    expect(latest().frames().at(-1)).toEqual({ type: 'pong' })
    rt.stop()
  })

  it('reconnects with exponential backoff and resyncs after reconnecting', async () => {
    const rt = client()
    const resync = vi.fn()
    rt.onResync(resync)
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready' })
    expect(resync).not.toHaveBeenCalled() // first connection is not a resync

    latest().serverClose(1006)
    expect(rt.status).toBe('reconnecting')
    expect(FakeSocket.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(749)
    expect(FakeSocket.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeSocket.instances).toHaveLength(2)

    // Second consecutive failure waits longer (1500ms).
    latest().serverClose(1006)
    await vi.advanceTimersByTimeAsync(1499)
    expect(FakeSocket.instances).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeSocket.instances).toHaveLength(3)

    latest().serverOpen()
    await flush()
    expect(latest().frames()[0]).toEqual({ type: 'auth', token: 'app-token' })
    latest().serverSend({ type: 'ready' })
    expect(resync).toHaveBeenCalledTimes(1)

    // A successful connection resets the backoff.
    latest().serverClose(1006)
    await vi.advanceTimersByTimeAsync(750)
    expect(FakeSocket.instances).toHaveLength(4)
    rt.stop()
  })

  it('caps the backoff at 30 seconds', async () => {
    const rt = client({ random: () => 1 })
    rt.start()
    for (let i = 0; i < 8; i++) {
      latest().serverClose(1006)
      await vi.advanceTimersByTimeAsync(30_000)
    }
    const before = FakeSocket.instances.length
    latest().serverClose(1006)
    await vi.advanceTimersByTimeAsync(29_999)
    expect(FakeSocket.instances).toHaveLength(before)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeSocket.instances).toHaveLength(before + 1)
    rt.stop()
  })

  it.each(['token_expired', 'unauthorized'])('signs in again after a 1008 %s close before reconnecting', async (reason) => {
    let token = 'old'
    const refreshToken = vi.fn(async () => {
      token = 'new'
      return true
    })
    const rt = client({ getToken: () => token, refreshToken })
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready', scopes: ['user:u1'] })
    latest().serverClose(CLOSE_POLICY_VIOLATION, reason)
    await flush()
    expect(refreshToken).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(750)
    latest().serverOpen()
    await flush()
    expect(latest().frames()[0]).toEqual({ type: 'auth', token: 'new' })
    rt.stop()
  })

  it('reconnects without a token refresh after other policy closes', async () => {
    const refreshToken = vi.fn(async () => true)
    const rt = client({ refreshToken })
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready' })
    latest().serverClose(CLOSE_POLICY_VIOLATION, 'slow_consumer')
    await flush()
    expect(refreshToken).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(750)
    expect(FakeSocket.instances).toHaveLength(2)
    rt.stop()
  })

  it('stops for good when the user is blocked', async () => {
    const rt = client()
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready' })
    latest().serverClose(CLOSE_POLICY_VIOLATION, 'user_blocked')
    expect(rt.status).toBe('stopped')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('keeps the granted scopes and re-syncs on a server resync frame', async () => {
    const rt = client()
    const resync = vi.fn()
    rt.onResync(resync)
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready', scopes: ['user:u1', 'driver:d1'] })
    expect(rt.scopes).toEqual(['user:u1', 'driver:d1'])
    latest().serverSend({ type: 'resync' })
    expect(resync).toHaveBeenCalledTimes(1)
    rt.stop()
  })

  it('closes a connection whose auth is never acknowledged', async () => {
    const rt = client({ readyTimeoutMs: 5000 })
    rt.start()
    latest().serverOpen()
    await flush()
    await vi.advanceTimersByTimeAsync(5000)
    expect(latest().closed?.code).toBe(4000)
    expect(rt.status).toBe('reconnecting')
    rt.stop()
  })

  it('pings periodically and reconnects when the connection goes idle', async () => {
    const rt = client({ heartbeatMs: 1000, idleTimeoutMs: 3500 })
    rt.start()
    const s = latest()
    s.serverOpen()
    await flush()
    s.serverSend({ type: 'ready' })
    await vi.advanceTimersByTimeAsync(1000)
    expect(s.frames().at(-1)).toEqual({ type: 'ping' })
    await vi.advanceTimersByTimeAsync(3000)
    expect(s.closed?.code).toBe(4000)
    rt.stop()
  })

  it('resyncs on foreground when open and reconnects immediately when not', async () => {
    const rt = client()
    const resync = vi.fn()
    rt.onResync(resync)
    rt.start()
    latest().serverOpen()
    await flush()
    latest().serverSend({ type: 'ready' })
    rt.notifyForeground()
    expect(resync).toHaveBeenCalledTimes(1)

    latest().serverClose(1006)
    const count = FakeSocket.instances.length
    rt.notifyForeground()
    expect(FakeSocket.instances).toHaveLength(count + 1) // no backoff wait
    rt.stop()
  })

  it('probes the connection on foreground and replaces one that does not answer', async () => {
    const rt = client({ probeTimeoutMs: 2000 })
    const resync = vi.fn()
    rt.onResync(resync)
    rt.start()
    const s = latest()
    s.serverOpen()
    await flush()
    s.serverSend({ type: 'ready' })
    rt.notifyForeground()
    expect(s.frames().at(-1)).toEqual({ type: 'ping' })
    // A live connection answers: nothing happens.
    s.serverSend({ type: 'pong' })
    await vi.advanceTimersByTimeAsync(2000)
    expect(s.closed).toBeNull()
    expect(rt.status).toBe('open')

    // A connection dropped in the background stays silent: it is replaced.
    rt.notifyForeground()
    await vi.advanceTimersByTimeAsync(2000)
    expect(s.closed?.reason).toBe('probe timeout')
    expect(rt.status).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(1000)
    const next = latest()
    expect(next).not.toBe(s)
    next.serverOpen()
    await flush()
    next.serverSend({ type: 'ready' })
    expect(rt.status).toBe('open')
    expect(resync).toHaveBeenCalledTimes(3) // two foregrounds + the reconnect
    rt.stop()
  })

  it('stops cleanly and does not reconnect', async () => {
    const rt = client()
    rt.start()
    latest().serverOpen()
    await flush()
    rt.stop()
    expect(rt.status).toBe('stopped')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(FakeSocket.instances).toHaveLength(1)
  })
})
