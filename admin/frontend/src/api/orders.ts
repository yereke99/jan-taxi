// Admin orders and disputes (§19 admin rows; OpenAPI AdminOrder*, Dispute*).

import { api } from '../services/api'
import type { Category, OrderStatus, PaymentMethod } from '../utils/status'
import type { ActorType, AdminRef, CityRef, DriverRef, LatLon, LocalizedText, Page, TariffRegion, Vehicle } from './common'
import { dayEnd, dayStart, opt } from './common'

/**
 * Where an order happens and which tariff priced it: `locality` is the real
 * settlement of pickup A (reverse geocoding; null when unknown), while
 * `tariff_region` is the city zone or the nationwide region (KAZ) whose
 * tariff applied. `city` is the same tariff city as a reference.
 */
export interface OrderPlace {
  locality: string | null
  tariff_region: TariffRegion | null
  city: CityRef
}

/** The tariff region of an order; older payloads only carry `city`. */
export function orderTariffRegion(o: Pick<OrderPlace, 'tariff_region' | 'city'>): TariffRegion | null {
  return o.tariff_region ?? (o.city ? { code: o.city.code, name: o.city.name } : null)
}

export type StopKind = 'pickup' | 'intermediate' | 'destination'

/** Passenger cancellation reasons (§22.3), in the order the passenger sees them. */
export const CANCEL_REASON_CODES = ['long_wait', 'driver_issue', 'vehicle_issue', 'changed_plans', 'other'] as const
export type CancelReasonCode = (typeof CANCEL_REASON_CODES)[number]

export type CommissionMode = 'fixed' | 'percent'

/** Vehicle class and load of an order (§21): seats for taxi, cargo size for parcel/other. */
export interface OrderLoad {
  vehicle_class: string
  vehicle_class_name: LocalizedText
  /** Taxi only. */
  passenger_count: number | null
  /** Parcel/other only: s | m | l | xl. */
  cargo_size: string | null
  /** 0 for taxi. */
  cargo_volume_l: number
}

export interface StopSummary {
  seq: number
  kind: StopKind
  address: string
  mode?: 'geocoded' | 'manual'
  lat: number | null
  lon: number | null
}

/** Manual destinations never become synthetic (0, 0) map points. */
export function hasStopCoordinates<T extends StopSummary>(stop: T): stop is T & LatLon {
  return stop.mode !== 'manual' && typeof stop.lat === 'number' && typeof stop.lon === 'number'
    && Number.isFinite(stop.lat) && Number.isFinite(stop.lon)
    && Math.abs(stop.lat) <= 90 && Math.abs(stop.lon) <= 180
}

export interface Stop extends StopSummary {
  id: string
  address_details: string | null
  completed_at: string | null
}

export interface AdminPassengerRef {
  id: string
  telegram_user_id: number
  first_name: string
  last_name: string
  username: string | null
  phone: string | null
  is_blocked: boolean
}

export interface AdminOrderListItem extends OrderLoad, OrderPlace {
  id: string
  number: number
  status: OrderStatus
  category: Category
  service_type: 'passenger' | 'delivery'
  fare: number
  currency: string
  commission_amount: number
  payment_method: PaymentMethod
  passenger: AdminPassengerRef
  driver: DriverRef | null
  pickup: StopSummary
  destination: StopSummary
  stop_count: number
  distance_m: number | null
  has_open_dispute: boolean
  created_at: string
  accepted_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  cancelled_by: ActorType | null
  cancel_reason_code: CancelReasonCode | null
}

export interface LineString {
  type: 'LineString'
  coordinates: Array<[number, number] | [number, number, number]>
}

export interface RouteSnapshot {
  geometry: LineString | null
  legs: Array<{ distance_m: number; duration_s: number }> | null
  bbox: [number, number, number, number] | null
  distance_m: number | null
  duration_s: number | null
}

export interface DriverLocation extends LatLon {
  accuracy_m: number | null
  heading: number | null
  recorded_at: string
}

export interface OrderOffer {
  driver: DriverRef
  status: 'sent' | 'accepted' | 'lost' | 'expired' | 'cancelled'
  distance_to_pickup_m: number | null
  sent_at: string
  closed_at: string | null
}

export interface AdminOrderEvent {
  id: number
  from_status: OrderStatus | null
  to_status: OrderStatus
  actor_type: ActorType
  actor_id: string | null
  reason: string | null
  meta: Record<string, unknown> | null
  created_at: string
}

export interface WalletReservation {
  id: string
  amount: number
  status: 'active' | 'settled' | 'released'
  created_at: string
  closed_at: string | null
}

