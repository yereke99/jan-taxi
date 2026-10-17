// Small geodesy helpers for operator screens (display only; the API decides dispatch).

import type { LatLon } from '../api/common'

const EARTH_RADIUS_M = 6_371_008.8

/** Great-circle distance in metres (haversine). */
export function distanceMeters(a: LatLon, b: LatLon): number {
  const rad = Math.PI / 180
  const dLat = (b.lat - a.lat) * rad
  const dLon = (b.lon - a.lon) * rad
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLon / 2) ** 2
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(h)))
}
