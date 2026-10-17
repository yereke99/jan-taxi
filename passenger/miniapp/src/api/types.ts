// Passenger API types (admin/backend/api/openapi.yaml: GeoPlace,
// GeoReverseResult, RouteQuote, Order, OrderSummary…). Field names are the
// API's snake_case; parsers in ./parse.ts fill these from raw JSON.

import type { LineStringGeometry } from '../maps'
import type { LocalizedText } from '../services/config'
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

export interface RouteQuote {
  quote_id: string
  expires_at: string
  category: Category
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

export interface Order {
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
  cancelled_by: ActorType | null
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

export interface OrderSummary {
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
