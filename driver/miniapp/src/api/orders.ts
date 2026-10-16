// Driver order endpoints: available orders, detail, acceptance and the trip.

import { http, type RequestOptions } from '../services/http'
import type { AvailableOrders, CursorList, DriverOrderDetail, DriverOrderSummary } from './types'

function id(orderId: string): string {
  return encodeURIComponent(orderId)
}

export async function listAvailable(opts?: RequestOptions): Promise<AvailableOrders> {
  const res = await http.get<AvailableOrders>('/driver/orders', opts)
  return {
    items: Array.isArray(res?.items) ? res.items : [],
    eligibility: res?.eligibility ?? { eligible: false, reason: null },
  }
}

/** The active order, or null when there is none (204). */
export function getActive(opts?: RequestOptions): Promise<DriverOrderDetail | null> {
  return http.getOptional<DriverOrderDetail>('/driver/orders/active', opts)
}

export function getOrder(orderId: string, opts?: RequestOptions): Promise<DriverOrderDetail> {
  return http.get<DriverOrderDetail>(`/driver/orders/${id(orderId)}`, opts)
}

export async function listHistory(before: string | null, limit = 20): Promise<CursorList<DriverOrderSummary>> {
  const res = await http.get<CursorList<DriverOrderSummary>>('/driver/orders/history', { query: { limit, before } })
  return { items: Array.isArray(res?.items) ? res.items : [], next_cursor: res?.next_cursor ?? null }
}

export function acceptOrder(orderId: string): Promise<DriverOrderDetail> {
  return http.post<DriverOrderDetail>(`/driver/orders/${id(orderId)}/accept`)
}

export function markArriving(orderId: string): Promise<DriverOrderDetail> {
  return http.post<DriverOrderDetail>(`/driver/orders/${id(orderId)}/arriving`)
}

export function startTrip(orderId: string): Promise<DriverOrderDetail> {
  return http.post<DriverOrderDetail>(`/driver/orders/${id(orderId)}/start`)
}

export function completeStop(orderId: string, stopId: string): Promise<DriverOrderDetail> {
  return http.post<DriverOrderDetail>(`/driver/orders/${id(orderId)}/stops/${encodeURIComponent(stopId)}/complete`)
}

export function completeTrip(orderId: string): Promise<DriverOrderDetail> {
  return http.post<DriverOrderDetail>(`/driver/orders/${id(orderId)}/complete`)
}

export function cancelOrder(orderId: string, reason: string): Promise<DriverOrderDetail> {
  return http.post<DriverOrderDetail>(`/driver/orders/${id(orderId)}/cancel`, { reason })
}
