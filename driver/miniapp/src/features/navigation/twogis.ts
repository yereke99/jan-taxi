// 2GIS navigation links for the driver (driver app only; not part of the
// shared core). Rules from the 2GIS deep-link documentation:
//
//   https://<base>/directions/tab/car/points/<A>|<via…>|<B>
//   point = "<lon>,<lat>" (longitude first, dot decimal separator)
//   empty A ("points/|<B>") = start from the user's current location
//
// Only https links are used: Telegram blocks custom schemes (dgis://) in
// openLink, and an https link opens the 2GIS app when installed and the web
// version otherwise. Intermediate points are documented for the Android app
// only and the web version accepts at most 8, so:
//   * "full" — driver GPS → every remaining stop (≤ 8 via points);
//              Android, desktop and web;
//   * "next" — current location (empty A) → next uncompleted stop; iOS, and
//              whenever the full route cannot be passed (no GPS, > 8 via points).
// The full route always stays visible in Jan Taxi.

export interface NavPoint {
  lat: number
  lon: number
}

export type TwoGisMode = 'full' | 'next'

/** Why the full route was not handed to 2GIS. */
export type TwoGisNote = 'ios' | 'too_many_points' | 'no_gps' | null

export const DEFAULT_TWOGIS_BASE = 'https://2gis.ru'

/** Hosts allowed as the link base (settings navigation.twogis_base_url). */
const ALLOWED_HOSTS = new Set(['2gis.ru', '2gis.kz', '2gis.com'])

/** Web version limit for intermediate points. */
export const MAX_VIA_POINTS = 8

/** "lon,lat" with 6 decimals and a dot, independent of the UI locale. */
export function formatPoint(p: NavPoint): string {
  return `${p.lon.toFixed(6)},${p.lat.toFixed(6)}`
}

function validPoint(p: NavPoint | null | undefined): p is NavPoint {
  return (
    !!p &&
    Number.isFinite(p.lat) &&
    Number.isFinite(p.lon) &&
    Math.abs(p.lat) <= 90 &&
    Math.abs(p.lon) <= 180 &&
    !(p.lat === 0 && p.lon === 0)
  )
}

/** https origin of an allowed 2GIS host; anything else falls back to the default. */
export function normalizeTwoGisBase(base: string | null | undefined): string {
  if (!base) return DEFAULT_TWOGIS_BASE
  try {
    const u = new URL(base)
    if (u.protocol !== 'https:' || !ALLOWED_HOSTS.has(u.hostname)) return DEFAULT_TWOGIS_BASE
    return u.origin
  } catch {
    return DEFAULT_TWOGIS_BASE
  }
}

/** Telegram platform ids whose 2GIS app is documented to accept via points (or web). */
export function preferredMode(platform: string): TwoGisMode {
  return platform.toLowerCase() === 'ios' ? 'next' : 'full'
}

/** Route preview from the current location (empty A) to `to`. */
export function nextStopUrl(base: string, to: NavPoint): string {
  if (!validPoint(to)) throw new RangeError('invalid destination')
  return `${normalizeTwoGisBase(base)}/directions/tab/car/points/|${formatPoint(to)}`
}

/** Route from `origin` through every stop; `stops` ends with the final destination. */
export function fullRouteUrl(base: string, origin: NavPoint, stops: readonly NavPoint[]): string {
  if (!validPoint(origin) || stops.length === 0 || !stops.every(validPoint)) throw new RangeError('invalid route points')
  if (stops.length - 1 > MAX_VIA_POINTS) throw new RangeError('too many via points')
  return `${normalizeTwoGisBase(base)}/directions/tab/car/points/${[origin, ...stops].map(formatPoint).join('|')}`
}

export interface NavigationPlan {
  /** Link for the main "Open in 2GIS" button. */
  primaryUrl: string
  primaryMode: TwoGisMode
  /** Link for the always-offered "Next stop only" button. */
  nextUrl: string
  /** Set when the full route could not be passed; the UI explains it. */
  note: TwoGisNote
}

/**
 * Plans the 2GIS links for the remaining stops (next uncompleted stop first,
 * final destination last). Returns null when there is nothing to navigate to.
 */
export function planNavigation(input: {
  base: string
  platform: string
  /** Current driver position; null without GPS. */
  origin: NavPoint | null
  remaining: readonly NavPoint[]
}): NavigationPlan | null {
  const stops = input.remaining.filter(validPoint)
  const next = stops[0]
  if (!next || stops.length !== input.remaining.length) return null
  const nextUrl = nextStopUrl(input.base, next)
  const result = (note: TwoGisNote): NavigationPlan => ({ primaryUrl: nextUrl, primaryMode: 'next', nextUrl, note })

  if (preferredMode(input.platform) === 'next') return result(stops.length > 1 ? 'ios' : null)
  if (!validPoint(input.origin)) return result(stops.length > 1 ? 'no_gps' : null)
  if (stops.length - 1 > MAX_VIA_POINTS) return result('too_many_points')
  return { primaryUrl: fullRouteUrl(input.base, input.origin, stops), primaryMode: 'full', nextUrl, note: null }
}

/** A manual landmark has no navigation coordinates. Preserve the known
 * prefix (especially pickup A), but never skip a malformed real waypoint. */
export function navigationPrefix(stops: readonly { mode?: string; lat: number | null; lon: number | null }[]): NavPoint[] {
  const points: NavPoint[] = []
  for (let i = 0; i < stops.length; i++) {
    const stop = stops[i]!
    if (stop.mode === 'manual') {
      if (i === 0 || stop.lat !== null || stop.lon !== null) return []
      break
    }
    if (typeof stop.lat !== 'number' || typeof stop.lon !== 'number') return []
    const point = { lat: stop.lat, lon: stop.lon }
    if (!validPoint(point)) return []
    points.push(point)
  }
  return points
}
