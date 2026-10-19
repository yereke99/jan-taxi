// Passenger API types (admin/backend/api/openapi.yaml: GeoPlace,
// GeoReverseResult, RouteQuote, Order, OrderSummary…). Field names are the
// API's snake_case; parsers in ./parse.ts fill these from raw JSON.

import type { LineStringGeometry } from '../maps'
import type { CargoSize, LocalizedText, VehicleIcon } from '../services/config'
import type { LatLon } from '../utils/geo'
import type { Category, OrderStatus, PaymentMethod } from '../utils/status'

export type StopKind = 'pickup' | 'intermediate' | 'destination'
export type ActorType = 'passenger' | 'driver' | 'admin' | 'system'

export interface GeoPlace extends LatLon {
  title: string
  subtitle: string | null
  /** 'place' (admin-curated), a geocoder name, or 'coordinates'. */
  source: string
}

export interface GeoReverse extends GeoPlace {
  in_service_area: boolean
}

export interface StopInput extends LatLon {
  address: string
  address_details?: string
}

export interface RoutePreviewRequest {
  category: Category
  stops: StopInput[]
  /** Taxi only, 1..max_passenger_count (API default 1). */
  passenger_count?: number
  /** Parcel/other only (API default 's'). */
  cargo_size?: CargoSize
}

export interface CityRef {
  id: string
  code: string
  name: LocalizedText
}

export interface QuoteStop extends LatLon {
  seq: number
  kind: StopKind
  address: string
  address_details: string | null
}

export interface RouteLeg {
  distance_m: number
  duration_s: number
}

export interface FareOptions {
  recommended: number
  min: number
  max: number
  step: number
  options: number[]
}

/** [min_lon, min_lat, max_lon, max_lat] */
export type BBox = [number, number, number, number]

/**
 * The quote of one vehicle class able to carry the request (§21.3). POST
 * /orders takes the chosen option's quote_id.
 */
export interface RouteQuoteOption {
  vehicle_class: string
  /** Null when the API did not name the class (the app falls back to its own names). */
  name: LocalizedText | null
  examples: string
  icon: VehicleIcon
  seats_max: number
  /** The class's default cargo volume, litres. */
  cargo_volume_l: number
  quote_id: string
  expires_at: string
  fare: FareOptions
  commission_bps: number
  /** Seconds until the nearest fitting driver could reach pickup A; null when none is near. */
  eta_s: number | null
}

export interface RouteQuote {
  quote_id: string
  expires_at: string
  category: Category
  vehicle_class: string
  passenger_count: number | null
  cargo_size: CargoSize | null
  city: CityRef | null
  stops: QuoteStop[]
  distance_m: number
  duration_s: number
  legs: RouteLeg[]
  geometry: LineStringGeometry | null
  bbox: BBox | null
  fare: FareOptions
  commission_bps: number
  payment_methods: PaymentMethod[]
  /** One per class in display order; never empty (the top-level quote is options[0]). */
  options: RouteQuoteOption[]
}

export interface OrderCreateRequest {
  quote_id: string
  fare: number
  payment_method: PaymentMethod
  comment?: string
  cargo_description?: string
  recipient_phone?: string
  prohibited_items_ack?: boolean
  idempotency_key: string
}

export interface OrderStop extends LatLon {
  id: string
  seq: number
  kind: StopKind
  address: string
  address_details: string | null
  completed_at: string | null
}

export interface RouteSnapshot {
  geometry: LineStringGeometry | null
  legs: RouteLeg[]
  bbox: BBox | null
  distance_m: number
  duration_s: number
}

export interface OrderDriver {
  id: string
  code: string
  first_name: string
  /** Initial only ("Б."). */
  last_name: string
  /** E.164 while the order is active, otherwise null. */
  phone: string | null
  rating: number | null
  rating_count: number
  completed_trips: number
  photo_url: string | null
}

export interface Vehicle {
  make: string
  model: string
  color: string
  plate: string
  vehicle_class: string | null
  vehicle_class_name: LocalizedText | null
  seats: number | null
  cargo_volume_l: number | null
}

