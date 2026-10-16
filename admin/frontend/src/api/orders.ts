// Admin orders and disputes (§19 admin rows; OpenAPI AdminOrder*, Dispute*).

import { api } from '../services/api'
import type { Category, OrderStatus, PaymentMethod } from '../utils/status'
import type { ActorType, AdminRef, CityRef, DriverRef, LatLon, Page, Vehicle } from './common'
import { dayEnd, dayStart, opt } from './common'

export type StopKind = 'pickup' | 'intermediate' | 'destination'

export interface StopSummary extends LatLon {
  seq: number
  kind: StopKind
  address: string
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

export interface AdminOrderListItem {
  id: string
  number: number
  status: OrderStatus
  category: Category
  service_type: 'passenger' | 'delivery'
  city: CityRef
  fare: number
  currency: string
  commission_amount: number
  payment_method: PaymentMethod
  passenger: AdminPassengerRef
  driver: DriverRef | null
  pickup: StopSummary
  destination: StopSummary
  stop_count: number
  distance_m: number
  has_open_dispute: boolean
  created_at: string
  accepted_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  cancelled_by: ActorType | null
}

export interface LineString {
  type: 'LineString'
  coordinates: Array<[number, number] | [number, number, number]>
}

export interface RouteSnapshot {
  geometry: LineString | null
  legs: Array<{ distance_m: number; duration_s: number }> | null
  bbox: [number, number, number, number] | null
  distance_m: number
  duration_s: number
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

export interface AdminOrder {
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
  payment_method: PaymentMethod
  tariff_id: string
  quote_id: string | null
  distance_m: number
  duration_s: number
  stop_count: number
  city: CityRef
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
  cancel_reason: string | null
  cancelled_by: ActorType | null
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
  cancel(id: string, reason: string): Promise<AdminOrder> {
    return api.post(`/admin/orders/${encodeURIComponent(id)}/cancel`, { reason })
  },
  assign(id: string, driverId: string): Promise<AdminOrder> {
    return api.post(`/admin/orders/${encodeURIComponent(id)}/assign`, { driver_id: driverId })
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
