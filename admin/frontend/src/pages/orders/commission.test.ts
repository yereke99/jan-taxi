import { describe, expect, it } from 'vitest'
import type { OrderLedgerRow } from '../../api/orders'
import { chargedCommission } from './commission'

function row(id: number, driver: string, type: string, delta: number): OrderLedgerRow {
  return {
    id,
    driver_id: driver,
    entry_type: type,
    balance_delta: delta,
    reserved_delta: 0,
    balance_after: 0,
    reserved_after: 0,
    currency: 'KZT',
    order_id: 'o1',
    order_number: 1,
    topup_id: null,
    topup_reference: null,
    description: null,
    created_at: '2026-10-19T08:00:00Z',
  }
}

describe('chargedCommission', () => {
  it('sums the accept-time charge of the given driver', () => {
    expect(chargedCommission([row(1, 'd1', 'commission_charge', -50)], 'd1')).toBe(50)
  })

  it('ignores other drivers and subtracts refunds', () => {
    const ledger = [row(1, 'd1', 'commission_charge', -50), row(2, 'd1', 'commission_refund', 50), row(3, 'd2', 'commission_charge', -70)]
    expect(chargedCommission(ledger, 'd1')).toBe(0)
    expect(chargedCommission(ledger, 'd2')).toBe(70)
    expect(chargedCommission(ledger, null)).toBe(0)
  })
})
