import { describe, expect, it } from 'vitest'
import { canAccess, entryRoute } from './access'
import { ROUTE } from './names'
import { intentLocation, parseStartParam, START_PARAM_RE } from './startParam'

const ID = '7d9f2c1e-1111-4a2b-9c3d-123456789abc'

describe('parseStartParam (driver)', () => {
  it.each([
    [null, { kind: 'home' }],
    ['', { kind: 'home' }],
    ['home', { kind: 'home' }],
    ['register', { kind: 'register' }],
    ['orders', { kind: 'orders' }],
    ['trip', { kind: 'trip' }],
    ['wallet', { kind: 'wallet' }],
    ['topup', { kind: 'topup' }],
    ['profile', { kind: 'profile' }],
    ['support', { kind: 'support' }],
    [`order_${ID}`, { kind: 'order', orderId: ID }],
    [`topup_${ID.toUpperCase()}`, { kind: 'topup_detail', topupId: ID }],
    ['topup_123', { kind: 'home' }],
    [`order_${ID}x`, { kind: 'home' }],
    ['taxi', { kind: 'home' }],
    ['Orders', { kind: 'home' }],
    ['orders?x=1', { kind: 'home' }],
    ['a'.repeat(65), { kind: 'home' }],
    ['../wallet', { kind: 'home' }],
  ] as const)('%j', (param, intent) => {
    expect(parseStartParam(param)).toEqual(intent)
  })

  it('uses the §18 charset', () => {
    expect(START_PARAM_RE.test(`order_${ID}`)).toBe(true)
    expect(START_PARAM_RE.test('order 1')).toBe(false)
  })
})

describe('intentLocation', () => {
  const router = { hasRoute: (name: string | symbol) => (Object.values(ROUTE) as string[]).includes(String(name)) }

  it('maps intents to routes with params', () => {
    expect(intentLocation({ kind: 'order', orderId: ID }, router)).toEqual({ name: ROUTE.order, params: { id: ID } })
    expect(intentLocation({ kind: 'topup_detail', topupId: ID }, router)).toEqual({ name: ROUTE.topupDetail, params: { id: ID } })
    expect(intentLocation({ kind: 'topup' }, router)).toEqual({ name: ROUTE.topup })
    expect(intentLocation({ kind: 'register' }, router)).toEqual({ name: ROUTE.register })
    expect(intentLocation({ kind: 'home' }, router)).toEqual({ name: ROUTE.home })
  })

  it('falls back to home for an unknown route', () => {
    expect(intentLocation({ kind: 'trip' }, { hasRoute: () => false })).toEqual({ name: ROUTE.home })
  })
})

describe('entry routing by driver status', () => {
  it.each([
    ['none', ROUTE.register],
    ['draft', ROUTE.register],
    ['changes_requested', ROUTE.register],
    ['pending', ROUTE.status],
    ['rejected', ROUTE.status],
    ['suspended', ROUTE.status],
    ['approved', ROUTE.home],
  ] as const)('%s → %s', (state, route) => {
    expect(entryRoute(state)).toBe(route)
  })

  it('keeps unapproved drivers out of orders and trips', () => {
    expect(canAccess('approved', 'pending')).toBe(false)
    expect(canAccess('approved', 'approved')).toBe(true)
    expect(canAccess('registration', 'approved')).toBe(false)
    expect(canAccess('registration', 'changes_requested')).toBe(true)
    expect(canAccess('status', 'suspended')).toBe(true)
    expect(canAccess('registered', 'draft')).toBe(false)
    expect(canAccess('registered', 'suspended')).toBe(true)
    expect(canAccess('any', 'none')).toBe(true)
    expect(canAccess(undefined, 'none')).toBe(true)
  })
})
