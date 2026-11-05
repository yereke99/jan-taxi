import { describe, expect, it } from 'vitest'
import type { PromoCoupon, PromoOffer, Promotions, ReferralSummary } from '../api/types'
import {
  activeCoupon,
  choosePromotion,
  countdown,
  couponBelowMinimum,
  couponStatus,
  hasPromo,
  homeGift,
  inviteOutOfReach,
  nextCouponExpiry,
  payable,
  serverOffset,
} from './promotions'

const NOW = Date.parse('2026-11-02T10:00:00Z')
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString()

function coupon(over: Partial<PromoCoupon> = {}): PromoCoupon {
  return {
    id: 'c1',
    title: null,
    amount: 400,
    min_order_fare: 400,
    categories: ['taxi'],
    status: 'issued',
    issued_at: iso(-3_600_000),
    expires_at: iso(72 * 3_600_000),
    reserved_order_id: null,
    redeemed_at: null,
    ...over,
  }
}

function promos(over: Partial<Promotions> = {}): Promotions {
  return {
    server_time: iso(0),
    redemptions_enabled: true,
    coupons: [coupon()],
    bonus: { balance: 0, reserved: 0, available: 0 },
    bonus_categories: ['taxi'],
    offers: [],
    banners: [],
    referral: { state: 'unavailable', lock_reason: null, reward_amount: 0, rewarded: false },
    marketing_subscribed: true,
    ...over,
  }
}

const withBonus = (available: number, over: Partial<Promotions> = {}) => promos({ bonus: { balance: available, reserved: 0, available }, ...over })

describe('map gift', () => {
  it('prioritises an available invitation with the server reward', () => {
    expect(homeGift(promos({ referral: { state: 'unlocked', lock_reason: null, reward_amount: 200, rewarded: false } }), NOW)).toEqual({ kind: 'invite', amount: 200 })
  })
  it('shows earned balance instead of promising the referral reward again', () => {
    expect(homeGift(withBonus(200, { referral: { state: 'unlocked', lock_reason: null, reward_amount: 200, rewarded: true } }), NOW)).toEqual({ kind: 'bonus', amount: 200 })
  })
  it('hides without an eligible promotion, including expired coupons', () => {
    expect(homeGift(null, NOW)).toBeNull()
    expect(homeGift(promos({ coupons: [coupon({ expires_at: iso(-1) })] }), NOW)).toBeNull()
    expect(homeGift(promos({ coupons: [], referral: { state: 'locked', lock_reason: 'paused', reward_amount: 200, rewarded: false } }), NOW)).toBeNull()
  })
})

