// Entry routing by driver status and per-route access rules.
//
//   no profile / draft / changes_requested → registration (corrections)
//   pending / rejected / suspended         → status screen (explanation + support)
//   approved                               → home

import type { DriverState } from '../stores/session'
import { ROUTE, type RouteName } from './names'

export type Access =
  /** Everyone signed in (support, language). */
  | 'any'
  /** Filling in or correcting the application. */
  | 'registration'
  /** Waiting, rejected or suspended. */
  | 'status'
  /** Has a submitted profile (wallet, profile). */
  | 'registered'
  /** Approved drivers (home, orders, trip). */
  | 'approved'

declare module 'vue-router' {
  interface RouteMeta {
    access?: Access
    /** Main section shown in the floating tab bar. */
    tab?: 'home' | 'orders' | 'wallet' | 'profile'
    /** Hide the tab bar (action screens, registration). */
    hideTabBar?: boolean
  }
}

export function entryRoute(state: DriverState): RouteName {
  switch (state) {
    case 'none':
    case 'draft':
    case 'changes_requested':
      return ROUTE.register
    case 'pending':
    case 'rejected':
    case 'suspended':
      return ROUTE.status
    case 'approved':
      return ROUTE.home
  }
}

export function canAccess(access: Access | undefined, state: DriverState): boolean {
  switch (access ?? 'any') {
    case 'any':
      return true
    case 'registration':
      return state === 'none' || state === 'draft' || state === 'changes_requested'
    case 'status':
      return state === 'pending' || state === 'rejected' || state === 'suspended'
    case 'registered':
      return state !== 'none' && state !== 'draft'
    case 'approved':
      return state === 'approved'
  }
}
