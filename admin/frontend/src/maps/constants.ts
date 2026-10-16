// Map defaults shared by the admin map screens (§14).

import type { LatLon } from '../api/common'

/** Light vector style (OpenFreeMap Positron); public config may override it. */
export const DEFAULT_MAP_STYLE_URL = 'https://tiles.openfreemap.org/styles/positron'

/** Default centre: Embi, the first service city (§3). */
export const DEFAULT_MAP_CENTER: Readonly<LatLon> = { lat: 48.8264, lon: 58.1442 }
export const DEFAULT_MAP_ZOOM = 12

/** Pan limit: Kazakhstan's bounding box plus about one degree. */
export const KZ_MAX_BOUNDS: [[number, number], [number, number]] = [
  [45.5, 39.8],
  [88.3, 56.2],
]

/** Kazakhstan bounding box (rough client check; the API decides with the real boundary). */
export const KZ_BBOX = { west: 46.47828, south: 40.58466, east: 87.3238, north: 55.43455 } as const

export const STOP_LETTERS = ['A', 'B', 'C', 'D', 'E'] as const

export function inKazakhstanBBox(p: LatLon): boolean {
  return p.lon >= KZ_BBOX.west && p.lon <= KZ_BBOX.east && p.lat >= KZ_BBOX.south && p.lat <= KZ_BBOX.north
}

/** [[west, south], [east, north]] of the points, or null for none. */
export function boundsOf(points: readonly LatLon[]): [[number, number], [number, number]] | null {
  if (points.length === 0) return null
  let w = Infinity
  let s = Infinity
  let e = -Infinity
  let n = -Infinity
  for (const p of points) {
    w = Math.min(w, p.lon)
    e = Math.max(e, p.lon)
    s = Math.min(s, p.lat)
    n = Math.max(n, p.lat)
  }
  return [
    [w, s],
    [e, n],
  ]
}
