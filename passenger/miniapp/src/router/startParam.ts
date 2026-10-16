// Passenger start parameters (ARCHITECTURE §18). Parameters are navigation
// hints only: every screen re-fetches its data with full authorisation.
// Anything that is not exactly one of the documented values is ignored.

import type { RouteLocationRaw } from 'vue-router'
import { isUuid } from '../utils/id'
import type { Category } from '../utils/status'
import { ROUTE } from './names'

/** Telegram's start parameter alphabet and length (§18). */
export const START_PARAM_RE = /^[A-Za-z0-9_-]{1,64}$/
const ORDER_PARAM_RE = /^order_([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$/

export type PassengerStartIntent =
  | { kind: 'home' }
  | { kind: 'book'; category: Category }
  | { kind: 'orders' }
  | { kind: 'order'; orderId: string }
  | { kind: 'support' }
  | { kind: 'become_driver' }

export function parseStartParam(param: string | null | undefined): PassengerStartIntent {
  if (typeof param !== 'string' || !START_PARAM_RE.test(param)) return { kind: 'home' }
  switch (param) {
    case 'taxi':
    case 'parcel':
    case 'other':
      return { kind: 'book', category: param }
    case 'orders':
      return { kind: 'orders' }
    case 'support':
      return { kind: 'support' }
    case 'driver':
      return { kind: 'become_driver' }
  }
  const m = ORDER_PARAM_RE.exec(param)
  if (m && isUuid(m[1]!)) return { kind: 'order', orderId: m[1]!.toLowerCase() }
  return { kind: 'home' }
}

/** The route a start intent opens. */
export function intentLocation(intent: PassengerStartIntent): RouteLocationRaw {
  switch (intent.kind) {
    case 'home':
      return { name: ROUTE.home }
    case 'book':
      return { name: ROUTE.book, query: { category: intent.category } }
    case 'orders':
      return { name: ROUTE.orders }
    case 'order':
      return { name: ROUTE.order, params: { id: intent.orderId } }
    case 'support':
      return { name: ROUTE.support }
    case 'become_driver':
      return { name: ROUTE.becomeDriver }
  }
}
