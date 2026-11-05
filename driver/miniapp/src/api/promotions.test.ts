import { describe, expect, it } from 'vitest'
import { parseDriverOffers } from './promotions'

describe('driver promotion offers', () => {
  it('hides missing or malformed campaigns instead of inventing a reward', () => {
    expect(parseDriverOffers(null)).toEqual([])
    expect(parseDriverOffers({ offers: [{ id: 'x', amount: 0, rides: 10 }, { id: 'y', amount: 500, rides: 0 }] })).toEqual([])
  })
  it('keeps server progress and distinguishes a credited promotion', () => {
    expect(parseDriverOffers({ offers: [{ id: 'campaign', amount: 500, rides: 10, completed: 3, rewarded: false,
      title: { kk: 'Сыйлық', ru: 'Подарок', en: 'Gift' }, categories: ['taxi'], ends_at: '2026-12-01T00:00:00Z' }] })).toEqual([
      { id: 'campaign', amount: 500, rides: 10, completed: 3, rewarded: false,
        title: { kk: 'Сыйлық', ru: 'Подарок', en: 'Gift' }, description: null, categories: ['taxi'], ends_at: '2026-12-01T00:00:00Z' },
    ])
  })
})
