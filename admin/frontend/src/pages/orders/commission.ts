// Commission a driver has paid for an order (§22.2): charges at accept minus
// refunds, from the order's ledger rows.

import type { OrderLedgerRow } from '../../api/orders'

/** Net commission driverId paid for the order (0 when never charged or already refunded). */
export function chargedCommission(ledger: OrderLedgerRow[], driverId: string | null | undefined): number {
  if (!driverId) return 0
  let net = 0
  for (const row of ledger) {
    if (row.driver_id !== driverId) continue
    if (row.entry_type === 'commission_charge' || row.entry_type === 'commission_refund') net -= row.balance_delta
  }
  return Math.max(net, 0)
}
