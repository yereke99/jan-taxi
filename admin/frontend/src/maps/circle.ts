// Geodesic circles of village commission areas. Every vertex lies exactly
// radius metres (great circle) from the centre, so the drawn circle is the
// one the API checks pickup A against, at any latitude of Kazakhstan.
// Coordinates are [lon, lat] as in GeoJSON.

import type { LatLon } from '../api/common'
import type { PolygonGeometry, Position } from '../api/config'
import { destinationPoint } from '../utils/geo'

export const CIRCLE_STEPS = 64

/** Closed counter-clockwise ring (the first vertex repeated at the end), the first vertex due north. */
export function geodesicRing(center: LatLon, radiusM: number, steps = CIRCLE_STEPS): Position[] {
  const n = Math.max(8, Math.round(steps))
  const ring: Position[] = []
  for (let i = 0; i < n; i++) {
    // North, then west, south and east: counter-clockwise on the map (RFC 7946 outer ring).
    const p = destinationPoint(center, radiusM, (360 - (i * 360) / n) % 360)
    ring.push([p.lon, p.lat])
  }
  ring.push([ring[0]![0], ring[0]![1]])
  return ring
}

/** GeoJSON polygon of the circle for map sources. */
export function geodesicCircle(center: LatLon, radiusM: number, steps = CIRCLE_STEPS): PolygonGeometry {
  return { type: 'Polygon', coordinates: [geodesicRing(center, radiusM, steps)] }
}

/** [[west, south], [east, north]] around the circle. */
export function circleBounds(center: LatLon, radiusM: number): [[number, number], [number, number]] {
  let w = Infinity
  let s = Infinity
  let e = -Infinity
  let n = -Infinity
  for (const [lon, lat] of geodesicRing(center, radiusM)) {
    w = Math.min(w, lon)
    e = Math.max(e, lon)
    s = Math.min(s, lat)
    n = Math.max(n, lat)
  }
  return [
    [w, s],
    [e, n],
  ]
}
