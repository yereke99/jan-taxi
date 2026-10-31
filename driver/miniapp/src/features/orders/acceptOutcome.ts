// Maps a failed POST /driver/orders/{id}/accept to what the screen does next
// (openapi acceptOrder: 402 insufficient_balance, 409 order_already_taken |
// driver_has_active_order | driver_offline | invalid_transition, 422
// location_required (reason location_required | location_stale), 403, 404 —
// taken, or no longer within the driver's dispatch radius).

import { isApiError } from '../../services/http'

export type AcceptFailure =
  /** Another driver won (409 order_already_taken): toast, drop it from the list. */
  | { kind: 'taken' }
  /** No longer within the driver's reach or vehicle (404): toast, drop it. */
  | { kind: 'unavailable' }
  /** Closed without a driver — cancelled or expired (invalid_transition): toast, drop it. */
  | { kind: 'closed' }
  /** Not enough available balance: open the top-up screen with this amount. */
  | { kind: 'insufficient_balance'; required: number | null; available: number | null; suggestedTopUp: number | null }
  /** The driver already has an active order: open the trip. */
  | { kind: 'has_active_order'; activeOrderId: string | null }
  /** The driver must go online first. */
  | { kind: 'offline' }
  /** No recent location (dispatch is by distance): ask to enable location. */
  | { kind: 'location_required'; stale: boolean }
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
      return { kind: 'taken' }
    case 'not_found':
      return { kind: 'unavailable' }
    case 'invalid_transition':
      return { kind: 'closed' }
    case 'insufficient_balance': {
      const required = int(d.required)
      const available = int(d.available)
      return { kind: 'insufficient_balance', required, available, suggestedTopUp: suggestedTopUpAmount(required, available, int(d.topup_min_amount)) }
    }
    case 'driver_has_active_order':
      return { kind: 'has_active_order', activeOrderId: typeof d.active_order_id === 'string' ? d.active_order_id : null }
    case 'driver_offline':
      return { kind: 'offline' }
    case 'location_required':
    case 'location_stale':
      return { kind: 'location_required', stale: err.code === 'location_stale' || d.reason === 'location_stale' }
    case 'driver_not_approved':
    case 'driver_not_registered':
      return { kind: 'not_approved' }
    default:
      return { kind: 'error', error: err }
  }
}
