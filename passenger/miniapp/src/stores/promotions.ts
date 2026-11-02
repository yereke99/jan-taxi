// The passenger's promotions (§23): coupons, the bonus balance, offers,
// banners, the invitation state and the marketing messages switch, from GET
// /promotions. Loaded on demand (the booking flow, the profile and the
// promotion screens ask for it) and kept fresh by realtime hints:
// promotions.updated, a completed or cancelled order (a coupon is used,
// returned or earned) and a resync. An API without promotions (404) or a
// failed first request leaves every promotion UI hidden; ordering never
// depends on it.
//
// Coupon validity is decided by expires_at on the server's clock: the store
// keeps offsetMs = server_time - Date.now() from the last answer, and `clock`
// (the server time of the last validity check) moves exactly when a coupon
// expires, so the booking price and the profile row change at that instant
// without a per-second timer.
//
// A coupon the server refused for an order although the answer shows it
// usable (its campaign's coupons are frozen, which GET /promotions does not
// say) is remembered by id: orders leave it out until an answer changes that
// coupon, so every next booking does not run into 409 promotion_changed
// again. A new app session tries it again.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getPromotions, setMarketingSubscription } from '../api/promotions'
import type { PromoCoupon, Promotions } from '../api/types'
import { activeCoupon, nextCouponExpiry, serverOffset } from '../domain/promotions'
import { isApiError } from '../services/http'
import { useRealtime } from '../services/realtime'

export const PROMOTIONS_UPDATED_EVENT = 'promotions.updated'
/** Order events after which a coupon or the bonus may have changed. */
export const PROMOTION_ORDER_EVENTS = ['order.completed', 'order.cancelled'] as const
/** Longest single wait of the expiry timer (setTimeout overflows after ~24.8 days). */
const MAX_TIMER_MS = 3_600_000

/** What GET /promotions says about a coupon; a refusal holds while it stays the same. */
function couponState(c: PromoCoupon): string {
  return [c.status, c.expires_at, c.amount, c.min_order_fare, c.categories.join(',')].join('|')
}

export const usePromotionsStore = defineStore('promotions', () => {
  const data = shallowRef<Promotions | null>(null)
  const loading = ref(false)
  const error = shallowRef<unknown>(null)
  /** The API has no promotions (404): every promotion UI stays hidden. */
  const unavailable = ref(false)
  /** server_time minus the device clock at the last answer, ms. */
  const offsetMs = ref(0)
  /** The server's clock at the last coupon validity check. */
  const clock = ref(Date.now())
  /** Counts realtime hints that promotions changed; screens with their own promotion data re-read it. */
  const changes = ref(0)
  const savingSubscription = ref(false)
  /** Coupons the server refused although they look usable: id → the coupon's state then. */
  const refusedCoupons = shallowRef<ReadonlyMap<string, string>>(new Map())

  let request = 0
  let inflight: Promise<void> | null = null
  let watching = false
  let expiryTimer: ReturnType<typeof setTimeout> | undefined

  /** The server's clock now: the device clock corrected by the last offset. */
  function serverNow(): number {
    return Date.now() + offsetMs.value
  }

  /** The coupon the passenger can still use (the profile shows it). */
  const coupon = computed(() => activeCoupon(data.value, clock.value))

  /** The promotions an order may use: the answer without the coupons the server refused. */
  const usable = computed<Promotions | null>(() => {
    const p = data.value
    const refused = refusedCoupons.value
    if (!p || refused.size === 0) return p
    return { ...p, coupons: p.coupons.filter((c) => !refused.has(c.id)) }
  })

  /** The server refused `c` for an order although the answer shows it usable. */
  function refuseCoupon(c: PromoCoupon): void {
    refusedCoupons.value = new Map(refusedCoupons.value).set(c.id, couponState(c))
  }

  function couponRefused(id: string): boolean {
    return refusedCoupons.value.has(id)
  }

  /** Forgets a refusal once an answer changes that coupon (or no longer lists it). */
  function keepRefusals(p: Promotions): void {
    const refused = refusedCoupons.value
    if (refused.size === 0) return
    const next = new Map([...refused].filter(([id, state]) => p.coupons.some((c) => c.id === id && couponState(c) === state)))
    if (next.size !== refused.size) refusedCoupons.value = next
  }

  /** Moves `clock` to now and waits for the next coupon expiry. */
  function checkExpiry(): void {
    clearTimeout(expiryTimer)
    clock.value = serverNow()
    const next = nextCouponExpiry(data.value, clock.value)
    if (next !== null) expiryTimer = setTimeout(checkExpiry, Math.min(Math.max(0, next - serverNow()), MAX_TIMER_MS))
  }

  async function load(): Promise<void> {
    const id = ++request
    loading.value = true
    try {
      const p = await getPromotions()
      if (id !== request) return
      offsetMs.value = serverOffset(p.server_time)
      keepRefusals(p)
      data.value = p
      error.value = null
      unavailable.value = false
      checkExpiry()
    } catch (err) {
      if (id !== request) return
      error.value = err
      if (isApiError(err) && err.status === 404) {
        unavailable.value = true
        data.value = null
        clearTimeout(expiryTimer)
      }
    } finally {
      if (id === request) loading.value = false
    }
  }

  /** A realtime hint: re-read, unless the API has no promotions. */
  function onHint(): void {
    if (unavailable.value) return
    changes.value++
    void refresh()
  }

  function watchRealtime(): void {
    if (watching) return
    try {
      const rt = useRealtime()
      rt.on(PROMOTIONS_UPDATED_EVENT, onHint)
      for (const type of PROMOTION_ORDER_EVENTS) rt.on(type, onHint)
      rt.onResync(() => {
        if (data.value) onHint()
      })
      watching = true
    } catch {
      // No realtime client (tests, failed start): screens re-read when they open.
    }
  }

  /** Re-reads GET /promotions; a newer read supersedes an older one. Never throws. */
  function refresh(): Promise<void> {
    watchRealtime()
    const p = load()
    inflight = p
    void p.finally(() => {
      if (inflight === p) inflight = null
    })
    return p
  }

  /** Loads once; a read in flight is shared. */
  function ensureLoaded(): Promise<void> {
    watchRealtime()
    if (data.value || unavailable.value) return Promise.resolve()
    return inflight ?? refresh()
  }

  /** Saves the marketing messages switch: shown at once, put back when saving fails (the error is rethrown). */
  async function setSubscribed(next: boolean): Promise<void> {
    const before = data.value
    if (!before || savingSubscription.value) return
    savingSubscription.value = true
    data.value = { ...before, marketing_subscribed: next }
    try {
      const saved = await setMarketingSubscription(next)
      if (data.value) data.value = { ...data.value, marketing_subscribed: saved }
    } catch (err) {
      if (data.value) data.value = { ...data.value, marketing_subscribed: before.marketing_subscribed }
      throw err
    } finally {
      savingSubscription.value = false
    }
  }

  return {
    data,
    loading,
    error,
    unavailable,
    offsetMs,
    clock,
    changes,
    savingSubscription,
    coupon,
    usable,
    serverNow,
    refresh,
    ensureLoaded,
    setSubscribed,
    refuseCoupon,
    couponRefused,
  }
})
