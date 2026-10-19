// Passenger order endpoints (§19): create, list, detail, cancel, rate,
// dispute and the assigned driver's photo.

import { http } from '../services/http'
import { parseOrder, parseOrderPage } from './parse'
import type { CancelOrderRequest, CursorPage, Order, OrderCreateRequest, OrderScope, OrderSummary } from './types'

export const ORDERS_PAGE_SIZE = 20

export async function createOrder(req: OrderCreateRequest): Promise<Order> {
  return parseOrder(await http.post<unknown>('/orders', req))
}

export async function listOrders(scope: OrderScope, opts: { before?: string | null; limit?: number; signal?: AbortSignal } = {}): Promise<CursorPage<OrderSummary>> {
  const raw = await http.get<unknown>('/orders', {
    query: { scope, limit: opts.limit ?? ORDERS_PAGE_SIZE, before: opts.before ?? undefined },
    signal: opts.signal,
  })
  return parseOrderPage(raw)
}

export async function getOrder(id: string, signal?: AbortSignal): Promise<Order> {
  return parseOrder(await http.get<unknown>(`/orders/${encodeURIComponent(id)}`, { signal }))
}

/** Cancels with the passenger's reason (§22.3); the comment is sent only when given. */
export async function cancelOrder(id: string, req: CancelOrderRequest): Promise<Order> {
  const comment = req.comment?.trim()
  const body: CancelOrderRequest = comment ? { reason_code: req.reason_code, comment } : { reason_code: req.reason_code }
  return parseOrder(await http.post<unknown>(`/orders/${encodeURIComponent(id)}/cancel`, body))
}

export async function rateOrder(id: string, rating: number, comment?: string): Promise<Order> {
  const body = comment ? { rating, comment } : { rating }
  return parseOrder(await http.post<unknown>(`/orders/${encodeURIComponent(id)}/rating`, body))
}

export async function disputeOrder(id: string, reason: string): Promise<Order> {
  return parseOrder(await http.post<unknown>(`/orders/${encodeURIComponent(id)}/dispute`, { reason }))
}

/** The assigned driver's avatar as a Blob (private JPEG, needs the bearer token). */
export function fetchDriverPhoto(orderId: string, signal?: AbortSignal): Promise<Blob> {
  return http.getBlob(`/orders/${encodeURIComponent(orderId)}/driver-photo`, { signal })
}
