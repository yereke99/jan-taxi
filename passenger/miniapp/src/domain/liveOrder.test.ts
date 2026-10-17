import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Order } from '../api/types'
import type { RealtimeEvent, RealtimeStatus } from '../services/realtime'
import type { OrderStatus } from '../utils/status'
import { LiveOrderSync, POLL_WHILE_CONNECTED_MS, POLL_WHILE_DISCONNECTED_MS, statusOfEvent, type RealtimeSource } from './liveOrder'

const ID = '7d9f2c1e-1111-4a2b-9c3d-123456789abc'
const DRIVER = 'dddddddd-0000-4000-8000-000000000001'

function order(status: OrderStatus, version: number, extra: Partial<Order> = {}): Order {
  return {
    id: ID,
    status,
    version,
    driver:
      status === 'searching'
        ? null
        : { id: DRIVER, code: 'EMB001', first_name: 'Ерлан', last_name: 'Б.', phone: null, rating: 4.9, rating_count: 10, completed_trips: 20, photo_url: null },
    driver_location: null,
    stops: [],
    ...extra,
  } as Order
}

/** Minimal realtime client double. */
class FakeRealtime implements RealtimeSource {
  status: RealtimeStatus = 'open'
  private handlers = new Map<string, Set<(e: RealtimeEvent) => void>>()
  private resync = new Set<() => void>()
  private statusCbs = new Set<(s: RealtimeStatus) => void>()

  on(type: string, handler: (e: RealtimeEvent) => void): () => void {
    const set = this.handlers.get(type) ?? new Set()
    set.add(handler)
    this.handlers.set(type, set)
    return () => set.delete(handler)
  }
  onResync(cb: () => void): () => void {
    this.resync.add(cb)
    return () => this.resync.delete(cb)
  }
  onStatus(cb: (s: RealtimeStatus) => void): () => void {
    this.statusCbs.add(cb)
    return () => this.statusCbs.delete(cb)
  }
  emit(type: string, data: Record<string, unknown>): void {
    for (const h of this.handlers.get(type) ?? []) h({ type, data })
  }
  fireResync(): void {
    for (const cb of this.resync) cb()
  }
  setStatus(s: RealtimeStatus): void {
    this.status = s
    for (const cb of this.statusCbs) cb(s)
  }
  listenerCount(): number {
    let n = this.resync.size + this.statusCbs.size
    for (const s of this.handlers.values()) n += s.size
    return n
  }
}

function setup(responses: Order[]) {
  const rt = new FakeRealtime()
  const seen: OrderStatus[] = []
  const hints: OrderStatus[] = []
  const fetchOrder = vi.fn(async () => {
    const next = responses.length > 1 ? responses.shift()! : responses[0]!
    return next
  })
  const sync = new LiveOrderSync({
    orderId: ID.toUpperCase(),
    fetchOrder,
    realtime: rt,
    onOrder: (o) => seen.push(o.status),
    onHint: (s) => hints.push(s),
  })
  return { rt, sync, seen, hints, fetchOrder }
}

