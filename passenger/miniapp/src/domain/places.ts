// Recent places for quick selection, derived from the passenger's own order
// history (newest first). Nothing is stored on the device.

import type { OrderStop, OrderSummary, StopSummary } from '../api/types'
import { haversineMeters } from '../utils/geo'
import type { PlaceValue } from './route'

/** Two history points closer than this are the same place. */
const SAME_PLACE_M = 60

function toPlace(s: StopSummary | OrderStop, source: string): PlaceValue {
  return { lat: s.lat, lon: s.lon, address: s.address, subtitle: null, source, inServiceArea: true }
}

function pushUnique(out: PlaceValue[], p: PlaceValue): void {
  if (!p.address) return
  const dup = out.some((q) => q.address === p.address || haversineMeters(q, p) < SAME_PLACE_M)
  if (!dup) out.push(p)
}

/** Most recent destinations, newest first, without near-duplicates. */
export function recentDestinations(history: readonly OrderSummary[], limit = 3): PlaceValue[] {
  const out: PlaceValue[] = []
  for (const o of history) {
    pushUnique(out, toPlace(o.destination, 'history'))
    if (out.length >= limit) break
  }
  return out
}

/** Recent destinations and pickups for the search screen. */
export function recentPlaces(history: readonly OrderSummary[], limit = 6): PlaceValue[] {
  const out: PlaceValue[] = []
  for (const o of history) {
    pushUnique(out, toPlace(o.destination, 'history'))
    if (out.length >= limit) break
    pushUnique(out, toPlace(o.pickup, 'history'))
    if (out.length >= limit) break
  }
  return out
}

/** Places of an order's stops, in order ("order again"). */
export function placesOfStops(stops: readonly OrderStop[]): PlaceValue[] {
  return [...stops].sort((a, b) => a.seq - b.seq).map((s) => toPlace(s, 'order'))
}
