// Defensive parsers for API payloads. The API is the source of truth; these
// only make sure a missing optional field or an unexpected null degrades to a
// safe value instead of crashing a screen. Required identity fields (ids,
// status) are validated and a payload without them is rejected.

import { isUnnamedPlace } from '../utils/text'
import type { LineStringGeometry, Position } from '../maps'
import { CARGO_SIZE_CODES, localizedOrNull, VEHICLE_ICONS, type CargoSize } from '../services/config'
import { ApiError } from '../services/http'
import { CATEGORIES, ORDER_STATUSES, PAYMENT_METHODS, type Category, type OrderStatus, type PaymentMethod } from '../utils/status'
import { CANCEL_REASON_CODES } from './types'
import type {
  BBox,
  CityRef,
  CursorPage,
  DriverLocation,
  FareOptions,
  GeoPlace,
  GeoReverse,
  Order,
  OrderDispute,
  OrderDriver,
  OrderEvent,
  OrderLoad,
  OrderPlace,
  OrderRating,
  OrderStop,
  OrderSummary,
  QuoteStop,
  RouteLeg,
  RouteQuote,
  RouteQuoteOption,
  RouteSnapshot,
  StopKind,
  StopSummary,
  TariffRegion,
  Vehicle,
} from './types'

type Rec = Record<string, unknown>

export function rec(v: unknown): Rec {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Rec) : {}
}

function arr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : []
}

function str(v: unknown, def = ''): string {
  return typeof v === 'string' ? v : def
}

function strOrNull(v: unknown): string | null {
  return typeof v === 'string' && v !== '' ? v : null
}

function num(v: unknown, def = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : def
}

function numOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

function bool(v: unknown): boolean {
  return v === true
}

function oneOf<T extends string>(values: readonly T[], v: unknown): T | null {
  return typeof v === 'string' && (values as readonly string[]).includes(v) ? (v as T) : null
}

const STOP_KINDS = ['pickup', 'intermediate', 'destination'] as const
const ACTORS = ['passenger', 'driver', 'admin', 'system'] as const

function bad(what: string): ApiError {
  return new ApiError(200, 'bad_response', `unexpected ${what} payload`)
}

function stopKind(v: unknown, seq: number, count: number): StopKind {
  return oneOf(STOP_KINDS, v) ?? (seq === 0 ? 'pickup' : seq === count - 1 ? 'destination' : 'intermediate')
}

function status(v: unknown): OrderStatus {
  const s = oneOf(ORDER_STATUSES, v)
  if (!s) throw bad('order status')
  return s
}

function category(v: unknown): Category {
  return oneOf(CATEGORIES, v) ?? 'taxi'
}

function paymentMethod(v: unknown): PaymentMethod {
  return oneOf(PAYMENT_METHODS, v) ?? 'cash'
}

/** The class every order had before vehicle classes existed (§21.2 backfill). */
export const DEFAULT_VEHICLE_CLASS = 'sedan'

function cargoSize(v: unknown): CargoSize | null {
  return oneOf(CARGO_SIZE_CODES, v)
}

function positiveIntOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) && v > 0 ? v : null
}

/** The load fields every order view carries (§21). */
function parseLoad(r: Rec): OrderLoad {
  return {
    vehicle_class: strOrNull(r.vehicle_class) ?? DEFAULT_VEHICLE_CLASS,
    vehicle_class_name: localizedOrNull(r.vehicle_class_name),
    passenger_count: positiveIntOrNull(r.passenger_count),
    cargo_size: cargoSize(r.cargo_size),
    cargo_volume_l: Math.max(0, num(r.cargo_volume_l)),
  }
}

function parseTariffRegion(v: unknown): TariffRegion | null {
  const r = rec(v)
  const code = strOrNull(r.code)
  if (!code) return null
  return { code, name: localizedOrNull(r.name) ?? { kk: code, ru: code, en: code } }
}

/** Locality and tariff region of an order; a locality that is only coordinates is dropped. */
function parsePlace(r: Rec): OrderPlace {
  const locality = str(r.locality).trim()
  return {
    locality: isUnnamedPlace(locality) ? null : locality,
    tariff_region: parseTariffRegion(r.tariff_region),
  }
}

export function parseGeometry(v: unknown): LineStringGeometry | null {
  const r = rec(v)
  if (r.type !== 'LineString') return null
  const coords = arr(r.coordinates).filter(
    (p): p is Position => Array.isArray(p) && p.length >= 2 && typeof p[0] === 'number' && typeof p[1] === 'number',
  )
  return coords.length >= 2 ? { type: 'LineString', coordinates: coords } : null
}

