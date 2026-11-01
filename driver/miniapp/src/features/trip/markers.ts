// Stop markers of an order map (TripMap). Only stops with coordinates get a
// marker; a manual stop (the passenger's words, no map point) is skipped, and
// every marker keeps the letter of its seq, as in the stop list: A, a manual B
// and C show markers A and C, never A and B.

import type { Stop } from '../../api/types'
import type { StopMarkerPoint } from '../../maps'
import { hasCoordinates } from '../../utils/geo'
import { sortedStops, stopLetter } from './sequence'

/** Markers of the stops with coordinates in seq order; `live` greys completed stops. */
export function stopMarkerPoints(stops: readonly Stop[], live = false): StopMarkerPoint[] {
  return sortedStops(stops)
    .filter(hasCoordinates)
    .map((s) => ({ lat: s.lat, lon: s.lon, done: live ? s.completed_at !== null : false, letter: stopLetter(s.seq) }))
}