describe('choosePromotion (§23.6)', () => {
  it('applies the issued coupon to a taxi order at or above its minimum fare', () => {
    expect(choosePromotion(promos(), 'taxi', 1500, NOW)).toMatchObject({ kind: 'coupon', discount: 400 })
    expect(choosePromotion(promos(), 'taxi', 400, NOW)).toMatchObject({ kind: 'coupon', discount: 400 })
  })

  it('never uses the coupon and the bonus together: the coupon first', () => {
    expect(choosePromotion(withBonus(300, { coupons: [coupon()] }), 'taxi', 1500, NOW)).toMatchObject({ kind: 'coupon', discount: 400, coupon: { id: 'c1' } })
  })

  it('falls back to the bonus, which pays at most the fare', () => {
    expect(choosePromotion(withBonus(300, { coupons: [] }), 'taxi', 1500, NOW)).toEqual({ kind: 'bonus', discount: 300, coupon: null })
    expect(choosePromotion(withBonus(900, { coupons: [] }), 'taxi', 700, NOW)).toEqual({ kind: 'bonus', discount: 700, coupon: null })
    // Below the coupon's minimum the bonus still applies.
    expect(choosePromotion(withBonus(300, { coupons: [coupon({ min_order_fare: 1000 })] }), 'taxi', 700, NOW)).toMatchObject({ kind: 'bonus', discount: 300 })
  })

  it('applies a coupon to a delivery only when the coupon covers that service; the bonus stays taxi', () => {
    const both = promos({ coupons: [coupon({ categories: ['taxi', 'parcel'] })], bonus: { balance: 300, reserved: 0, available: 300 } })
    expect(choosePromotion(both, 'parcel', 800, NOW)).toMatchObject({ kind: 'coupon', discount: 400 })
    expect(choosePromotion(both, 'other', 800, NOW)).toBeNull()
    expect(couponBelowMinimum(both, 'parcel', 300, NOW)?.id).toBe('c1')
  })

  it('offers nothing for deliveries, below the minimum, after expiry or while redemptions are off', () => {
    expect(choosePromotion(withBonus(300), 'parcel', 1500, NOW)).toBeNull()
    expect(choosePromotion(promos({ coupons: [coupon({ min_order_fare: 1000 })] }), 'taxi', 700, NOW)).toBeNull()
    expect(choosePromotion(promos({ coupons: [coupon({ expires_at: iso(0) })] }), 'taxi', 1500, NOW)).toBeNull()
    expect(choosePromotion(promos({ coupons: [coupon({ status: 'reserved' })] }), 'taxi', 1500, NOW)).toBeNull()
    expect(choosePromotion(withBonus(300, { redemptions_enabled: false }), 'taxi', 1500, NOW)).toBeNull()
    expect(choosePromotion(null, 'taxi', 1500, NOW)).toBeNull()
    expect(choosePromotion(promos(), 'taxi', null, NOW)).toBeNull()
  })

  it('takes the coupon that expires first, like the server, and leaves refused kinds out', () => {
    const later = coupon({ id: 'later', expires_at: iso(48 * 3_600_000) })
    const sooner = coupon({ id: 'sooner', expires_at: iso(3_600_000) })
    expect(choosePromotion(promos({ coupons: [later, sooner] }), 'taxi', 1500, NOW)?.coupon?.id).toBe('sooner')
    expect(choosePromotion(withBonus(300), 'taxi', 1500, NOW, ['coupon'])).toMatchObject({ kind: 'bonus', discount: 300 })
    expect(choosePromotion(withBonus(300), 'taxi', 1500, NOW, ['coupon', 'bonus'])).toBeNull()
  })

  it('names the coupon a fare is too low for, only while no coupon applies', () => {
    const p = promos({ coupons: [coupon({ min_order_fare: 1000 })] })
    expect(couponBelowMinimum(p, 'taxi', 700, NOW)?.min_order_fare).toBe(1000)
    expect(couponBelowMinimum(p, 'taxi', 1000, NOW)).toBeNull()
    expect(couponBelowMinimum(p, 'parcel', 700, NOW)).toBeNull()
    expect(couponBelowMinimum({ ...p, redemptions_enabled: false }, 'taxi', 700, NOW)).toBeNull()
  })
})

describe('coupon validity on the server clock', () => {
  it('reads an issued coupon as expired from expires_at on', () => {
    const c = coupon({ expires_at: iso(5000) })
    expect(couponStatus(c, NOW + 4999)).toBe('issued')
    expect(couponStatus(c, NOW + 5000)).toBe('expired')
    expect(couponStatus(coupon({ status: 'redeemed' }), NOW)).toBe('redeemed')
    expect(activeCoupon(promos({ coupons: [c] }), NOW + 5000)).toBeNull()
    expect(nextCouponExpiry(promos({ coupons: [c, coupon({ id: 'c2' })] }), NOW)).toBe(NOW + 5000)
  })

  it('measures the server offset from server_time', () => {
    expect(serverOffset(iso(600_000), NOW)).toBe(600_000)
    expect(serverOffset('not a date', NOW)).toBe(0)
  })

  it('counts down in whole seconds rounded up and reaches zero exactly at expires_at', () => {
    const expires = iso(2 * 86_400_000 + 3 * 3_600_000 + 4 * 60_000 + 5_000)
    expect(countdown(expires, NOW)).toEqual({ days: 2, hours: 3, minutes: 4, seconds: 5, expired: false })
    expect(countdown(iso(1500), NOW)).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 2, expired: false })
    expect(countdown(iso(1), NOW)).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 1, expired: false })
    expect(countdown(iso(0), NOW)).toEqual({ days: 0, hours: 0, minutes: 0, seconds: 0, expired: true })
    expect(countdown('', NOW).expired).toBe(true)
  })
})

