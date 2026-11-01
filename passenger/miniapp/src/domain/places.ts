// Recent places for quick selection, derived from the passenger's own order
// history (newest first). Nothing is stored on the device.

import type { OrderStop, OrderSummary, StopSummary } from '../api/types'
import { hasCoordinates, haversineMeters } from '../utils/geo'
import type { PlaceValue } from './route'

/** Two history points closer than this are the same place. */
const SAME_PLACE_M = 60

/**
 * A pickup the passenger described in their own words (address_typed) comes
 * back typed, so editing it starts from that text; a manual stop comes back
 * without coordinates.
 */
function toPlace(s: StopSummary | OrderStop, source: string): PlaceValue {
  const manual = s.mode === 'manual'
  return {
    lat: s.lat,
    lon: s.lon,
    address: s.address,
    subtitle: null,
    source: manual ? 'manual' : source,
    inServiceArea: manual ? null : true,
    ...(!manual && s.address_typed === true ? { typed: true } : {}),
  }
}

function pushUnique(out: PlaceValue[], p: PlaceValue, manual: boolean): void {
  if (!p.address || (!manual && p.source === 'manual')) return
  const dup = out.some((q) => q.address === p.address || (hasCoordinates(q) && hasCoordinates(p) && haversineMeters(q, p) < SAME_PLACE_M))
  if (!dup) out.push(p)
}

/**
 * Most recent destinations, newest first, without near-duplicates. Manual
 * places (no map point) only while manual stops are switched on.
 */
export function recentDestinations(history: readonly OrderSummary[], limit = 3, manual = true): PlaceValue[] {
  const out: PlaceValue[] = []
  for (const o of history) {
    pushUnique(out, toPlace(o.destination, 'history'), manual)
    if (out.length >= limit) break
  }
  return out
}

/** Recent destinations and pickups for the search screen (manual ones as in recentDestinations). */
export function recentPlaces(history: readonly OrderSummary[], limit = 6, manual = true): PlaceValue[] {
  const out: PlaceValue[] = []
  for (const o of history) {
    pushUnique(out, toPlace(o.destination, 'history'), manual)
    if (out.length >= limit) break
    pushUnique(out, toPlace(o.pickup, 'history'), manual)
    if (out.length >= limit) break
  }
  return out
}

/** List key of a recent place; addresses are unique in the lists, coordinates are not (manual places have none). */
export function placeKey(p: PlaceValue): string {
  return `${p.lat},${p.lon},${p.address}`
}

/** Places of an order's stops, in order ("order again"). */
export function placesOfStops(stops: readonly OrderStop[]): PlaceValue[] {
  return [...stops].sort((a, b) => a.seq - b.seq).map((s) => toPlace(s, 'order'))
}
