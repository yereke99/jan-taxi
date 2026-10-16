// GET /geo/search and /geo/reverse (§19). Both accept an AbortSignal so the
// search box and the map pin can cancel superseded requests.

import { http } from '../services/http'
import type { Locale } from '../utils/format'
import { roundCoord, type LatLon } from '../utils/geo'
import { parseGeoPlaces, parseGeoReverse } from './parse'
import type { GeoPlace, GeoReverse } from './types'

/** Minimum query length in code points (the API rejects shorter queries). */
export const GEO_SEARCH_MIN_CHARS = 2

export async function searchPlaces(q: string, opts: { near?: LatLon | null; lang: Locale; signal?: AbortSignal }): Promise<GeoPlace[]> {
  const near = opts.near ?? null
  const raw = await http.get<unknown>('/geo/search', {
    query: {
      q: q.trim(),
      lat: near ? roundCoord(near.lat) : undefined,
      lon: near ? roundCoord(near.lon) : undefined,
      lang: opts.lang,
    },
    signal: opts.signal,
  })
  return parseGeoPlaces(raw)
}

export async function reverseGeocode(p: LatLon, opts: { lang: Locale; signal?: AbortSignal }): Promise<GeoReverse> {
  const raw = await http.get<unknown>('/geo/reverse', {
    query: { lat: roundCoord(p.lat), lon: roundCoord(p.lon), lang: opts.lang },
    signal: opts.signal,
  })
  return parseGeoReverse(raw)
}