describe('LiveOrderSync (realtime-driven status transitions)', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('maps event types to the statuses they announce', () => {
    expect(statusOfEvent('order.accepted')).toBe('accepted')
    expect(statusOfEvent('order.driver_arriving')).toBe('driver_arriving')
    expect(statusOfEvent('order.stop_completed')).toBe('in_progress')
    expect(statusOfEvent('order.completed')).toBe('completed')
    expect(statusOfEvent('wallet.updated')).toBeNull()
  })

  it('follows searching → accepted → driver_arriving → in_progress → completed via events + REST', async () => {
    const { rt, sync, seen, hints, fetchOrder } = setup([order('searching', 1)])
    sync.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(seen).toEqual(['searching'])

    const steps: Array<[string, Order]> = [
      ['order.accepted', order('accepted', 2)],
      ['order.driver_arriving', order('driver_arriving', 3)],
      ['order.started', order('in_progress', 4)],
      ['order.completed', order('completed', 5)],
    ]
    for (const [type, next] of steps) {
      fetchOrder.mockResolvedValueOnce(next)
      rt.emit(type, { order_id: ID, status: next.status })
      await vi.advanceTimersByTimeAsync(0)
    }
    expect(seen).toEqual(['searching', 'accepted', 'driver_arriving', 'in_progress', 'completed'])
    expect(hints).toEqual(['accepted', 'driver_arriving', 'in_progress', 'completed'])
    sync.stop()
  })

  it('ignores events of other orders', async () => {
    const { rt, sync, fetchOrder } = setup([order('searching', 1)])
    sync.start()
    await vi.advanceTimersByTimeAsync(0)
    rt.emit('order.accepted', { order_id: 'eeeeeeee-0000-4000-8000-000000000009' })
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchOrder).toHaveBeenCalledTimes(1)
    sync.stop()
  })

  it('never goes back to an older version', async () => {
    const { rt, sync, seen, fetchOrder } = setup([order('searching', 1)])
    sync.start()
    await vi.advanceTimersByTimeAsync(0)
    fetchOrder.mockResolvedValueOnce(order('accepted', 3))
    rt.emit('order.accepted', { order_id: ID })
    await vi.advanceTimersByTimeAsync(0)
    // A lagging replica answers with the previous version.
    fetchOrder.mockResolvedValueOnce(order('searching', 1))
    rt.fireResync()
    await vi.advanceTimersByTimeAsync(0)
    expect(seen).toEqual(['searching', 'accepted'])
    expect(sync.current?.status).toBe('accepted')
    sync.stop()
  })

  it('applies driver.location_updated for the assigned driver without a re-fetch', async () => {
    const { rt, sync, fetchOrder } = setup([order('accepted', 2)])
    sync.start()
    await vi.advanceTimersByTimeAsync(0)
    rt.emit('driver.location_updated', { order_id: ID, driver_id: DRIVER, lat: 48.83, lon: 58.15, heading: 90, recorded_at: '2026-10-16T10:00:00Z' })
    expect(sync.current?.driver_location).toMatchObject({ lat: 48.83, lon: 58.15, heading: 90 })
    // Another driver's position is ignored.
    rt.emit('driver.location_updated', { order_id: ID, driver_id: 'x', lat: 1, lon: 1, recorded_at: '2026-10-16T10:00:05Z' })
    expect(sync.current?.driver_location?.lat).toBe(48.83)
    // An older fix is ignored.
    rt.emit('driver.location_updated', { order_id: ID, driver_id: DRIVER, lat: 48.8, lon: 58.1, recorded_at: '2026-10-16T09:59:00Z' })
    expect(sync.current?.driver_location?.lat).toBe(48.83)
    expect(fetchOrder).toHaveBeenCalledTimes(1)
    sync.stop()
  })

  it('polls every 10 s while the socket is down, slower while connected, and stops when finished', async () => {
    const { rt, sync, fetchOrder } = setup([order('searching', 1)])
    rt.status = 'reconnecting'
    sync.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchOrder).toHaveBeenCalledTimes(1)
    // +1: a zero-delay timer created inside a fake-timer tick runs one tick later.
    await vi.advanceTimersByTimeAsync(POLL_WHILE_DISCONNECTED_MS + 1)
    expect(fetchOrder).toHaveBeenCalledTimes(2)

    rt.setStatus('open')
    await vi.advanceTimersByTimeAsync(POLL_WHILE_DISCONNECTED_MS + 1)
    expect(fetchOrder).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(POLL_WHILE_CONNECTED_MS - POLL_WHILE_DISCONNECTED_MS)
    expect(fetchOrder).toHaveBeenCalledTimes(3)

    fetchOrder.mockResolvedValue(order('cancelled', 2))
    rt.emit('order.cancelled', { order_id: ID })
    await vi.advanceTimersByTimeAsync(0)
    const calls = fetchOrder.mock.calls.length
    await vi.advanceTimersByTimeAsync(POLL_WHILE_CONNECTED_MS * 3)
    expect(fetchOrder).toHaveBeenCalledTimes(calls)
    sync.stop()
  })

  it('re-syncs on reconnect and unsubscribes on stop', async () => {
    const { rt, sync, fetchOrder } = setup([order('searching', 1)])
    sync.start()
    await vi.advanceTimersByTimeAsync(0)
    rt.fireResync()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchOrder).toHaveBeenCalledTimes(2)
    sync.stop()
    expect(rt.listenerCount()).toBe(0)
    rt.fireResync()
    rt.emit('order.accepted', { order_id: ID })
    await vi.advanceTimersByTimeAsync(POLL_WHILE_DISCONNECTED_MS * 2)
    expect(fetchOrder).toHaveBeenCalledTimes(2)
  })
})
