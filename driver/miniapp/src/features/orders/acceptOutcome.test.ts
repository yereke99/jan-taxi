import { describe, expect, it } from 'vitest'
import { ApiError } from '../../services/http'
import { classifyAcceptError, suggestedTopUpAmount } from './acceptOutcome'

const ORDER = '7d9f2c1e-1111-4a2b-9c3d-123456789abc'

describe('classifyAcceptError', () => {
  it('409 order_already_taken → taken (toast + remove)', () => {
    expect(classifyAcceptError(new ApiError(409, 'order_already_taken', 'taken'))).toEqual({ kind: 'taken' })
  })

  it('an order out of reach (404) or closed without a driver (invalid_transition) has its own outcome', () => {
    expect(classifyAcceptError(new ApiError(404, 'not_found', 'gone'))).toEqual({ kind: 'unavailable' })
    expect(classifyAcceptError(new ApiError(409, 'invalid_transition', 'closed', {}, { from: 'cancelled', action: 'accept' }))).toEqual({
      kind: 'closed',
    })
  })

  it('402 insufficient_balance → top-up with the shortfall', () => {
    const err = new ApiError(402, 'insufficient_balance', 'low', {}, { required: 300, available: 120, currency: 'KZT', topup_min_amount: 500 })
    expect(classifyAcceptError(err)).toEqual({ kind: 'insufficient_balance', required: 300, available: 120, suggestedTopUp: 500 })
  })

  it('409 driver_has_active_order → go to the trip', () => {
    const err = new ApiError(409, 'driver_has_active_order', 'busy', {}, { active_order_id: ORDER })
    expect(classifyAcceptError(err)).toEqual({ kind: 'has_active_order', activeOrderId: ORDER })
    expect(classifyAcceptError(new ApiError(409, 'driver_has_active_order', 'busy'))).toEqual({ kind: 'has_active_order', activeOrderId: null })
  })

  it('409 driver_offline and 403 driver_not_approved', () => {
    expect(classifyAcceptError(new ApiError(409, 'driver_offline', 'off'))).toEqual({ kind: 'offline' })
    expect(classifyAcceptError(new ApiError(403, 'driver_not_approved', 'no'))).toEqual({ kind: 'not_approved' })
  })

  it('anything else is shown as an error', () => {
    const net = new ApiError(0, 'network_error', 'down')
    expect(classifyAcceptError(net)).toEqual({ kind: 'error', error: net })
    const plain = new Error('boom')
    expect(classifyAcceptError(plain)).toEqual({ kind: 'error', error: plain })
  })
})

describe('suggestedTopUpAmount', () => {
  it('covers the shortfall but never goes below the minimum top-up', () => {
    expect(suggestedTopUpAmount(300, 120, 500)).toBe(500)
    expect(suggestedTopUpAmount(3000, 500, 500)).toBe(2500)
    expect(suggestedTopUpAmount(300, null, null)).toBe(300)
    expect(suggestedTopUpAmount(300, -50, null)).toBe(300)
  })

  it('falls back to the minimum when amounts are unknown or already covered', () => {
    expect(suggestedTopUpAmount(null, 100, 500)).toBe(500)
    expect(suggestedTopUpAmount(100, 200, 500)).toBe(500)
    expect(suggestedTopUpAmount(null, null, null)).toBeNull()
  })
})
