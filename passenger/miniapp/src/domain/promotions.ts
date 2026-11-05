// Promotions as the passenger app presents and requests them (ARCHITECTURE
// §23): the promotion an order can use, a coupon's state on the server's
// clock, the countdown to its expiry, what the passenger pays for an order
// and whether inviting can still open. The server decides and computes every
// discount; these rules only mirror it, so the price the app shows is the
// price the server accepts. When they differ, the server refuses the order
// (409 promotion_changed) instead of charging another amount.

import type { CouponStatus, OrderPromoAmounts, PromoCoupon, PromotionKind, Promotions, ReferralSummary } from '../api/types'
import type { Category } from '../utils/status'

/** Never advertise a referral reward the passenger has already received. */
export function homeGift(p: Promotions | null, now: number): { kind: 'invite' | 'coupons' | 'bonus'; amount: number } | null {
  if (!p) return null
  if (p.referral.state === 'unlocked' && !p.referral.rewarded && p.referral.reward_amount > 0) return { kind: 'invite', amount: p.referral.reward_amount }
  if (p.redemptions_enabled && p.bonus.available > 0) return { kind: 'bonus', amount: p.bonus.available }
  const coupon = activeCoupon(p, now)
  if (coupon) return { kind: 'coupons', amount: coupon.amount }
  if (p.offers[0]) return { kind: 'coupons', amount: p.offers[0].amount }
  return p.banners.length ? { kind: 'coupons', amount: 0 } : null
}

/** The promotion an order can use: a coupon applies by itself, the bonus balance is the passenger's choice. */
export interface PromotionOffer {
  kind: PromotionKind
  discount: number
  /** The coupon that applies (kind coupon). */
  coupon: PromoCoupon | null
}

/** Issued and not yet past expires_at on the server's clock: the server would still take it. */
export function isCouponActive(c: PromoCoupon, serverNow: number): boolean {
  return c.status === 'issued' && Date.parse(c.expires_at) > serverNow
}

/** The state shown: an issued coupon reads expired from expires_at on, before the server's expiry job marks it. */
export function couponStatus(c: PromoCoupon, serverNow: number): CouponStatus {
  return c.status === 'issued' && !isCouponActive(c, serverNow) ? 'expired' : c.status
}

/** Active coupons, the soonest expiry first (the order the server takes them in). */
function activeCoupons(p: Promotions, serverNow: number, category?: Category): PromoCoupon[] {
  return p.coupons
    .filter((c) => isCouponActive(c, serverNow) && (category === undefined || c.categories.includes(category)))
    .sort((a, b) => Date.parse(a.expires_at) - Date.parse(b.expires_at))
}

/** The passenger's coupon that can still be used, whatever the order (the profile shows it). */
export function activeCoupon(p: Promotions | null, serverNow: number): PromoCoupon | null {
  return p ? (activeCoupons(p, serverNow)[0] ?? null) : null
}

/** The coupon the server would apply to an order of `category` at `fare`, or null. */
export function usableCoupon(p: Promotions, category: Category, fare: number, serverNow: number): PromoCoupon | null {
  if (!p.redemptions_enabled) return null
  return activeCoupons(p, serverNow, category).find((c) => fare >= c.min_order_fare) ?? null
}

/** An active coupon of the category whose minimum this fare does not reach, while no other coupon fits. */
export function couponBelowMinimum(p: Promotions | null, category: Category, fare: number | null, serverNow: number): PromoCoupon | null {
  if (!p || fare === null || !p.redemptions_enabled || usableCoupon(p, category, fare, serverNow)) return null
  return activeCoupons(p, serverNow, category).find((c) => fare < c.min_order_fare) ?? null
}

/** What the bonus balance pays of an order of `category` at `fare`: as much as is available, at most the fare. */
export function bonusDiscount(p: Promotions, category: Category, fare: number): number {
  if (!p.redemptions_enabled || !p.bonus_categories.includes(category)) return 0
  return Math.max(0, Math.min(p.bonus.available, fare))
}

