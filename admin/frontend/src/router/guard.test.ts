import { describe, expect, it } from 'vitest'
import type { RouteMeta } from 'vue-router'
import { ROLE_PERMISSIONS, type Permission, type Role } from '../permissions'
import { guardDecision, type GuardSession, type GuardTarget } from './guard'

function session(role: Role | null): GuardSession {
  const perms = new Set<Permission>(role ? ROLE_PERMISSIONS[role] : [])
  return { isAuthenticated: role !== null, can: (p) => p === undefined || perms.has(p) }
}

function target(name: string, fullPath: string, meta: RouteMeta, query: Record<string, string> = {}): GuardTarget {
  return { name, fullPath, meta, query }
}

describe('guardDecision', () => {
  it('sends anonymous visitors to the login page with a redirect back', () => {
    const to = target('topups', '/topups?status=pending_verification', { perm: 'finance.view' })
    expect(guardDecision(to, session(null))).toEqual({ name: 'login', query: { redirect: '/topups?status=pending_verification' } })
    expect(guardDecision(target('dashboard', '/', { perm: 'dashboard.view' }), session(null))).toEqual({ name: 'login', query: {} })
  })

  it('lets public pages through and moves signed-in operators off the login page', () => {
    expect(guardDecision(target('not-found', '/x', { public: true }), session(null))).toBe(true)
    expect(guardDecision(target('login', '/login', { public: true }, { redirect: '/orders' }), session('operator'))).toBe('/orders')
    expect(guardDecision(target('login', '/login', { public: true }, { redirect: '//evil.example' }), session('operator'))).toEqual({ name: 'dashboard' })
  })

  it('allows a route when the role has its permission', () => {
    expect(guardDecision(target('topups', '/topups', { perm: 'finance.view' }), session('finance_manager'))).toBe(true)
    expect(guardDecision(target('moderation', '/moderation', { perm: 'drivers.moderate' }), session('driver_moderator'))).toBe(true)
    expect(guardDecision(target('admins', '/admins', { perm: 'admins.manage' }), session('super_admin'))).toBe(true)
  })

  it('shows the no-permission page for routes outside the role', () => {
    expect(guardDecision(target('topups', '/topups', { perm: 'finance.view' }), session('operator'))).toEqual({ name: 'forbidden' })
    expect(guardDecision(target('settings', '/settings', { perm: 'config.manage' }), session('finance_manager'))).toEqual({ name: 'forbidden' })
    expect(guardDecision(target('admins', '/admins', { perm: 'admins.manage' }), session('auditor'))).toEqual({ name: 'forbidden' })
    expect(guardDecision(target('moderation', '/moderation', { perm: 'drivers.moderate' }), session('operator'))).toEqual({ name: 'forbidden' })
  })

  it('sends operators without the dashboard to their first permitted section', () => {
    const limited: GuardSession = { isAuthenticated: true, can: (p) => p === undefined || p === 'audit.view' }
    expect(guardDecision(target('dashboard', '/', { perm: 'dashboard.view' }), limited)).toEqual({ name: 'audit' })
    const none: GuardSession = { isAuthenticated: true, can: (p) => p === undefined }
    expect(guardDecision(target('dashboard', '/', { perm: 'dashboard.view' }), none)).toEqual({ name: 'forbidden' })
  })
})
