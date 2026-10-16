// Presentation rules for an order: headline per status, cancel reason,
// map markers (completed stops grey, pulsing pickup while searching) and the
// points the map should frame in each phase.

import type { Order } from '../api/types'
import { STOP_LETTERS } from '../maps/constants'
import type { StopMarkerPoint } from '../maps/useStopMarkers'
import type { LatLon } from '../utils/geo'
import { isActiveOrderStatus } from '../utils/status'

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
  /** Free-text reason given by a person. */
  text: string | null
}

export function cancelInfo(o: Order): CancelInfo {
  const by = o.cancelled_by
  const reason = o.cancel_reason
  if (by === 'system' && reason === 'no_driver_found') return { byKey: null, reasonKey: 'order.reason_no_driver_found', text: null }
  return {
    byKey: by ? `order.reason_by_${by}` : null,
    reasonKey: null,
    text: reason && by !== 'system' ? reason : null,
  }
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