/**
 * The promotion an order of `category` at `fare` can use, as the server
 * decides it (§23.6): a usable coupon first, else the bonus balance; never
 * both. `excluded` kinds were refused for this booking.
 */
export function choosePromotion(
  p: Promotions | null,
  category: Category,
  fare: number | null,
  serverNow: number,
  excluded: readonly PromotionKind[] = [],
): PromotionOffer | null {
  if (!p || fare === null || fare <= 0) return null
  if (!excluded.includes('coupon')) {
    const coupon = usableCoupon(p, category, fare, serverNow)
    if (coupon) return { kind: 'coupon', discount: coupon.amount, coupon }
  }
  if (!excluded.includes('bonus')) {
    const discount = bonusDiscount(p, category, fare)
    if (discount > 0) return { kind: 'bonus', discount, coupon: null }
  }
  return null
}

/** The next instant (server clock) an active coupon expires, or null. */
export function nextCouponExpiry(p: Promotions | null, serverNow: number): number | null {
  return p ? (activeCoupons(p, serverNow).map((c) => Date.parse(c.expires_at))[0] ?? null) : null
}

/** server_time minus the device clock, ms; 0 when the server time is unreadable. */
export function serverOffset(serverTime: string, deviceNow = Date.now()): number {
  const t = Date.parse(serverTime)
  return Number.isFinite(t) ? t - deviceNow : 0
}

export interface Countdown {
  days: number
  hours: number
  minutes: number
  seconds: number
  /** expires_at has come on the server's clock. */
  expired: boolean
}

/**
 * Time left until `expiresAt` on the server's clock in whole seconds, rounded
 * up: the countdown reaches zero exactly at expires_at, when the coupon turns
 * expired.
 */
export function countdown(expiresAt: string, serverNow: number): Countdown {
  const left = Date.parse(expiresAt) - serverNow
  if (!(left > 0)) return { days: 0, hours: 0, minutes: 0, seconds: 0, expired: true }
  const total = Math.ceil(left / 1000)
  return {
    days: Math.floor(total / 86_400),
    hours: Math.floor((total % 86_400) / 3600),
    minutes: Math.floor((total % 3600) / 60),
    seconds: total % 60,
    expired: false,
  }
}

type PromoAmounts = Pick<OrderPromoAmounts, 'promo_discount' | 'cash_due'> & {
  fare: number
  /** The order's status; a cancelled order reads as its plain fare. */
  status?: string
}

/**
 * The order's price has a coupon or bonus taken off. A cancelled order keeps
 * promo_discount only as a snapshot (§23.6): nothing is paid for it, so it
 * shows the plain fare.
 */
export function hasPromo(o: PromoAmounts): boolean {
  return o.promo_discount > 0 && o.status !== 'cancelled'
}

/** What the passenger pays the driver for an order: the fare less a coupon or bonus. */
export function payable(o: PromoAmounts): number {
  return hasPromo(o) ? o.cash_due : o.fare
}

/**
 * Inviting can never open for this passenger (§23.5, §23.14 items 1–2): it is
 * locked by a first-ride coupon rule (coupon_not_redeemed, coupon_not_used)
 * while the passenger has no such coupon left to use (issued, or on an
 * order) and none to earn (no first-ride offer): someone who rode before the
 * launch, whose first order was another service, or whose coupon expired or
 * was voided. The API still says "locked"; the app says the promotion is not
 * for them instead of promising a lock that never opens.
 */
export function inviteOutOfReach(r: Pick<ReferralSummary, 'state' | 'lock_reason'>, p: Promotions, serverNow: number): boolean {
  if (r.state !== 'locked') return false
  const reason = r.lock_reason ?? 'coupon_not_redeemed'
  if (reason !== 'coupon_not_redeemed' && reason !== 'coupon_not_used') return false
  if (p.offers.some((o) => o.kind === 'first_ride_coupon')) return false
  return !p.coupons.some((c) => c.status === 'reserved' || isCouponActive(c, serverNow))
}
