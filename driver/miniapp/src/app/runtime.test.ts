// Realtime hints keep the open order list current without polling: a card
// leaves every list as soon as its order is taken (by anyone, also through
// the bot), cancelled or expired, and the first connection re-reads state.

import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { RealtimeClient, RealtimeEvent, RealtimeStatus } from '../services/realtime'
import { useOrdersStore } from '../stores/orders'
import { useSessionStore } from '../stores/session'
import { installDriverRuntime } from './runtime'

vi.mock('../api/orders', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/orders')>()
  return { ...real, getActive: vi.fn().mockResolvedValue(null), listAvailable: vi.fn() }
})

class FakeRealtime {
  status: RealtimeStatus = 'connecting'
  private handlers = new Map<string, Array<(e: RealtimeEvent) => void>>()
  private statusHandlers: Array<(s: RealtimeStatus) => void> = []
  private resyncHandlers: Array<() => void> = []
  on(type: string, cb: (e: RealtimeEvent) => void): () => void {
    this.handlers.set(type, [...(this.handlers.get(type) ?? []), cb])
    return () => undefined
  }
  onStatus(cb: (s: RealtimeStatus) => void): () => void {
    this.statusHandlers.push(cb)
    return () => undefined
  }
  onResync(cb: () => void): () => void {
    this.resyncHandlers.push(cb)
    return () => undefined
  }
  emit(type: string, data: Record<string, unknown>): void {
    for (const cb of this.handlers.get(type) ?? []) cb({ type, id: 'e1', ts: new Date().toISOString(), data } as RealtimeEvent)
  }
  setStatus(s: RealtimeStatus): void {
    this.status = s
    for (const cb of this.statusHandlers) cb(s)
  }
}

describe('driver runtime', () => {
  let off: () => void = () => undefined

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
  })
  afterEach(() => {
    off()
    vi.useRealTimers()
  })

  function install(rt: FakeRealtime) {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
    off = installDriverRuntime(rt as unknown as RealtimeClient, router)
  }

  it('order.unavailable and order.taken drop the card at once', () => {
    const rt = new FakeRealtime()
    install(rt)
    const orders = useOrdersStore()
    const remove = vi.spyOn(orders, 'remove')
    rt.emit('order.unavailable', { order_id: 'o-1', status: 'accepted' })
    rt.emit('order.taken', { order_id: 'o-2', status: 'accepted' })
    expect(remove).toHaveBeenNthCalledWith(1, 'o-1')
    expect(remove).toHaveBeenNthCalledWith(2, 'o-2')
  })

  it('order.available refreshes the list within a few hundred milliseconds, once per burst', () => {
    const rt = new FakeRealtime()
    install(rt)
    const session = useSessionStore()
    vi.spyOn(session, 'isApproved', 'get').mockReturnValue(true)
    const orders = useOrdersStore()
    const refresh = vi.spyOn(orders, 'refresh').mockResolvedValue()
    rt.emit('order.available', { order_id: 'o-1' })
    rt.emit('order.available', { order_id: 'o-2' })
    vi.advanceTimersByTime(300)
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('the first connection re-reads the lists', () => {
    const rt = new FakeRealtime()
    install(rt)
    const session = useSessionStore()
    const refreshMe = vi.spyOn(session, 'refreshMe').mockResolvedValue()
    rt.setStatus('open')
    vi.advanceTimersByTime(300)
    expect(refreshMe).toHaveBeenCalledTimes(1)
    rt.setStatus('reconnecting')
    rt.setStatus('open')
    vi.advanceTimersByTime(300)
    // Later reconnects resync through onResync, not here.
    expect(refreshMe).toHaveBeenCalledTimes(1)
  })
})
