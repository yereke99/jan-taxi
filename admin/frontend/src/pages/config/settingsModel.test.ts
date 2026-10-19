import { describe, expect, it } from 'vitest'
import type { SettingItem } from '../../api/config'
import { commissionBounds, groupOf, intOutOfRange, radiusSchedule, welcomeBonusOn } from './settingsModel'

function item(key: string, value: unknown, constraints: SettingItem['constraints'] = {}): SettingItem {
  return {
    key,
    type: 'int',
    value,
    default: value,
    source: 'default',
    has_env_default: false,
    overrides_env: false,
    description: { kk: '', ru: '', en: '' },
    constraints,
    updated_at: null,
    updated_by: null,
  }
}

describe('groupOf', () => {
  it('puts each key in the group of its longest matching prefix', () => {
    expect(groupOf('topup.min_amount')).toBe('topup')
    expect(groupOf('topup.auto_confirm_enabled')).toBe('topup_auto')
    expect(groupOf('topup.auto_max_amount_per_day')).toBe('topup_auto')
    expect(groupOf('topup.receipt_max_age_hours')).toBe('topup_auto')
    expect(groupOf('company.bin')).toBe('company')
    expect(groupOf('kaspi.payment_url')).toBe('kaspi')
    expect(groupOf('dispatch.radius_max_m')).toBe('dispatch')
    expect(groupOf('commission.default_bps')).toBe('orders')
    expect(groupOf('orders.passenger_cancels_after_accept_per_day')).toBe('orders')
    expect(groupOf('something.new')).toBe('other')
  })

  it('collects the welcome bonus and commission keys in their own block', () => {
    for (const k of ['driver.welcome_bonus', 'commission.mode', 'commission.fixed_amount', 'commission.fixed_min', 'commission.fixed_max']) {
      expect(groupOf(k)).toBe('rules')
    }
    expect(groupOf('driver.min_online_balance')).toBe('driver')
  })
})

describe('bonus and commission rules', () => {
  it('treats a zero welcome bonus as switched off', () => {
    expect(welcomeBonusOn('500')).toBe(true)
    expect(welcomeBonusOn('1 000')).toBe(true)
    expect(welcomeBonusOn('0')).toBe(false)
    // Clearing the amount to type a new one keeps the field on screen.
    expect(welcomeBonusOn('')).toBe(true)
    expect(welcomeBonusOn('12x')).toBe(true)
  })

  it('reads the fixed commission bounds from the API constraints, then the read-only keys', () => {
    expect(commissionBounds([item('commission.fixed_amount', 50, { min: 50, max: 100 })])).toEqual({ min: 50, max: 100 })
    expect(commissionBounds([item('commission.fixed_amount', 70), item('commission.fixed_min', 60), item('commission.fixed_max', 90)])).toEqual({ min: 60, max: 90 })
    expect(commissionBounds([])).toEqual({ min: 50, max: 100 })
  })

  it('flags integers outside the setting range', () => {
    const amount = item('commission.fixed_amount', 50, { min: 50, max: 100 })
    expect(intOutOfRange(amount, 100)).toBe(false)
    expect(intOutOfRange(amount, 120)).toBe(true)
    expect(intOutOfRange(amount, 40)).toBe(true)
    expect(intOutOfRange(item('driver.welcome_bonus', 500, { min: 0, max: 100000 }), 0)).toBe(false)
  })
})

describe('radiusSchedule', () => {
  it('grows by one step per interval up to the maximum (ARCHITECTURE §7 defaults)', () => {
    expect(radiusSchedule(5000, 2500, 60, 10000)).toEqual([
      { afterS: 0, radiusM: 5000 },
      { afterS: 60, radiusM: 7500 },
      { afterS: 120, radiusM: 10000 },
    ])
  })

  it('caps the last step at the maximum and limits the rows', () => {
    expect(radiusSchedule(5000, 4000, 30, 10000)).toEqual([
      { afterS: 0, radiusM: 5000 },
      { afterS: 30, radiusM: 9000 },
      { afterS: 60, radiusM: 10000 },
    ])
    expect(radiusSchedule(500, 500, 10, 100000)).toHaveLength(6)
  })

  it('keeps a fixed radius when the step is zero', () => {
    expect(radiusSchedule(5000, 0, 60, 10000)).toEqual([{ afterS: 0, radiusM: 5000 }])
  })

  it('returns nothing for values the API would reject', () => {
    expect(radiusSchedule(5000, 2500, 60, 4000)).toEqual([])
    expect(radiusSchedule(5000, 2500, 0, 10000)).toEqual([])
    expect(radiusSchedule(Number.NaN, 2500, 60, 10000)).toEqual([])
  })
})
