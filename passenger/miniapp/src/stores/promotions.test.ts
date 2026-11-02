import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { parsePromotions } from '../api/parse'
import { getPromotions, setMarketingSubscription } from '../api/promotions'
import { ApiError } from '../services/http'
import { usePromotionsStore } from './promotions'

const rt = vi.hoisted(() => ({
  handlers: new Map<string, Array<() => void>>(),
  resyncs: [] as Array<() => void>,
}))

vi.mock('../api/promotions', () => ({ getPromotions: vi.fn(), setMarketingSubscription: vi.fn() }))
vi.mock('../services/realtime', () => ({
  useRealtime: () => ({
    on: (type: string, handler: () => void) => {
      rt.handlers.set(type, [...(rt.handlers.get(type) ?? []), handler])
      return () => undefined
    },
    onResync: (cb: () => void) => {
      rt.resyncs.push(cb)
      return () => undefined
    },
  }),
}))

const get = vi.mocked(getPromotions)
const save = vi.mocked(setMarketingSubscription)

/** The device clock; the server runs ten minutes ahead of it. */
const DEVICE_NOW = Date.parse('2026-11-02T10:00:00Z')
const SERVER_AHEAD_MS = 600_000

function promotions(couponExpiresInMs = 5000, over: Record<string, unknown> = {}) {
  const server = DEVICE_NOW + SERVER_AHEAD_MS
  return parsePromotions({
    server_time: new Date(server).toISOString(),
    redemptions_enabled: true,
    coupons: [
      {
        id: 'c1',
        amount: 400,
        min_order_fare: 400,
        categories: ['taxi'],
        status: 'issued',
        issued_at: new Date(server - 3_600_000).toISOString(),
        expires_at: new Date(server + couponExpiresInMs).toISOString(),
      },
    ],
    bonus: { balance: 300, reserved: 0, available: 300 },
    bonus_categories: ['taxi'],
    referral: { state: 'unlocked', reward_amount: 300, rewarded: false },
    marketing_subscribed: true,
    ...over,
  })
}

function emit(type: string): void {
  for (const h of rt.handlers.get(type) ?? []) h()
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  vi.setSystemTime(DEVICE_NOW)
  get.mockReset()
  save.mockReset()
  rt.handlers.clear()
  rt.resyncs.length = 0
})

afterEach(() => vi.useRealTimers())

describe('promotions store', () => {
  it('keeps the server clock offset and drops the coupon exactly at expires_at on that clock', async () => {
    get.mockResolvedValue(promotions(5000))
    const store = usePromotionsStore()
    await store.ensureLoaded()
    expect(store.offsetMs).toBe(SERVER_AHEAD_MS)
    expect(store.serverNow()).toBe(DEVICE_NOW + SERVER_AHEAD_MS)
    expect(store.coupon?.id).toBe('c1')

    // On the device clock the coupon would last ten more minutes; on the server's it has 5 s.
    await vi.advanceTimersByTimeAsync(4999)
    expect(store.coupon?.id).toBe('c1')
    await vi.advanceTimersByTimeAsync(1)
    expect(store.coupon).toBeNull()
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('loads once on demand and shares a read in flight', async () => {
    get.mockResolvedValue(promotions())
    const store = usePromotionsStore()
    await Promise.all([store.ensureLoaded(), store.ensureLoaded()])
    await store.ensureLoaded()
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('hides promotions silently when the API has none (404) and ignores later hints', async () => {
    get.mockRejectedValue(new ApiError(404, 'not_found', 'no route'))
    const store = usePromotionsStore()
    await expect(store.ensureLoaded()).resolves.toBeUndefined()
    expect(store.unavailable).toBe(true)
    expect(store.data).toBeNull()
    emit('promotions.updated')
    emit('order.completed')
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('keeps the last answer when a re-read fails', async () => {
    get.mockResolvedValueOnce(promotions())
    const store = usePromotionsStore()
    await store.ensureLoaded()
    get.mockRejectedValueOnce(new ApiError(0, 'network_error', 'offline'))
    await store.refresh()
    expect(store.data?.bonus.available).toBe(300)
    expect(store.unavailable).toBe(false)
  })

  it('re-reads on promotions.updated, a completed or cancelled order and a resync, counting the changes', async () => {
    get.mockResolvedValue(promotions())
    const store = usePromotionsStore()
    await store.ensureLoaded()

    get.mockResolvedValue(promotions(5000, { bonus: { balance: 0, reserved: 0, available: 0 } }))
    emit('promotions.updated')
    await vi.advanceTimersByTimeAsync(0)
    expect(store.data?.bonus.available).toBe(0)
    emit('order.cancelled')
    emit('order.completed')
    for (const cb of rt.resyncs) cb()
    await vi.advanceTimersByTimeAsync(0)
    expect(get).toHaveBeenCalledTimes(5)
    expect(store.changes).toBe(4)
  })

  it('keeps a coupon the server refused out of orders until an answer changes that coupon', async () => {
    const answer = promotions(86_400_000)
    get.mockResolvedValue(answer)
    const store = usePromotionsStore()
    await store.ensureLoaded()
    store.refuseCoupon(store.data!.coupons[0]!)
    expect(store.couponRefused('c1')).toBe(true)
    expect(store.usable?.coupons).toEqual([])
    // The screens still list it; only orders leave it out.
    expect(store.data?.coupons.map((c) => c.id)).toEqual(['c1'])
    expect(store.usable?.bonus.available).toBe(300)

    // The same coupon in a new answer (a frozen campaign does not show): still refused.
    await store.refresh()
    expect(store.couponRefused('c1')).toBe(true)

    // Taken by an order since, so not frozen: forgotten, and usable once it is back.
    const server = DEVICE_NOW + SERVER_AHEAD_MS
    const reserved = { id: 'c1', amount: 400, min_order_fare: 400, categories: ['taxi'], status: 'reserved', expires_at: new Date(server + 86_400_000).toISOString() }
    get.mockResolvedValueOnce(promotions(86_400_000, { coupons: [reserved] }))
    await store.refresh()
    expect(store.couponRefused('c1')).toBe(false)
    await store.refresh()
    expect(store.usable?.coupons.map((c) => c.id)).toEqual(['c1'])
  })

  it('shows the marketing switch at once and puts it back when saving fails', async () => {
    get.mockResolvedValue(promotions())
    const store = usePromotionsStore()
    await store.ensureLoaded()

    save.mockResolvedValueOnce(false)
    await store.setSubscribed(false)
    expect(save).toHaveBeenCalledWith(false)
    expect(store.data?.marketing_subscribed).toBe(false)

    let fail!: (err: unknown) => void
    save.mockImplementationOnce(() => new Promise((_, reject) => (fail = reject)))
    const saving = store.setSubscribed(true)
    expect(store.data?.marketing_subscribed).toBe(true)
    expect(store.savingSubscription).toBe(true)
    fail(new ApiError(0, 'network_error', 'offline'))
    await expect(saving).rejects.toBeInstanceOf(ApiError)
    expect(store.data?.marketing_subscribed).toBe(false)
    expect(store.savingSubscription).toBe(false)
  })
})
