// Route editor model: 2..maxPoints stops, pickup first and destination last
// with up to three intermediate stops between them (§19 /routes/preview).
// Pure functions over immutable arrays so the store stays thin and the rules
// are unit-tested. Stops are never re-ordered automatically: only an explicit
// move of an intermediate stop changes the order.

import { isUnnamedPlace } from '../utils/text'
import type { StopInput } from '../api/types'
import { hasCoordinates, roundCoord, type LatLon } from '../utils/geo'

export type StopRole = 'pickup' | 'intermediate' | 'destination'

export const MIN_ROUTE_POINTS = 2
export const MAX_ROUTE_POINTS = 5

/** A chosen point: address from search, reverse geocoding or coordinates. */
export interface PlaceValue {
  lat: number | null
  lon: number | null
  address: string
  /** Locality line shown under the address. */
  subtitle: string | null
  /** 'place', a geocoder name, 'coordinates' or 'gps'. */
  source: string
  /** false when the API reported the point outside Kazakhstan; null if unknown. */
  inServiceArea: boolean | null
}

export interface RouteStop {
  /** Stable identity for list rendering and drag-and-drop. */
  key: string
  role: StopRole
  place: PlaceValue | null
}

export type KeyFactory = () => string

let counter = 0
/** Default key factory: unique within the page lifetime. */
export const nextStopKey: KeyFactory = () => `stop-${++counter}`

export function clampMaxPoints(max: number): number {
  return Number.isInteger(max) ? Math.min(MAX_ROUTE_POINTS, Math.max(MIN_ROUTE_POINTS, max)) : MAX_ROUTE_POINTS
}

/** Role of position `index` in a route of `count` stops. */
export function roleAt(index: number, count: number): StopRole {
  if (index === 0) return 'pickup'
  return index === count - 1 ? 'destination' : 'intermediate'
}

function withRoles(stops: RouteStop[]): RouteStop[] {
  return stops.map((s, i) => (s.role === roleAt(i, stops.length) ? s : { ...s, role: roleAt(i, stops.length) }))
}

/** Empty pickup + destination. */
export function createStops(key: KeyFactory = nextStopKey): RouteStop[] {
  return [
    { key: key(), role: 'pickup', place: null },
    { key: key(), role: 'destination', place: null },
  ]
}

export function isIntermediate(stops: readonly RouteStop[], index: number): boolean {
  return index > 0 && index < stops.length - 1
}

export function canAddStop(stops: readonly RouteStop[], maxPoints: number): boolean {
  return stops.length < clampMaxPoints(maxPoints)
}

/**
 * Adds an empty intermediate stop just before the destination. Returns the
 * same array when the route is already at the maximum.
 */
export function addStop(stops: RouteStop[], maxPoints: number, key: KeyFactory = nextStopKey): RouteStop[] {
  if (!canAddStop(stops, maxPoints)) return stops
  const next = [...stops]
  next.splice(next.length - 1, 0, { key: key(), role: 'intermediate', place: null })
  return withRoles(next)
}

/** Removes an intermediate stop; pickup and destination cannot be removed. */
export function removeStop(stops: RouteStop[], index: number): RouteStop[] {
  if (!isIntermediate(stops, index)) return stops
  return withRoles(stops.filter((_, i) => i !== index))
}

/** Drops intermediate stops that never got a place (the passenger backed out of the search). */
export function removeEmptyStops(stops: RouteStop[]): RouteStop[] {
  const keep = stops.filter((s, i) => s.place !== null || !isIntermediate(stops, i))
  return keep.length === stops.length ? stops : withRoles(keep)
}

/**
 * Moves an intermediate stop to another intermediate position. Pickup and
 * destination keep their fixed roles; anything else returns the same array.
 */
export function moveStop(stops: RouteStop[], from: number, to: number): RouteStop[] {
  if (from === to || !isIntermediate(stops, from) || !isIntermediate(stops, to)) return stops
  const next = [...stops]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved!)
  return withRoles(next)
}

export function setPlace(stops: RouteStop[], index: number, place: PlaceValue | null): RouteStop[] {
  if (index < 0 || index >= stops.length) return stops
  return stops.map((s, i) => (i === index ? { ...s, place } : s))
}

/**
 * Brings a route in line with the rules without changing the order of the
 * stops that remain: at least pickup + destination, at most maxPoints (extra
 * intermediate stops are dropped from the end of the intermediate run), roles
 * by position.
 */
export function normalizeStops(stops: readonly RouteStop[], maxPoints: number, key: KeyFactory = nextStopKey): RouteStop[] {
  const max = clampMaxPoints(maxPoints)
  let next = [...stops]
  while (next.length < MIN_ROUTE_POINTS) next.push({ key: key(), role: 'destination', place: null })
  if (next.length > max) next = [...next.slice(0, max - 1), next[next.length - 1]!]
  return withRoles(next)
}

/** Builds stops from a finished order ("order again"), keeping their order. */
export function stopsFromPlaces(places: readonly PlaceValue[], maxPoints: number, key: KeyFactory = nextStopKey): RouteStop[] {
  return normalizeStops(
    places.map((p) => ({ key: key(), role: 'intermediate' as const, place: p })),
    maxPoints,
    key,
  )
}

export function isComplete(stops: readonly RouteStop[]): boolean {
  return stops.length >= MIN_ROUTE_POINTS && stops.every((s, i) => s.place !== null && (hasCoordinates(s.place) || (i === stops.length - 1 && s.place.source === 'manual' && s.place.address.trim().length > 0)))
}

/** Indices of stops the API reported outside Kazakhstan. */
export function outsideIndices(stops: readonly RouteStop[]): number[] {
  return stops.flatMap((s, i) => (s.place?.inServiceArea === false ? [i] : []))
}

/**
 * The API request stops in the user's order, or null while incomplete. A
 * point without a name is sent as `unnamed` ("Point on the map"), never as
 * coordinates text.
 */
export function toStopInputs(stops: readonly RouteStop[], unnamed = ''): StopInput[] | null {
  if (!isComplete(stops)) return null
  return stops.map((s) => {
    const address = s.place!.source === 'manual' ? s.place!.address.normalize('NFC').trim() : s.place!.address.trim().slice(0, 200)
    return {
      ...(s.place!.source === 'manual' ? {mode: 'manual' as const} : {}),
      lat: s.place!.lat === null ? null : roundCoord(s.place!.lat),
      lon: s.place!.lon === null ? null : roundCoord(s.place!.lon),
      address: s.place!.source === 'manual' ? address : isUnnamedPlace(address) ? unnamed || address : address,
    }
  })
}

/** Identity of the routable part of a route (changes trigger a new quote). */
export function routeFingerprint(stops: readonly RouteStop[]): string {
  return stops
    .map((s) => (s.place ? `${s.place.source},${s.place.lat === null ? 'null' : roundCoord(s.place.lat)},${s.place.lon === null ? 'null' : roundCoord(s.place.lon)},${s.place.address}` : '-'))
    .join('|')
}

/** "48.826400, 58.144200" — for logs and accessibility hints, never shown as an address. */
export function formatCoordinates(p: LatLon): string {
  return `${p.lat.toFixed(6)}, ${p.lon.toFixed(6)}`
}
