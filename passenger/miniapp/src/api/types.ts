// Passenger API types (admin/backend/api/openapi.yaml: GeoPlace,
// GeoReverseResult, RouteQuote, Order, OrderSummary, PassengerPromotions,
// ReferralView…). Field names are the API's snake_case; parsers in ./parse.ts
// fill these from raw JSON.

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

export interface StopInput {
  mode?: 'geocoded' | 'manual'
  lat: number | null
  lon: number | null
  address: string
  /**
   * True only for pickup A whose address is the passenger's own words while A
   * keeps its coordinates (§3); never sent for a manual stop.
   */
  address_typed?: boolean
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

export interface QuoteStop {
  mode?: 'geocoded' | 'manual'
  lat: number | null
  lon: number | null
  seq: number
  kind: StopKind
  address: string
  /** The passenger wrote the address of a point that keeps its coordinates (typed pickup A). */
  address_typed: boolean
  address_details: string | null
}

export interface RouteLeg {
  distance_m: number | null
  duration_s: number | null
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
  distance_m: number | null
  duration_s: number | null
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
  /**
   * The promotion the passenger confirmed (§23.6). The server computes the
   * discount itself and refuses the order (409 promotion_changed) when it is
   * not promotion_discount, the discount the app showed.
   */
  promotion?: PromotionKind | 'none'
  promotion_discount?: number
  idempotency_key: string
}

export interface OrderStop {
  mode?: 'geocoded' | 'manual'
  lat: number | null
  lon: number | null
  id: string
  seq: number
  kind: StopKind
  address: string
  /** The passenger wrote the address of a point that keeps its coordinates (typed pickup A). */
  address_typed: boolean
  address_details: string | null
  completed_at: string | null
}

export interface RouteSnapshot {
  geometry: LineStringGeometry | null
  legs: RouteLeg[]
  bbox: BBox | null
  distance_m: number | null
  duration_s: number | null
}

export interface OrderDriver {
  id: string
  code: string
  first_name: string
  /** Initial only ("Б."). */
  last_name: string
  /** E.164 while the order is active, otherwise null. */
  phone: string | null
  /**
   * The driver's Telegram chat while the order is active (https://t.me/<username>
   * or tg://user?id=<id>), otherwise null; offered when there is no phone.
   */
  telegram_url: string | null
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

/**
 * A coupon or bonus on an order (§23.6). The fare stays the class price;
 * promo_discount is what the promotion took off and cash_due (= fare -
 * promo_discount) what the passenger pays the driver.
 */
export interface OrderPromoAmounts {
  promo_discount: number
  cash_due: number
}

export interface Order extends OrderLoad, OrderPlace, OrderPromoAmounts {
  id: string
  number: number
  status: OrderStatus
  category: Category
  cargo_description: string | null
  recipient_phone: string | null
  comment: string | null
  fare: number
  payment_method: PaymentMethod
  /** The promotion applied to the order, null without one. */
  promotion: OrderPromotion | null
  distance_m: number | null
  duration_s: number | null
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

export interface StopSummary {
  mode?: 'geocoded' | 'manual'
  lat: number | null
  lon: number | null
  seq: number
  kind: StopKind
  address: string
  /** The passenger wrote the address of a point that keeps its coordinates (typed pickup A). */
  address_typed: boolean
}

export interface OrderSummary extends OrderLoad, OrderPlace, OrderPromoAmounts {
  id: string
  number: number
  status: OrderStatus
  category: Category
  fare: number
  payment_method: PaymentMethod
  pickup: StopSummary
  destination: StopSummary
  stop_count: number
  distance_m: number | null
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

// --- Promotions (§23: GET /promotions, /promotions/referral, /promotions/bonus/history) ---

export const PROMOTION_KINDS = ['coupon', 'bonus'] as const
export type PromotionKind = (typeof PROMOTION_KINDS)[number]

/** The promotion of an order: reserved while it runs, released when cancelled, settled when completed. */
export const ORDER_PROMOTION_STATUSES = ['reserved', 'released', 'settled', 'reversed'] as const
export type OrderPromotionStatus = (typeof ORDER_PROMOTION_STATUSES)[number]

export interface OrderPromotion {
  kind: PromotionKind
  /** The campaign's name; null for the bonus balance. */
  title: LocalizedText | null
  discount: number
  gross_fare: number
  cash_due: number
  status: OrderPromotionStatus
  /** What Jan Taxi credited the driver once the order completed. */
  compensation: number | null
  settled_at: string | null
}

/** "expired" also comes from the server for an issued coupon past expires_at. */
export const COUPON_STATUSES = ['issued', 'reserved', 'redeemed', 'expired', 'voided'] as const
export type CouponStatus = (typeof COUPON_STATUSES)[number]

export interface PromoCoupon {
  id: string
  title: LocalizedText | null
  amount: number
  /** The coupon applies to an order whose fare is at least this. */
  min_order_fare: number
  categories: Category[]
  status: CouponStatus
  issued_at: string
  /** Decided on the server's clock: the coupon is unusable from this instant. */
  expires_at: string
  /** The order the coupon is reserved for while it runs. */
  reserved_order_id: string | null
  redeemed_at: string | null
}

export interface BonusAccount {
  balance: number
  /** Earmarked for an active order. */
  reserved: number
  available: number
}

/** A promotion the passenger can still earn ("after your first ride you get a 400 ₸ coupon"). */
export interface PromoOffer {
  kind: 'first_ride_coupon'
  title: LocalizedText | null
  description: LocalizedText | null
  amount: number
  /** Null when the API did not say. */
  validity_hours: number | null
  categories: Category[]
}

/** An informational campaign without money. */
export interface PromoBanner {
  id: string
  title: LocalizedText | null
  description: LocalizedText | null
}

export const REFERRAL_STATES = ['unavailable', 'locked', 'unlocked'] as const
export type ReferralState = (typeof REFERRAL_STATES)[number]

/** Why inviting is not open yet; the app explains each. */
export const REFERRAL_LOCK_REASONS = ['coupon_not_redeemed', 'coupon_not_used', 'first_ride_required', 'paused', 'disabled'] as const
export type ReferralLockReason = (typeof REFERRAL_LOCK_REASONS)[number]

export interface ReferralSummary {
  state: ReferralState
  lock_reason: ReferralLockReason | null
  reward_amount: number
  /** The inviter already received the reward (it is paid once). */
  rewarded: boolean
}

export interface Promotions {
  /** The database clock when the answer was made; coupon validity is decided on it. */
  server_time: string
  redemptions_enabled: boolean
  coupons: PromoCoupon[]
  bonus: BonusAccount
  /** Order categories the bonus balance pays for. */
  bonus_categories: Category[]
  offers: PromoOffer[]
  banners: PromoBanner[]
  referral: ReferralSummary
  marketing_subscribed: boolean
}

export const REFERRAL_FRIEND_STATUSES = ['attributed', 'rewarded', 'capped', 'not_counted'] as const
export type ReferralFriendStatus = (typeof REFERRAL_FRIEND_STATUSES)[number]

/** An invited friend: first name and last-name initial only. */
export interface ReferralFriend {
  name: string
  status: ReferralFriendStatus
  attributed_at: string
}

export interface ReferralView extends ReferralSummary {
  title: LocalizedText | null
  description: LocalizedText | null
  /** Only while inviting is open. */
  code: string | null
  /** https://t.me/<passenger bot>?start=ref_<code> */
  link: string | null
  /** https://t.me/<passenger bot>?startapp=ref_<code> */
  app_link: string | null
  earned: number
  friends: ReferralFriend[]
}

/** What the passenger sees in the bonus history (reservations stay internal). */
export const BONUS_ENTRY_TYPES = ['grant', 'spend', 'adjustment_credit', 'adjustment_debit'] as const
export type BonusEntryType = (typeof BONUS_ENTRY_TYPES)[number]

export interface BonusEntry {
  id: number
  type: BonusEntryType
  /** Signed: positive came in, negative went out. */
  amount: number
  order_number: number | null
  created_at: string
}