/** What an order asks for (§21): the class, and the passenger count or cargo size. */
export interface OrderLoad {
  vehicle_class: string
  vehicle_class_name: LocalizedText | null
  /** Taxi only. */
  passenger_count: number | null
  /** Parcel/other only. */
  cargo_size: CargoSize | null
  /** 0 for taxi. */
  cargo_volume_l: number
}

/** The city zone, or the nationwide region (code KAZ), whose tariff priced an order. */
export interface TariffRegion {
  code: string
  name: LocalizedText
}

/**
 * Where an order happens: `locality` is the real settlement of pickup A from
 * reverse geocoding (city, town, village, else district), never coordinates;
 * null when unknown. `tariff_region` only says which tariff priced it.
 */
export interface OrderPlace {
  locality: string | null
  tariff_region: TariffRegion | null
}

/** Passenger cancellation reasons (§22.3). */
export const CANCEL_REASON_CODES = ['long_wait', 'driver_issue', 'vehicle_issue', 'changed_plans', 'other'] as const
export type CancelReasonCode = (typeof CANCEL_REASON_CODES)[number]

export interface CancelOrderRequest {
  reason_code: CancelReasonCode
  /** Required for 'other'; at most 120 code points. */
  comment?: string
  /**
   * The status the passenger saw when confirming. If the order moved on
   * meanwhile (a driver accepted it), the API answers 409 conflict
   * {reason: order_changed} instead of cancelling.
   */
  expected_status?: OrderStatus
}

export interface DriverLocation extends LatLon {
  accuracy_m: number | null
  heading: number | null
  recorded_at: string
}

export interface OrderEvent {
  from_status: OrderStatus | null
  to_status: OrderStatus
  actor_type: ActorType
  reason: string | null
  created_at: string
}

export interface OrderRating {
  rating: number
  comment: string | null
  created_at: string
}

export interface OrderDispute {
  status: 'open' | 'resolved'
  reason: string
  outcome: 'completed' | 'cancelled' | null
  resolution: string | null
  created_at: string
  resolved_at: string | null
}

export interface Order extends OrderLoad, OrderPlace {
  id: string
  number: number
  status: OrderStatus
  category: Category
  cargo_description: string | null
  recipient_phone: string | null
  comment: string | null
  fare: number
  payment_method: PaymentMethod
  distance_m: number
  duration_s: number
  city: CityRef | null
  stops: OrderStop[]
  route: RouteSnapshot
  driver: OrderDriver | null
  vehicle: Vehicle | null
  driver_location: DriverLocation | null
  events: OrderEvent[]
  rating: OrderRating | null
  dispute: OrderDispute | null
  cancel_reason: string | null
  cancel_reason_code: CancelReasonCode | null
  cancelled_by: ActorType | null
  /** Seconds until the driver reaches pickup A while the order is accepted (§22.6). */
  driver_eta_s: number | null
  /** When driver_eta_s was computed. */
  driver_eta_at: string | null
  can_cancel: boolean
  can_rate: boolean
  can_dispute: boolean
  version: number
  created_at: string
  updated_at: string
  search_expires_at: string | null
  accepted_at: string | null
  arrived_at: string | null
  started_at: string | null
  completed_at: string | null
  cancelled_at: string | null
}

export interface StopSummary extends LatLon {
  seq: number
  kind: StopKind
  address: string
}

export interface OrderSummary extends OrderLoad, OrderPlace {
  id: string
  number: number
  status: OrderStatus
  category: Category
  fare: number
  payment_method: PaymentMethod
  pickup: StopSummary
  destination: StopSummary
  stop_count: number
  distance_m: number
  driver_first_name: string | null
  rating: number | null
  can_rate: boolean
  created_at: string
  completed_at: string | null
  cancelled_at: string | null
}

export interface CursorPage<T> {
  items: T[]
  next_cursor: string | null
}

export type OrderScope = 'active' | 'history'
