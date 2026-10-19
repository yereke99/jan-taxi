// Presentation rules for an order: headline per status, cancel reason,
// the driver's arrival estimate, map markers (completed stops grey, pulsing
// pickup while searching) and the points the map should frame in each phase.

import type { Order, OrderPlace } from '../api/types'
import { STOP_LETTERS } from '../maps/constants'
import { haversineMeters } from '../utils/geo'
import type { StopMarkerPoint } from '../maps/useStopMarkers'
import type { LatLon } from '../utils/geo'
import { isActiveOrderStatus } from '../utils/status'
import { isUnnamedPlace } from '../utils/text'

/**
 * The place name shown for an order: the settlement of pickup A from the API
 * ("Алматы"), else the pickup's locality line the passenger saw while
 * booking; null when neither is known. Never coordinates, and never the
 * tariff region (a nationwide tariff is not a place).
 */
export function orderLocality(o: Pick<OrderPlace, 'locality'>, pickupSubtitle?: string | null): string | null {
  for (const v of [o.locality, pickupSubtitle]) {
    const text = v?.trim() ?? ''
    if (!isUnnamedPlace(text)) return text
  }
  return null
}

export function isDeliveryOrder(o: Pick<Order, 'category'>): boolean {
  return o.category !== 'taxi'
}

/** Message key of the status headline. */
export function headlineKey(o: Order): string {
  const delivery = isDeliveryOrder(o)
  switch (o.status) {
    case 'searching':
      return 'order.searching_title'
    case 'accepted':
      return 'order.accepted_title'
    case 'driver_arriving':
      return 'order.arriving_title'
    case 'in_progress':
      return delivery ? 'order.in_progress_delivery_title' : 'order.in_progress_title'
    case 'completed':
      return delivery ? 'order.completed_delivery_title' : 'order.completed_title'
    case 'cancelled':
      return 'order.cancelled_title'
    case 'disputed':
      return 'order.disputed_title'
    default:
      return `order_status.${o.status}`
  }
}

/** The driver is assigned and the trip is not over. */
export function hasActiveDriver(o: Order): boolean {
  return o.driver !== null && (o.status === 'accepted' || o.status === 'driver_arriving' || o.status === 'in_progress')
}

export interface CancelInfo {
  /** Who cancelled (message key), null when unknown. */
  byKey: string | null
  /** Translated system reason (message key), e.g. no driver found. */
  reasonKey: string | null
  /** The passenger's chosen reason (message key of its label), shown as "Reason: …". */
  labelKey: string | null
  /** Free-text reason given by a person (the passenger's comment). */
  text: string | null
}

/** Reason codes the driver app and bot may send; shown in the passenger's language. */
export const DRIVER_CANCEL_CODES = ['passenger_no_show', 'sender_no_show', 'passenger_asked', 'cannot_reach', 'vehicle_problem', 'unsafe_cargo'] as const

export function cancelInfo(o: Order): CancelInfo {
  const by = o.cancelled_by
  const reason = o.cancel_reason
  if (by === 'system' && reason === 'no_driver_found') return { byKey: null, reasonKey: 'order.reason_no_driver_found', labelKey: null, text: null }
  const code = reason?.trim() ?? ''
  if (by === 'driver' && (DRIVER_CANCEL_CODES as readonly string[]).includes(code)) {
    return { byKey: 'order.reason_by_driver', reasonKey: `order.driver_reason.${code}`, labelKey: null, text: null }
  }
  return {
    byKey: by ? `order.reason_by_${by}` : null,
    reasonKey: null,
    // §22.3: the passenger's reason code, with the comment (if any) in cancel_reason.
    labelKey: by === 'passenger' && o.cancel_reason_code ? `cancel.reason.${o.cancel_reason_code}` : null,
    text: reason && by !== 'system' ? reason : null,
  }
}

/** An arrival estimate older than this is not trusted; the local estimate is used instead. */
export const ETA_MAX_AGE_MS = 5 * 60_000
/** Average town speed for the local arrival estimate, m/s (~25 km/h along streets). */
const TOWN_SPEED_MPS = 7
const ROAD_FACTOR = 1.3

/**
 * Minutes until the driver reaches pickup A while the order is accepted:
 * the API's road estimate (§22.6) less the time since it was computed, or a
 * straight-line estimate from the driver's last position; at least 1, null
 * when neither is known.
 */
export function arrivalMinutes(o: Order, now = Date.now()): number | null {
  if (o.status !== 'accepted') return null
  if (o.driver_eta_s !== null && o.driver_eta_s >= 0) {
    const at = Date.parse(o.driver_eta_at ?? '')
    const age = Number.isFinite(at) ? Math.max(0, now - at) : 0
    if (age <= ETA_MAX_AGE_MS) return Math.max(1, Math.ceil((o.driver_eta_s - age / 1000) / 60))
  }
  const loc = o.driver_location
  const pickup = o.stops[0]
  if (!loc || !pickup) return null
  return Math.max(1, Math.round((haversineMeters(loc, pickup) * ROAD_FACTOR) / TOWN_SPEED_MPS / 60))
}

/** Map markers for the order's stops. */
export function orderMarkers(o: Order): StopMarkerPoint[] {
  return o.stops.map((s, i) => ({
    lat: s.lat,
    lon: s.lon,
    letter: STOP_LETTERS[i],
    done: s.completed_at !== null && o.status === 'in_progress',
    pulse: o.status === 'searching' && i === 0,
  }))
}

/** The stop the driver heads to next, or null when no driver is on the way. */
export function nextStop(o: Order): LatLon | null {
  if (o.status === 'accepted' || o.status === 'driver_arriving') return o.stops[0] ?? null
  if (o.status === 'in_progress') return o.stops.find((s, i) => i > 0 && s.completed_at === null) ?? null
  return null
}

/** Points the map frames: the whole route, or the driver and where they go next. */
export function framingPoints(o: Order): LatLon[] {
  const driver = o.driver_location
  if (driver && isActiveOrderStatus(o.status) && o.status !== 'searching') {
    const rest =
      o.status === 'in_progress' ? o.stops.filter((s, i) => i > 0 && s.completed_at === null) : o.stops.slice(0, 1)
    return [driver, ...rest]
  }
  const pts: LatLon[] = [...o.stops]
  const b = o.route.bbox
  if (b) pts.push({ lon: b[0], lat: b[1] }, { lon: b[2], lat: b[3] })
  return pts
}
