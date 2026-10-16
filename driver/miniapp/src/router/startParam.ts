// Driver start parameters (ARCHITECTURE §18). Parameters are navigation hints
// only: every screen re-fetches its data with full authorisation, and the
// router's access rules still apply (e.g. "orders" for a pending driver lands
// on the status screen).

import type { RouteLocationRaw, Router } from 'vue-router'
import { isUuid } from '../utils/id'
import { ROUTE } from './names'

export type DriverStartIntent =
  | { kind: 'home' }
  | { kind: 'register' }
  | { kind: 'orders' }
  | { kind: 'order'; orderId: string }
  | { kind: 'trip' }
  | { kind: 'wallet' }
  | { kind: 'topup' }
  | { kind: 'topup_detail'; topupId: string }
  | { kind: 'profile' }
  | { kind: 'support' }

/** §18: `^[A-Za-z0-9_-]{1,64}$`. Anything else is ignored. */
export const START_PARAM_RE = /^[A-Za-z0-9_-]{1,64}$/

function withId(param: string, prefix: string): string | null {
  if (!param.startsWith(prefix)) return null
  const id = param.slice(prefix.length)
  return isUuid(id) ? id.toLowerCase() : null
}

export function parseStartParam(param: string | null | undefined): DriverStartIntent {
  if (!param || !START_PARAM_RE.test(param)) return { kind: 'home' }
  switch (param) {
    case 'register':
    case 'orders':
    case 'trip':
    case 'wallet':
    case 'topup':
    case 'profile':
    case 'support':
      return { kind: param }
  }
  const orderId = withId(param, 'order_')
  if (orderId) return { kind: 'order', orderId }
  const topupId = withId(param, 'topup_')
  if (topupId) return { kind: 'topup_detail', topupId }
  return { kind: 'home' }
}

/** Route for an intent; unknown route names fall back to home. */
export function intentLocation(intent: DriverStartIntent, router: Pick<Router, 'hasRoute'>): RouteLocationRaw {
  const home: RouteLocationRaw = { name: ROUTE.home }
  const to = (name: string, params?: Record<string, string>): RouteLocationRaw =>
    router.hasRoute(name) ? (params ? { name, params } : { name }) : home
  switch (intent.kind) {
    case 'home':
      return home
    case 'register':
      return to(ROUTE.register)
    case 'orders':
      return to(ROUTE.orders)
    case 'order':
      return to(ROUTE.order, { id: intent.orderId })
    case 'trip':
      return to(ROUTE.trip)
    case 'wallet':
      return to(ROUTE.wallet)
    case 'topup':
      return to(ROUTE.topup)
    case 'topup_detail':
      return to(ROUTE.topupDetail, { id: intent.topupId })
    case 'profile':
      return to(ROUTE.profile)
    case 'support':
      return to(ROUTE.support)
  }
}
