import { describe, expect, it } from 'vitest'
import { ROUTE } from './names'
import { intentLocation, parseStartParam } from './startParam'

describe('parseStartParam (passenger, §18)', () => {
  it.each([
    [null, { kind: 'home' }],
    [undefined, { kind: 'home' }],
    ['', { kind: 'home' }],
    ['home', { kind: 'home' }],
    ['taxi', { kind: 'book', category: 'taxi' }],
    ['parcel', { kind: 'book', category: 'parcel' }],
    ['other', { kind: 'book', category: 'other' }],
    ['orders', { kind: 'orders' }],
    ['support', { kind: 'support' }],
    ['driver', { kind: 'become_driver' }],
    ['order_7D9F2C1E-1111-4A2B-9C3D-123456789ABC', { kind: 'order', orderId: '7d9f2c1e-1111-4a2b-9c3d-123456789abc' }],
    ['order_not-a-uuid', { kind: 'home' }],
    ['unknown', { kind: 'home' }],
  ] as const)('%j', (param, intent) => {
    expect(parseStartParam(param)).toEqual(intent)
  })

  it.each([
    'taxi ',
    'TAXI',
    'taxi?x=1',
    'order_7d9f2c1e-1111-4a2b-9c3d-123456789abc/x',
    'order_7d9f2c1e-1111-4a2b-9c3d-123456789abcd',
    'x_order_7d9f2c1e-1111-4a2b-9c3d-123456789abc',
    'a'.repeat(65),
    'support%20',
    'тaxi',
  ])('rejects %j (strict format) and falls back to home', (param) => {
    expect(parseStartParam(param)).toEqual({ kind: 'home' })
  })
})

describe('intentLocation', () => {
  it('maps every intent to its screen', () => {
    expect(intentLocation({ kind: 'home' })).toEqual({ name: ROUTE.home })
    expect(intentLocation({ kind: 'book', category: 'parcel' })).toEqual({ name: ROUTE.book, query: { category: 'parcel' } })
    expect(intentLocation({ kind: 'orders' })).toEqual({ name: ROUTE.orders })
    expect(intentLocation({ kind: 'order', orderId: 'abc' })).toEqual({ name: ROUTE.order, params: { id: 'abc' } })
    expect(intentLocation({ kind: 'support' })).toEqual({ name: ROUTE.support })
    expect(intentLocation({ kind: 'become_driver' })).toEqual({ name: ROUTE.becomeDriver })
  })
})