export function parseBBox(v: unknown): BBox | null {
  const a = arr(v)
  return a.length === 4 && a.every((x) => typeof x === 'number' && Number.isFinite(x)) ? (a as BBox) : null
}

function parseCity(v: unknown): CityRef | null {
  const r = rec(v)
  const id = strOrNull(r.id)
  const code = str(r.code)
  if (!id) return null
  return { id, code, name: localizedOrNull(r.name) ?? { kk: code, ru: code, en: code } }
}

function parseLeg(v: unknown): RouteLeg {
  const r = rec(v)
  return { distance_m: numOrNull(r.distance_m), duration_s: num(r.duration_s) }
}

export function parseGeoPlace(v: unknown): GeoPlace | null {
  const r = rec(v)
  const lat = numOrNull(r.lat)
  const lon = numOrNull(r.lon)
  const title = str(r.title).trim()
  if (lat === null || lon === null || !title) return null
  return { title, subtitle: strOrNull(r.subtitle), lat, lon, source: str(r.source, 'unknown') }
}

export function parseGeoPlaces(v: unknown): GeoPlace[] {
  return arr(v)
    .map(parseGeoPlace)
    .filter((p): p is GeoPlace => p !== null)
}

/**
 * Reverse geocoding result. The API's last resort (source "coordinates") has
 * no title: the UI names it "Point on the map"; coordinates text never
 * becomes the title.
 */
export function parseGeoReverse(v: unknown): GeoReverse {
  const r = rec(v)
  const named = parseGeoPlace(v)
  const lat = numOrNull(r.lat)
  const lon = numOrNull(r.lon)
  const place = named ?? (lat !== null && lon !== null ? { title: '', subtitle: strOrNull(r.subtitle), lat, lon, source: str(r.source, 'coordinates') } : null)
  if (!place) throw bad('reverse geocoding')
  return { ...place, title: isUnnamedPlace(place.title) ? '' : place.title, in_service_area: r.in_service_area !== false }
}

function parseFare(v: unknown): FareOptions {
  const r = rec(v)
  const options = arr(r.options)
    .filter((x): x is number => typeof x === 'number' && Number.isInteger(x) && x > 0)
    .sort((a, b) => a - b)
  if (options.length === 0) throw bad('fare')
  const min = num(r.min, options[0]!)
  const max = num(r.max, options[options.length - 1]!)
  const recommended = options.includes(num(r.recommended)) ? num(r.recommended) : options[0]!
  return { recommended, min, max, step: num(r.step, 1), options }
}

function parseQuoteStop(v: unknown, i: number, count: number): QuoteStop {
  const r = rec(v)
  const seq = num(r.seq, i)
  return {
    seq,
    kind: stopKind(r.kind, seq, count),
    mode: r.mode === 'manual' ? 'manual' : 'geocoded',
    lat: numOrNull(r.lat),
    lon: numOrNull(r.lon),
    address: str(r.address),
    address_details: strOrNull(r.address_details),
  }
}

function parseOption(v: unknown): RouteQuoteOption | null {
  const r = rec(v)
  const quoteId = strOrNull(r.quote_id)
  const vehicleClass = strOrNull(r.vehicle_class)
  if (!quoteId || !vehicleClass) return null
  let fare: FareOptions
  try {
    fare = parseFare(r.fare)
  } catch {
    return null
  }
  return {
    vehicle_class: vehicleClass,
    name: localizedOrNull(r.name),
    examples: str(r.examples).trim(),
    icon: oneOf(VEHICLE_ICONS, r.icon) ?? 'sedan',
    seats_max: Math.max(0, num(r.seats_max)),
    cargo_volume_l: Math.max(0, num(r.cargo_volume_l)),
    quote_id: quoteId,
    expires_at: str(r.expires_at),
    fare,
    commission_bps: num(r.commission_bps),
    eta_s: numOrNull(r.eta_s),
  }
}

/**
 * Route quote with one option per vehicle class (§21.3). An API that sends no
 * options (older build) yields a single option made of the top-level quote, so
 * the screens always have at least one class to show.
 */
