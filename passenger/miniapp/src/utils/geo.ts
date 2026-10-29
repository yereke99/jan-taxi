// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Geometry helpers. The server is authoritative for the service area
// (PostGIS polygon, ARCHITECTURE §3); the bbox check here only gives early UI
// feedback and must never be used to accept a point.

/** WGS84 coordinate in the API's shape. */
export interface LatLon {
  lat: number
  lon: number
}

/** [[west, south], [east, north]] in lon/lat order, as MapLibre expects. */
export type LngLatBoundsTuple = [[number, number], [number, number]]

/** Kazakhstan's bounding box (Natural Earth 1:10m incl. Baikonur). */
export const KZ_BBOX = { west: 46.47828, south: 40.58466, east: 87.3238, north: 55.43455 } as const

/** Map pan limit: the KZ bbox plus about one degree of margin. */
export const KZ_MAX_BOUNDS: LngLatBoundsTuple = [
  [45.5, 39.8],
  [88.3, 56.2],
]

const EARTH_RADIUS_M = 6_371_008.8

function toRad(deg: number): number {
  return (deg * Math.PI) / 180
}

/** Great-circle distance in metres. */
export function haversineMeters(a: LatLon, b: LatLon): number {
  const dLat = toRad(b.lat - a.lat)
  const dLon = toRad(b.lon - a.lon)
  const s = Math.sin(dLat / 2) ** 2 + Math.cos(toRad(a.lat)) * Math.cos(toRad(b.lat)) * Math.sin(dLon / 2) ** 2
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(s)))
}

/** True when the value is a finite, in-range WGS84 coordinate (and not the 0,0 null island). */
export function isValidLatLon(p: Partial<LatLon> | null | undefined): p is LatLon {
  if (!p || typeof p.lat !== 'number' || typeof p.lon !== 'number') return false
  if (!Number.isFinite(p.lat) || !Number.isFinite(p.lon)) return false
  if (Math.abs(p.lat) > 90 || Math.abs(p.lon) > 180) return false
  return !(p.lat === 0 && p.lon === 0)
}

/** Quick client-side check against Kazakhstan's bounding box. */
export function isInKazakhstanBBox(p: LatLon): boolean {
  return p.lon >= KZ_BBOX.west && p.lon <= KZ_BBOX.east && p.lat >= KZ_BBOX.south && p.lat <= KZ_BBOX.north
}

/** Bounds of a set of points, or null for an empty set. */
export function boundsOf(points: readonly LatLon[]): LngLatBoundsTuple | null {
  if (points.length === 0) return null
  let west = Infinity
  let south = Infinity
  let east = -Infinity
  let north = -Infinity
  for (const p of points) {
    west = Math.min(west, p.lon)
    east = Math.max(east, p.lon)
    south = Math.min(south, p.lat)
    north = Math.max(north, p.lat)
  }
  return [
    [west, south],
    [east, north],
  ]
}

/** Rounds to the API's 6-decimal precision (~0.1 m). */
export function roundCoord(v: number): number {
  return Math.round(v * 1e6) / 1e6
}

/** Initial compass bearing from a to b in degrees (0 = north, clockwise). */
export function bearingDegrees(a: LatLon, b: LatLon): number {
  const y = Math.sin(toRad(b.lon - a.lon)) * Math.cos(toRad(b.lat))
  const x =
    Math.cos(toRad(a.lat)) * Math.sin(toRad(b.lat)) -
    Math.sin(toRad(a.lat)) * Math.cos(toRad(b.lat)) * Math.cos(toRad(b.lon - a.lon))
  return ((Math.atan2(y, x) * 180) / Math.PI + 360) % 360
}

/** Only real coordinate pairs are eligible for markers, distance and navigation. */
export function hasCoordinates<T extends { lat: number | null; lon: number | null }>(p: T): p is T & LatLon {
 return typeof p.lat === 'number' && Number.isFinite(p.lat) && typeof p.lon === 'number' && Number.isFinite(p.lon)
}
