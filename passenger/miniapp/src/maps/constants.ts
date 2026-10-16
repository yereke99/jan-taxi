// Shared with the other Mini App — keep both copies identical (make verify-shared).

import type { LatLon } from '../utils/geo'

/** Light vector style used until public config provides map.style_url (§14). */
export const DEFAULT_MAP_STYLE_URL = 'https://tiles.openfreemap.org/styles/positron'

/** Default map centre: Embi, the first service city (§3). Never used as a user location. */
export const DEFAULT_MAP_CENTER: Readonly<LatLon> = { lat: 48.8264, lon: 58.1442 }

export const DEFAULT_MAP_ZOOM = 13

/** Closest zoom used when fitting a route or centring on a point. */
export const FIT_MAX_ZOOM = 16

/** Stop marker letters: pickup A, up to three intermediate stops, destination. */
export const STOP_LETTERS = ['A', 'B', 'C', 'D', 'E'] as const

/** Route styling per §15: black line on a white casing; approach dashed gray. */
export const ROUTE_STYLE = {
  casingColor: '#FFFFFF',
  casingWidth: 8,
  lineColor: '#000000',
  lineWidth: 5,
  approachColor: '#8E8E93',
  approachWidth: 4,
  approachDash: [1.5, 1.5] as [number, number],
} as const