export function parseQuote(v: unknown): RouteQuote {
  const r = rec(v)
  const quoteId = strOrNull(r.quote_id)
  if (!quoteId) throw bad('quote')
  const stops = arr(r.stops)
  const fare = parseFare(r.fare)
  const vehicleClass = strOrNull(r.vehicle_class) ?? DEFAULT_VEHICLE_CLASS
  const options: RouteQuoteOption[] = []
  for (const raw of arr(r.options)) {
    const o = parseOption(raw)
    if (o && !options.some((x) => x.vehicle_class === o.vehicle_class)) options.push(o)
  }
  if (options.length === 0) {
    options.push({
      vehicle_class: vehicleClass,
      name: null,
      examples: '',
      icon: oneOf(VEHICLE_ICONS, vehicleClass) ?? 'sedan',
      seats_max: 0,
      cargo_volume_l: 0,
      quote_id: quoteId,
      expires_at: str(r.expires_at),
      fare,
      commission_bps: num(r.commission_bps),
      eta_s: null,
    })
  }
  return {
    quote_id: quoteId,
    expires_at: str(r.expires_at),
    category: category(r.category),
    vehicle_class: vehicleClass,
    passenger_count: positiveIntOrNull(r.passenger_count),
    cargo_size: cargoSize(r.cargo_size),
    city: parseCity(r.city),
    stops: stops.map((s, i) => parseQuoteStop(s, i, stops.length)),
    distance_m: numOrNull(r.distance_m),
    duration_s: numOrNull(r.duration_s),
    legs: arr(r.legs).map(parseLeg),
    geometry: parseGeometry(r.geometry),
    bbox: parseBBox(r.bbox),
    fare,
    commission_bps: num(r.commission_bps),
    payment_methods: arr(r.payment_methods)
      .map((m) => oneOf(PAYMENT_METHODS, m))
      .filter((m): m is PaymentMethod => m !== null),
    options,
  }
}

function parseStop(v: unknown, i: number, count: number): OrderStop {
  const r = rec(v)
  const seq = num(r.seq, i)
  return {
    id: str(r.id, `stop-${i}`),
    seq,
    kind: stopKind(r.kind, seq, count),
    mode: r.mode === 'manual' ? 'manual' : 'geocoded',
    lat: numOrNull(r.lat),
    lon: numOrNull(r.lon),
    address: str(r.address),
    address_details: strOrNull(r.address_details),
    completed_at: strOrNull(r.completed_at),
  }
}

function parseRoute(v: unknown): RouteSnapshot {
  const r = rec(v)
  return {
    geometry: parseGeometry(r.geometry),
    legs: arr(r.legs).map(parseLeg),
    bbox: parseBBox(r.bbox),
    distance_m: numOrNull(r.distance_m),
    duration_s: numOrNull(r.duration_s),
  }
}

function parseDriver(v: unknown): OrderDriver | null {
  const r = rec(v)
  const id = strOrNull(r.id)
  if (!id) return null
  const rating = numOrNull(r.rating)
  return {
    id,
    code: str(r.code),
    first_name: str(r.first_name),
    last_name: str(r.last_name),
    phone: strOrNull(r.phone),
    rating: rating !== null && rating >= 1 && rating <= 5 ? rating : null,
    rating_count: num(r.rating_count),
    completed_trips: num(r.completed_trips),
    photo_url: strOrNull(r.photo_url),
  }
}

function parseVehicle(v: unknown): Vehicle | null {
  const r = rec(v)
  const plate = str(r.plate)
  if (!plate && !r.make) return null
  return {
    make: str(r.make),
    model: str(r.model),
    color: str(r.color),
    plate,
    vehicle_class: strOrNull(r.vehicle_class),
    vehicle_class_name: localizedOrNull(r.vehicle_class_name),
    seats: positiveIntOrNull(r.seats),
    cargo_volume_l: numOrNull(r.cargo_volume_l),
  }
}

export function parseDriverLocation(v: unknown): DriverLocation | null {
  const r = rec(v)
  const lat = numOrNull(r.lat)
  const lon = numOrNull(r.lon)
  if (lat === null || lon === null) return null
  return {
    lat,
    lon,
    accuracy_m: numOrNull(r.accuracy_m),
    heading: numOrNull(r.heading),
    recorded_at: str(r.recorded_at),
  }
}

function parseEvent(v: unknown): OrderEvent | null {
  const r = rec(v)
  const to = oneOf(ORDER_STATUSES, r.to_status)
  if (!to) return null
  return {
    from_status: oneOf(ORDER_STATUSES, r.from_status),
    to_status: to,
    actor_type: oneOf(ACTORS, r.actor_type) ?? 'system',
    reason: strOrNull(r.reason),
    created_at: str(r.created_at),
  }
}

