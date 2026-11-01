// Small geodesy helpers for operator screens (display only; the API decides dispatch).

import type { LatLon } from '../api/common'

const EARTH_RADIUS_M = 6_371_008.8
const RAD = Math.PI / 180

/** Great-circle distance in metres (haversine). */
export function distanceMeters(a: LatLon, b: LatLon): number {
  const rad = Math.PI / 180
  const dLat = (b.lat - a.lat) * rad
  const dLon = (b.lon - a.lon) * rad
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLon / 2) ** 2
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(h)))
}

/** Point reached from `from` after distanceM along a great circle with the given initial bearing (degrees clockwise from north). */
export function destinationPoint(from: LatLon, distanceM: number, bearingDeg: number): LatLon {
  const d = distanceM / EARTH_RADIUS_M
  const theta = bearingDeg * RAD
  const phi1 = from.lat * RAD
  const lambda1 = from.lon * RAD
  const sinPhi2 = Math.sin(phi1) * Math.cos(d) + Math.cos(phi1) * Math.sin(d) * Math.cos(theta)
  const phi2 = Math.asin(Math.max(-1, Math.min(1, sinPhi2)))
  const lambda2 = lambda1 + Math.atan2(Math.sin(theta) * Math.sin(d) * Math.cos(phi1), Math.cos(d) - Math.sin(phi1) * sinPhi2)
  return { lat: phi2 / RAD, lon: ((lambda2 / RAD + 540) % 360) - 180 }
}

/** Initial bearing from a to b in degrees clockwise from north, 0 ≤ bearing < 360. */
export function bearingDegrees(a: LatLon, b: LatLon): number {
  const phi1 = a.lat * RAD
  const phi2 = b.lat * RAD
  const dLambda = (b.lon - a.lon) * RAD
  const y = Math.sin(dLambda) * Math.cos(phi2)
  const x = Math.cos(phi1) * Math.sin(phi2) - Math.sin(phi1) * Math.cos(phi2) * Math.cos(dLambda)
  return (Math.atan2(y, x) / RAD + 360) % 360
}
