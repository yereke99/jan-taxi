import { describe, expect, it } from 'vitest'
import { groupOf, radiusSchedule } from './settingsModel'

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
    expect(groupOf('something.new')).toBe('other')
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