function parseRating(v: unknown): OrderRating | null {
  const r = rec(v)
  const rating = numOrNull(r.rating)
  if (rating === null) return null
  return { rating, comment: strOrNull(r.comment), created_at: str(r.created_at) }
}

function parseDispute(v: unknown): OrderDispute | null {
  const r = rec(v)
  const st = oneOf(['open', 'resolved'] as const, r.status)
  if (!st) return null
  return {
    status: st,
    reason: str(r.reason),
    outcome: oneOf(['completed', 'cancelled'] as const, r.outcome),
    resolution: strOrNull(r.resolution),
    created_at: str(r.created_at),
    resolved_at: strOrNull(r.resolved_at),
  }
}

export function parseOrder(v: unknown): Order {
  const r = rec(v)
  const id = strOrNull(r.id)
  if (!id) throw bad('order')
  const stops = arr(r.stops)
  return {
    id,
    number: num(r.number),
    status: status(r.status),
    category: category(r.category),
    ...parseLoad(r),
    ...parsePlace(r),
    cargo_description: strOrNull(r.cargo_description),
    recipient_phone: strOrNull(r.recipient_phone),
    comment: strOrNull(r.comment),
    fare: num(r.fare),
    payment_method: paymentMethod(r.payment_method),
    distance_m: numOrNull(r.distance_m),
    duration_s: numOrNull(r.duration_s),
    city: parseCity(r.city),
    stops: stops.map((s, i) => parseStop(s, i, stops.length)).sort((a, b) => a.seq - b.seq),
    route: parseRoute(r.route),
    driver: parseDriver(r.driver),
    vehicle: parseVehicle(r.vehicle),
    driver_location: parseDriverLocation(r.driver_location),
    events: arr(r.events)
      .map(parseEvent)
      .filter((e): e is OrderEvent => e !== null),
    rating: parseRating(r.rating),
    dispute: parseDispute(r.dispute),
    cancel_reason: strOrNull(r.cancel_reason),
    cancel_reason_code: oneOf(CANCEL_REASON_CODES, r.cancel_reason_code),
    cancelled_by: oneOf(ACTORS, r.cancelled_by),
    driver_eta_s: numOrNull(r.driver_eta_s),
    driver_eta_at: strOrNull(r.driver_eta_at),
    can_cancel: bool(r.can_cancel),
    can_rate: bool(r.can_rate),
    can_dispute: bool(r.can_dispute),
    version: num(r.version, 1),
    created_at: str(r.created_at),
    updated_at: str(r.updated_at),
    search_expires_at: strOrNull(r.search_expires_at),
    accepted_at: strOrNull(r.accepted_at),
    arrived_at: strOrNull(r.arrived_at),
    started_at: strOrNull(r.started_at),
    completed_at: strOrNull(r.completed_at),
    cancelled_at: strOrNull(r.cancelled_at),
  }
}

function parseStopSummary(v: unknown, kind: StopKind): StopSummary {
  const r = rec(v)
  return {
    seq: num(r.seq),
    kind: oneOf(STOP_KINDS, r.kind) ?? kind,
    mode: r.mode === 'manual' ? 'manual' : 'geocoded',
    lat: numOrNull(r.lat),
    lon: numOrNull(r.lon),
    address: str(r.address),
  }
}

export function parseOrderSummary(v: unknown): OrderSummary | null {
  const r = rec(v)
  const id = strOrNull(r.id)
  const st = oneOf(ORDER_STATUSES, r.status)
  if (!id || !st) return null
  return {
    id,
    number: num(r.number),
    status: st,
    category: category(r.category),
    ...parseLoad(r),
    ...parsePlace(r),
    fare: num(r.fare),
    payment_method: paymentMethod(r.payment_method),
    pickup: parseStopSummary(r.pickup, 'pickup'),
    destination: parseStopSummary(r.destination, 'destination'),
    stop_count: num(r.stop_count, 2),
    distance_m: numOrNull(r.distance_m),
    driver_first_name: strOrNull(r.driver_first_name),
    rating: numOrNull(r.rating),
    can_rate: bool(r.can_rate),
    created_at: str(r.created_at),
    completed_at: strOrNull(r.completed_at),
    cancelled_at: strOrNull(r.cancelled_at),
  }
}

export function parseOrderPage(v: unknown): CursorPage<OrderSummary> {
  const r = rec(v)
  return {
    items: arr(r.items)
      .map(parseOrderSummary)
      .filter((o): o is OrderSummary => o !== null),
    next_cursor: strOrNull(r.next_cursor),
  }
}