describe('payable', () => {
  it('is the cash due with a promotion and the fare without one', () => {
    expect(payable({ fare: 1500, promo_discount: 400, cash_due: 1100 })).toBe(1100)
    expect(payable({ fare: 700, promo_discount: 0, cash_due: 700 })).toBe(700)
    expect(payable({ fare: 1500, promo_discount: 400, cash_due: 1100, status: 'completed' })).toBe(1100)
  })

  it('reads a cancelled order as its plain fare: the discount stays only as a snapshot', () => {
    const cancelled = { fare: 1500, promo_discount: 400, cash_due: 1100, status: 'cancelled' }
    expect(hasPromo(cancelled)).toBe(false)
    expect(payable(cancelled)).toBe(1500)
  })
})

describe('inviteOutOfReach (§23.5, §23.14 items 1–2)', () => {
  const locked: ReferralSummary = { state: 'locked', lock_reason: 'coupon_not_redeemed', reward_amount: 300, rewarded: false }
  const offer: PromoOffer = { kind: 'first_ride_coupon', title: null, description: null, amount: 400, validity_hours: 72, categories: ['taxi'] }

  it('is out of reach without a first-ride coupon to use or to earn (a passenger who rode before the launch)', () => {
    expect(inviteOutOfReach(locked, promos({ coupons: [] }), NOW)).toBe(true)
    expect(inviteOutOfReach({ ...locked, lock_reason: 'coupon_not_used' }, promos({ coupons: [] }), NOW)).toBe(true)
    // An unknown reason reads like the default policy, as the screen's text does.
    expect(inviteOutOfReach({ ...locked, lock_reason: null }, promos({ coupons: [] }), NOW)).toBe(true)
  })

  it('is out of reach once the coupon expired unused or was voided', () => {
    expect(inviteOutOfReach(locked, promos({ coupons: [coupon({ status: 'expired' })] }), NOW)).toBe(true)
    expect(inviteOutOfReach(locked, promos({ coupons: [coupon({ expires_at: iso(0) })] }), NOW)).toBe(true)
    expect(inviteOutOfReach(locked, promos({ coupons: [coupon({ status: 'voided' })] }), NOW)).toBe(true)
  })

  it('can still open while a coupon waits to be used, is on an order or can be earned', () => {
    expect(inviteOutOfReach(locked, promos(), NOW)).toBe(false)
    expect(inviteOutOfReach(locked, promos({ coupons: [coupon({ status: 'reserved', expires_at: iso(-60_000) })] }), NOW)).toBe(false)
    expect(inviteOutOfReach(locked, promos({ coupons: [], offers: [offer] }), NOW)).toBe(false)
  })

  it('leaves the other states and lock reasons as the server says', () => {
    const none = promos({ coupons: [] })
    expect(inviteOutOfReach({ ...locked, state: 'unlocked', lock_reason: null }, none, NOW)).toBe(false)
    expect(inviteOutOfReach({ ...locked, state: 'unavailable', lock_reason: null }, none, NOW)).toBe(false)
    expect(inviteOutOfReach({ ...locked, lock_reason: 'first_ride_required' }, none, NOW)).toBe(false)
    expect(inviteOutOfReach({ ...locked, lock_reason: 'paused' }, none, NOW)).toBe(false)
    expect(inviteOutOfReach({ ...locked, lock_reason: 'disabled' }, none, NOW)).toBe(false)
  })
})
