// Keeps one order in sync with the server (ARCHITECTURE §9): realtime events
// are hints that trigger a REST re-fetch; a resync (reconnect, server resync,
// app back in the foreground) re-fetches too; while the socket is not open the
// order is polled every 10 s. Driver positions arrive as
// driver.location_updated and are applied directly. A response older than
// what is already shown (lower version, or an outdated request) is ignored.

import type { DriverLocation, Order } from '../api/types'
import type { RealtimeEvent, RealtimeStatus } from '../services/realtime'
import { isActiveOrderStatus, type OrderStatus } from '../utils/status'
import { createLatest, type LatestRunner } from './latest'

/** Order event types delivered to the passenger's user scope. */
export const ORDER_EVENT_TYPES = [
  'order.created',
  'order.accepted',
  'order.driver_arriving',
  'order.started',
  'order.stop_completed',
  'order.completed',
  'order.cancelled',
  'order.disputed',
] as const

export const DRIVER_LOCATION_EVENT = 'driver.location_updated'
export const POLL_WHILE_DISCONNECTED_MS = 10_000
export const POLL_WHILE_CONNECTED_MS = 30_000

/** Status an order event announces (the re-fetch is still authoritative). */
export function statusOfEvent(type: string): OrderStatus | null {
  switch (type) {
    case 'order.created':
      return 'searching'
    case 'order.accepted':
      return 'accepted'
    case 'order.driver_arriving':
      return 'driver_arriving'
    case 'order.started':
    case 'order.stop_completed':
      return 'in_progress'
    case 'order.completed':
      return 'completed'
    case 'order.cancelled':
      return 'cancelled'
    case 'order.disputed':
      return 'disputed'
    default:
      return null
  }
}

export function eventOrderId(e: RealtimeEvent): string | null {
  const id = e.data.order_id
  return typeof id === 'string' ? id.toLowerCase() : null
}

/** True when `next` is at least as new as `current` (orders.version increases on every change). */
export function isSameOrNewer(current: Order | null, next: Order): boolean {
  return current === null || current.id !== next.id || next.version >= current.version
}

/** Driver position from a driver.location_updated event for `order`, or null when it does not apply. */
export function locationFromEvent(order: Order | null, e: RealtimeEvent): DriverLocation | null {
  if (!order || !order.driver || !isActiveOrderStatus(order.status) || order.status === 'searching') return null
  const d = e.data
  if (eventOrderId(e) !== order.id) return null
  if (typeof d.driver_id === 'string' && d.driver_id !== order.driver.id) return null
  const lat = d.lat
  const lon = d.lon
  if (typeof lat !== 'number' || typeof lon !== 'number' || !Number.isFinite(lat) || !Number.isFinite(lon)) return null
  const recordedAt = typeof d.recorded_at === 'string' ? d.recorded_at : new Date().toISOString()
  const prev = order.driver_location
  if (prev && Date.parse(prev.recorded_at) > Date.parse(recordedAt)) return null
  return {
    lat,
    lon,
    accuracy_m: typeof d.accuracy_m === 'number' ? d.accuracy_m : null,
    heading: typeof d.heading === 'number' ? d.heading : null,
    recorded_at: recordedAt,
  }
}

/** The part of the realtime client the sync needs (lets tests pass a fake). */
export interface RealtimeSource {
  readonly status: RealtimeStatus
  on(type: string, handler: (event: RealtimeEvent) => void): () => void
  onResync(cb: () => void): () => void
  onStatus(cb: (status: RealtimeStatus) => void): () => void
}

export interface LiveOrderOptions {
  orderId: string
  fetchOrder: (id: string, signal: AbortSignal) => Promise<Order>
  realtime: RealtimeSource
  onOrder: (order: Order) => void
  onError?: (error: unknown) => void
  /** Status hint from an event, before the re-fetch confirms it. */
  onHint?: (status: OrderStatus) => void
}

export class LiveOrderSync {
  private order: Order | null = null
  private readonly runner: LatestRunner<void>
  private readonly offs: Array<() => void> = []
  private pollTimer: ReturnType<typeof setTimeout> | undefined
  private stopped = true
  private readonly orderId: string

  constructor(private readonly opts: LiveOrderOptions) {
    this.orderId = opts.orderId.toLowerCase()
    this.runner = createLatest<void, Order>({
      run: (_, signal) => opts.fetchOrder(this.orderId, signal),
      onResult: (order) => this.accept(order),
      onError: (err) => {
        opts.onError?.(err)
        this.schedulePoll()
      },
    })
  }

  get current(): Order | null {
    return this.order
  }

  start(): void {
    if (!this.stopped) return
    this.stopped = false
    const rt = this.opts.realtime
    for (const type of ORDER_EVENT_TYPES) {
      this.offs.push(
        rt.on(type, (e) => {
          if (eventOrderId(e) !== this.orderId) return
          const hint = statusOfEvent(e.type)
          if (hint) this.opts.onHint?.(hint)
          this.refresh()
        }),
      )
    }
    this.offs.push(
      rt.on(DRIVER_LOCATION_EVENT, (e) => {
        const loc = locationFromEvent(this.order, e)
        if (!loc || !this.order) return
        this.order = { ...this.order, driver_location: loc }
        this.opts.onOrder(this.order)
      }),
    )
    this.offs.push(rt.onResync(() => this.refresh()))
    this.offs.push(rt.onStatus(() => this.schedulePoll()))
    this.refresh()
  }

  stop(): void {
    this.stopped = true
    this.runner.cancel()
    clearTimeout(this.pollTimer)
    for (const off of this.offs.splice(0)) off()
  }

  /** Re-fetches now; an older pending fetch is superseded. */
  refresh(): void {
    if (this.stopped) return
    clearTimeout(this.pollTimer)
    this.runner.schedule(undefined, { immediate: true })
  }

  /** Applies an order the caller already has (e.g. the cancel response). */
  accept(order: Order): void {
    if (this.stopped || order.id.toLowerCase() !== this.orderId) return
    if (isSameOrNewer(this.order, order)) {
      // A REST payload carries the last stored location; keep a newer live one.
      const live = this.order?.driver_location
      const merged =
        live && order.driver_location && Date.parse(live.recorded_at) > Date.parse(order.driver_location.recorded_at) && isActiveOrderStatus(order.status)
          ? { ...order, driver_location: live }
          : order
      this.order = merged
      this.opts.onOrder(merged)
    }
    this.schedulePoll()
  }

  private schedulePoll(): void {
    clearTimeout(this.pollTimer)
    if (this.stopped || (this.order && !isActiveOrderStatus(this.order.status))) return
    const every = this.opts.realtime.status === 'open' ? POLL_WHILE_CONNECTED_MS : POLL_WHILE_DISCONNECTED_MS
    this.pollTimer = setTimeout(() => this.refresh(), every)
  }
}
