// Navigation guard decision, kept free of router/Pinia wiring so it can be
// tested directly: public pages, sign-in redirect, and permission checks.

import type { LocationQuery, RouteLocationRaw, RouteMeta } from 'vue-router'
import { firstAllowedRoute } from '../navigation'
import type { Permission } from '../permissions'

export interface GuardTarget {
  name?: string | symbol | null
  fullPath: string
  query: LocationQuery
  meta: RouteMeta
}

export interface GuardSession {
  isAuthenticated: boolean
  can: (perm: Permission | undefined) => boolean
}

/** Only same-app absolute paths are accepted as post-login redirects. */
export function safeRedirect(v: unknown): string | null {
  return typeof v === 'string' && v.startsWith('/') && !v.startsWith('//') && !v.startsWith('/login') ? v : null
}

export function homeLocation(can: (p: Permission) => boolean): RouteLocationRaw {
  const name = firstAllowedRoute(can)
  return name ? { name } : { name: 'forbidden' }
}

/** true to continue, or where to go instead. */
export function guardDecision(to: GuardTarget, session: GuardSession): true | RouteLocationRaw {
  if (to.meta.public) {
    if (to.name === 'login' && session.isAuthenticated) return safeRedirect(to.query.redirect) ?? homeLocation(session.can)
    return true
  }
  if (!session.isAuthenticated) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  if (to.meta.perm && !session.can(to.meta.perm)) {
    // The dashboard is the default landing page; send operators without it to
    // their first permitted section instead of a dead end.
    if (to.name === 'dashboard') {
      const home = firstAllowedRoute(session.can)
      if (home && home !== 'dashboard') return { name: home }
    }
    return { name: 'forbidden' }
  }
  return true
}
