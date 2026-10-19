// Whether the wallet can pay for the next order (§22.2). The commission is
// charged when the driver takes an order; with less available than that,
// dispatch stops sending offers until the driver tops up. In fixed mode the
// amount is known in advance (public config); in percent mode it depends on
// the fare, so only the server's per-order check applies.

import type { LedgerEntry, Wallet } from '../../api/types'
import { fixedCommission, type CommissionPolicy } from '../vehicle/classes'

export interface OrderFunding {
  /** Commission of the next order when known in advance (fixed mode). */
  commission: number | null
  /** True when the available balance cannot pay that commission: no offers come in. */
  paused: boolean
  /** How much is missing (0 when not paused). */
  shortfall: number
}

export function orderFunding(wallet: Pick<Wallet, 'available'> | null, policy: CommissionPolicy | null): OrderFunding {
  const commission = fixedCommission(policy)
  if (!wallet || commission === null) return { commission, paused: false, shortfall: 0 }
  const shortfall = Math.max(0, commission - wallet.available)
  return { commission, paused: shortfall > 0, shortfall }
}

/** The ledger description the API writes for the automatic welcome bonus (§22.1). */
const WELCOME_BONUS_DESCRIPTION = 'welcome bonus'

/** A bonus from Jan Taxi: the welcome bonus or one granted by an administrator. */
export function bonusKind(e: Pick<LedgerEntry, 'entry_type' | 'description'>): 'welcome' | 'manual' | null {
  if (e.entry_type !== 'bonus_credit') return null
  return e.description?.trim().toLowerCase() === WELCOME_BONUS_DESCRIPTION ? 'welcome' : 'manual'
}