/** Wallet journal row linked to the order (orders.LedgerRow). */
export interface OrderLedgerRow {
  id: number
  driver_id: string
  entry_type: string
  balance_delta: number
  reserved_delta: number
  balance_after: number
  reserved_after: number
  currency: string
  order_id: string | null
  order_number: number | null
  topup_id: string | null
  topup_reference: string | null
  description: string | null
  created_at: string
}

export type DisputeOutcome = 'completed' | 'cancelled'

export interface Dispute {
  id: string
  order_id: string
  order_number: number
  status: 'open' | 'resolved'
  opened_by_type: 'passenger' | 'driver' | 'admin'
  opened_by_id: string | null
  reason: string
  previous_status: OrderStatus
  outcome: DisputeOutcome | null
  resolution: string | null
  resolved_by: AdminRef | null
  created_at: string
  resolved_at: string | null
}

export interface OrderRating {
  rating: number
  comment: string | null
  created_at: string
}

export interface AdminOrder extends OrderLoad, OrderPlace {
  id: string
  number: number
  status: OrderStatus
  category: Category
  service_type: 'passenger' | 'delivery'
  cargo_type: 'none' | 'parcel' | 'other'
  cargo_description: string | null
  recipient_phone: string | null
  prohibited_items_ack: boolean
  comment: string | null
  fare: number
  currency: string
  commission_bps: number
  commission_amount: number
  /** fixed: commission.fixed_amount at creation; percent: commission_bps of the fare (orders before v2). */
  commission_mode: CommissionMode
  payment_method: PaymentMethod
  tariff_id: string
  quote_id: string | null
  distance_m: number | null
  duration_s: number | null
  stop_count: number
  stops: Stop[]
  route: RouteSnapshot
  passenger: AdminPassengerRef
  driver: DriverRef | null
  vehicle: Vehicle | null
  driver_location: DriverLocation | null
  offers: OrderOffer[]
  events: AdminOrderEvent[]
  reservation: WalletReservation | null
  ledger: OrderLedgerRow[]
  dispute: Dispute | null
  rating: OrderRating | null
  /** Passenger comment (or the operator's reason for an admin cancel). */
  cancel_reason: string | null
  /** Reason the passenger chose; null for other cancellations and older orders. */
  cancel_reason_code: CancelReasonCode | null
  cancelled_by: ActorType | null
  /** Road time from the driver to pickup A while accepted (§22.6). */
  driver_eta_s: number | null
  driver_eta_at: string | null
  version: number
  created_at: string
  updated_at: string
  search_expires_at: string | null
  accepted_at: string | null
  arrived_at: string | null
  started_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  /** Non-fatal notes on an action response, e.g. ["offline"] after assigning an offline driver. */
  warnings?: string[]
}

export interface OrderFilters {
  status?: string
  city_id?: string
  category?: string
  driver_id?: string
  /** Local dates YYYY-MM-DD (inclusive). */
  from?: string
  to?: string
  /** Order number, id, phone, driver code or part of the locality of pickup A (e.g. "Алматы"). */
  q?: string
}

export function orderQuery(f: OrderFilters): Record<string, string | undefined> {
  return {
    status: opt(f.status),
    city_id: opt(f.city_id),
    category: opt(f.category),
    driver_id: opt(f.driver_id),
    from: dayStart(f.from),
    to: dayEnd(f.to),
    q: opt(f.q?.trim()),
  }
}

export const ordersApi = {
  list(f: OrderFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminOrderListItem>> {
    return api.get('/admin/orders', { query: { ...orderQuery(f), page, page_size: pageSize }, signal })
  },
  get(id: string, signal?: AbortSignal): Promise<AdminOrder> {
    return api.get(`/admin/orders/${encodeURIComponent(id)}`, { signal })
  },
  /** The driver's commission is kept unless refundCommission (§22.2). */
  cancel(id: string, reason: string, refundCommission = false): Promise<AdminOrder> {
    return api.post(`/admin/orders/${encodeURIComponent(id)}/cancel`, { reason, refund_commission: refundCommission })
  },
  /** On a re-assignment, refundCommission returns the previous driver's commission; the new driver is always charged. */
  assign(id: string, driverId: string, refundCommission = false): Promise<AdminOrder> {
    return api.post(`/admin/orders/${encodeURIComponent(id)}/assign`, { driver_id: driverId, refund_commission: refundCommission })
  },
  openDispute(id: string, reason: string): Promise<AdminOrder> {
    return api.post(`/admin/orders/${encodeURIComponent(id)}/dispute`, { reason })
  },
}

export const disputesApi = {
  list(status: string, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<Dispute>> {
    return api.get('/admin/disputes', { query: { status: opt(status), page, page_size: pageSize }, signal })
  },
  resolve(id: string, body: { outcome: DisputeOutcome; resolution: string; refund_commission: boolean }): Promise<Dispute> {
    return api.post(`/admin/disputes/${encodeURIComponent(id)}/resolve`, body)
  },
}
