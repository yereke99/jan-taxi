import { describe, expect, it } from 'vitest'
import { bonusKind, orderFunding } from './balance'

describe('order funding (commission charged at acceptance, §22.2)', () => {
  const fixed = { mode: 'fixed' as const, fixed_amount: 50, by_area: false }

  it('pauses orders when the available balance cannot pay the commission', () => {
    expect(orderFunding({ available: 49 }, fixed)).toEqual({ commission: 50, paused: true, shortfall: 1 })
    expect(orderFunding({ available: -20 }, fixed)).toEqual({ commission: 50, paused: true, shortfall: 70 })
    expect(orderFunding({ available: 50 }, fixed)).toEqual({ commission: 50, paused: false, shortfall: 0 })
  })

  it('says nothing when the commission is not known in advance', () => {
    expect(orderFunding({ available: 0 }, { mode: 'percent', fixed_amount: 0, by_area: false })).toEqual({ commission: null, paused: false, shortfall: 0 })
    // Villages with their own percentage: only the server knows what an order costs.
    expect(orderFunding({ available: 10 }, { ...fixed, by_area: true })).toEqual({ commission: null, paused: false, shortfall: 0 })
    expect(orderFunding({ available: 0 }, null).paused).toBe(false)
    expect(orderFunding(null, fixed)).toEqual({ commission: 50, paused: false, shortfall: 0 })
  })
})

describe('bonus entries', () => {
  it('tells the welcome bonus from a bonus an administrator granted', () => {
    expect(bonusKind({ entry_type: 'bonus_credit', description: 'welcome bonus' })).toBe('welcome')
    expect(bonusKind({ entry_type: 'bonus_credit', description: 'Үздік жүргізуші' })).toBe('manual')
    expect(bonusKind({ entry_type: 'bonus_credit', description: null })).toBe('manual')
    expect(bonusKind({ entry_type: 'topup_credit', description: 'welcome bonus' })).toBeNull()
  })
})
