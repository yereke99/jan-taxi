// Driver order endpoints: available orders, detail, acceptance and the trip.
// Drivers cannot cancel (ARCHITECTURE §22.3): there is no cancel call here;
// a driver with a problem contacts support, and operators cancel or reassign.

import { http, type RequestOptions } from '../services/http'
import { normalizeOrderLoad, normalizeOrderPlace, normalizePassenger, num, rec } from './normalize'
import type { AvailableOrders, CursorList, DriverOrderCard, DriverOrderDetail, DriverOrderSummary } from './types'

function id(orderId: string): string {
  return encodeURIComponent(orderId)
}

/** An available order card with its load (§21) and place always present. */
export function normalizeCard(raw: unknown): DriverOrderCard {
  return { ...(rec(raw) as unknown as DriverOrderCard), ...normalizeOrderLoad(raw), ...normalizeOrderPlace(raw) }
}

/** A driver order view with its load and the passenger contact normalised. */
export function normalizeDetail(raw: unknown): DriverOrderDetail {
  const r = rec(raw)
  return {
    ...(r as unknown as DriverOrderDetail),
    ...normalizeOrderLoad(r),
    ...normalizeOrderPlace(r),
    passenger: normalizePassenger(r.passenger),
    commission_charged: num(r.commission_charged),
    // Drivers cannot cancel (§22.3), whatever an older API says.
    can_cancel: false,
  }
}

export function normalizeSummary(raw: unknown): DriverOrderSummary {
  const r = rec(raw)
  return { ...(r as unknown as DriverOrderSummary), ...normalizeOrderLoad(r), ...normalizeOrderPlace(r), commission_charged: num(r.commission_charged) }
}

export async function listAvailable(opts?: RequestOptions): Promise<AvailableOrders> {
  const res = await http.get<{ items?: unknown[]; eligibility?: AvailableOrders['eligibility'] }>('/driver/orders', opts)
  return {
    items: Array.isArray(res?.items) ? res.items.map(normalizeCard) : [],
    eligibility: res?.eligibility ?? { eligible: false, reason: null },
  }
}

/** The active order, or null when there is none (204). */
export async function getActive(opts?: RequestOptions): Promise<DriverOrderDetail | null> {
  const res = await http.getOptional<unknown>('/driver/orders/active', opts)
  return res ? normalizeDetail(res) : null
}

export async function getOrder(orderId: string, opts?: RequestOptions): Promise<DriverOrderDetail> {
  return normalizeDetail(await http.get<unknown>(`/driver/orders/${id(orderId)}`, opts))
}

export async function listHistory(before: string | null, limit = 20): Promise<CursorList<DriverOrderSummary>> {
  const res = await http.get<{ items?: unknown[]; next_cursor?: string | null }>('/driver/orders/history', { query: { limit, before } })
  return { items: Array.isArray(res?.items) ? res.items.map(normalizeSummary) : [], next_cursor: res?.next_cursor ?? null }
}

/**
 * Takes the order. The commission is charged in the same transaction
 * (§22.2): 402 insufficient_balance when the available balance is short.
 */
export async function acceptOrder(orderId: string): Promise<DriverOrderDetail> {
  return normalizeDetail(await http.post<unknown>(`/driver/orders/${id(orderId)}/accept`))
}

export async function markArriving(orderId: string): Promise<DriverOrderDetail> {
  return normalizeDetail(await http.post<unknown>(`/driver/orders/${id(orderId)}/arriving`))
}

export async function startTrip(orderId: string): Promise<DriverOrderDetail> {
  return normalizeDetail(await http.post<unknown>(`/driver/orders/${id(orderId)}/start`))
}

export async function completeStop(orderId: string, stopId: string): Promise<DriverOrderDetail> {
  return normalizeDetail(await http.post<unknown>(`/driver/orders/${id(orderId)}/stops/${encodeURIComponent(stopId)}/complete`))
}

export async function completeTrip(orderId: string): Promise<DriverOrderDetail> {
  return normalizeDetail(await http.post<unknown>(`/driver/orders/${id(orderId)}/complete`))
}
