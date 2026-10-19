// Vehicle classes on the passenger side (ARCHITECTURE §21): how many
// passengers a taxi order may ask for, the cargo sizes of a delivery, the
// option the passenger picked from a quote, and the preview errors that are
// about the load rather than the route.

import type { RouteQuote, RouteQuoteOption } from '../api/types'
import { CARGO_SIZE_CODES, DEFAULT_CARGO_SIZES, type CargoSize, type CargoSizeInfo, type PublicConfig } from '../services/config'
import { isApiError } from '../services/http'

export const MIN_PASSENGERS = 1
export const DEFAULT_CARGO_SIZE: CargoSize = 's'

/** Classes seeded by §21.1; the app has its own names for them as a fallback. */
export const KNOWN_VEHICLE_CLASSES = ['sedan', 'universal', 'minivan', 'microbus', 'cargo_van', 'truck'] as const

export function isCargoSize(v: unknown): v is CargoSize {
  return typeof v === 'string' && (CARGO_SIZE_CODES as readonly string[]).includes(v)
}

/**
 * Most passengers the stepper offers: the API limit, but never more seats than
 * the largest active passenger class has (when the config lists classes).
 */
export function maxPassengers(config: Pick<PublicConfig, 'max_passenger_count' | 'vehicle_classes'>): number {
  const limit = Math.max(MIN_PASSENGERS, config.max_passenger_count)
  const seats = config.vehicle_classes.filter((c) => c.passenger_capable && c.seats_max > 0).map((c) => c.seats_max)
  return seats.length > 0 ? Math.max(MIN_PASSENGERS, Math.min(limit, Math.max(...seats))) : limit
}

export function clampPassengers(n: number, max: number): number {
  if (!Number.isFinite(n)) return MIN_PASSENGERS
  return Math.min(Math.max(MIN_PASSENGERS, Math.round(n)), Math.max(MIN_PASSENGERS, max))
}

/** Volume in litres of a cargo size (config first, then the §21.2 defaults). */
export function cargoVolume(size: CargoSize, sizes: readonly CargoSizeInfo[] = DEFAULT_CARGO_SIZES): number {
  return sizes.find((s) => s.code === size)?.volume_l ?? DEFAULT_CARGO_SIZES.find((s) => s.code === size)?.volume_l ?? 0
}

/**
 * The option a booking starts with: the cheapest class able to carry the load
 * (lowest recommended fare; on a tie the API's display order). The API lists
 * the cheapest first, so this is normally options[0].
 */
export function defaultOption(q: RouteQuote | null): RouteQuoteOption | null {
  if (!q || q.options.length === 0) return null
  let best = q.options[0]!
  for (const o of q.options) if (o.fare.recommended < best.fare.recommended) best = o
  return best
}

/** The option for `vehicleClass`, or the default option when it is not offered (or none was tapped). */
export function pickOption(q: RouteQuote | null, vehicleClass: string | null): RouteQuoteOption | null {
  if (!q || q.options.length === 0) return null
  return (vehicleClass !== null ? q.options.find((o) => o.vehicle_class === vehicleClass) : undefined) ?? defaultOption(q)
}

/** A class at least this large is a "big" vehicle (minivan, minibus). */
export const LARGE_CLASS_SEATS = 7
/** A party this small fits a sedan. */
export const SMALL_PARTY = 4

/**
 * True when a taxi is about to be ordered in a much larger (and dearer)
 * vehicle than the passengers need, e.g. a minibus for one person: the
 * passenger confirms it in an action sheet first.
 */
export function oversizedForParty(category: string, passengerCount: number, option: Pick<RouteQuoteOption, 'seats_max'> | null): boolean {
  return category === 'taxi' && option !== null && option.seats_max >= LARGE_CLASS_SEATS && passengerCount <= SMALL_PARTY
}

/** Whole minutes (at least 1) for an ETA in seconds; null when unknown. */
export function etaMinutes(etaS: number | null | undefined): number | null {
  if (typeof etaS !== 'number' || !Number.isFinite(etaS) || etaS < 0) return null
  return Math.max(1, Math.ceil(etaS / 60))
}

/**
 * Why a preview failed because of the load (§21.3 422 fields):
 *   'passengers'   no active class seats that many passengers
 *   'cargo'        no active class fits the cargo size
 *   'unavailable'  classes exist but none is priced in this city
 * null for every other error (route, network…).
 */
export type LoadProblem = 'passengers' | 'cargo' | 'unavailable'

export function loadProblem(err: unknown): LoadProblem | null {
  if (!isApiError(err) || err.code !== 'validation_failed') return null
  const f = err.fields
  if (f.passenger_count) return 'passengers'
  if (f.cargo_size) return 'cargo'
  if (f.category === 'not_allowed') return 'unavailable'
  return null
}
