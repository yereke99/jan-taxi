// Maps a failed POST /driver/orders/{id}/accept to what the screen does next
// (openapi acceptOrder: 402 insufficient_balance, 409 order_already_taken |
// driver_has_active_order | driver_offline | invalid_transition, 403, 404).

import { isApiError } from '../../services/http'

export type AcceptFailure =
  /** Another driver won, or the order is gone: toast, drop it from the list. */
  | { kind: 'taken' }
  /** Not enough available balance: open the top-up screen with this amount. */
  | { kind: 'insufficient_balance'; required: number | null; available: number | null; suggestedTopUp: number | null }
  /** The driver already has an active order: open the trip. */
  | { kind: 'has_active_order'; activeOrderId: string | null }
  /** The driver must go online first. */
  | { kind: 'offline' }
  /** The profile is not approved (or suspended): re-run entry routing. */
  | { kind: 'not_approved' }
  /** Anything else: show the localized error. */
  | { kind: 'error'; error: unknown }

function int(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? Math.round(v) : null
}

/**
 * Smallest top-up that makes the order acceptable, never below the minimum
 * top-up (details.topup_min_amount). Null when the amounts are unknown.
 */
export function suggestedTopUpAmount(required: number | null, available: number | null, topupMin: number | null): number | null {
  if (required === null) return topupMin
  const shortfall = Math.max(0, required - Math.max(0, available ?? 0))
  if (shortfall === 0) return topupMin
  return Math.max(shortfall, topupMin ?? 0)
}

export function classifyAcceptError(err: unknown): AcceptFailure {
  if (!isApiError(err)) return { kind: 'error', error: err }
  const d = err.details
  switch (err.code) {
    case 'order_already_taken':
    case 'not_found':
    case 'invalid_transition':
      return { kind: 'taken' }
    case 'insufficient_balance': {
      const required = int(d.required)
      const available = int(d.available)
      return { kind: 'insufficient_balance', required, available, suggestedTopUp: suggestedTopUpAmount(required, available, int(d.topup_min_amount)) }
    }
    case 'driver_has_active_order':
      return { kind: 'has_active_order', activeOrderId: typeof d.active_order_id === 'string' ? d.active_order_id : null }
    case 'driver_offline':
      return { kind: 'offline' }
    case 'driver_not_approved':
    case 'driver_not_registered':
      return { kind: 'not_approved' }
    default:
      return { kind: 'error', error: err }
  }
}
