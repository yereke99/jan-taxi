// Admin session: login/logout, the current administrator (/admin/auth/me) and
// permission checks for navigation and route guards.

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, isApiError } from '../services/api'
import { getCsrfToken, setCsrfToken } from '../services/csrf'
import { isPermission, isRole, ROLE_PERMISSIONS, type Permission, type Role } from '../permissions'

export interface AdminProfile {
  id: string
  email: string
  full_name: string
  role: Role
  permissions: Permission[]
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/**
 * Maps the login and /me payloads ({admin:{...}, csrf_token} or the profile
 * itself) onto AdminProfile. Unknown roles get no permissions.
 */
export function normalizeAdmin(raw: unknown): { admin: AdminProfile | null; csrf: string | null } {
  const r = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  const a = r.admin && typeof r.admin === 'object' ? (r.admin as Record<string, unknown>) : r
  const csrf = str(r.csrf_token) || null
  const role = a.role
  if (!str(a.id) || !isRole(role)) return { admin: null, csrf }
  const perms = Array.isArray(a.permissions) ? a.permissions.filter(isPermission) : [...ROLE_PERMISSIONS[role]]
  return {
    admin: { id: str(a.id), email: str(a.email), full_name: str(a.full_name), role, permissions: perms },
    csrf,
  }
}

export const useAuthStore = defineStore('auth', () => {
  const admin = ref<AdminProfile | null>(null)
  /** True once the initial /me check finished (success or not). */
  const loaded = ref(false)
  let bootstrapping: Promise<void> | null = null

  const isAuthenticated = computed(() => admin.value !== null)
  const permissions = computed(() => new Set<Permission>(admin.value?.permissions ?? []))

  function can(perm: Permission | undefined): boolean {
    return perm === undefined || permissions.value.has(perm)
  }

  function apply(raw: unknown): void {
    const { admin: profile, csrf } = normalizeAdmin(raw)
    admin.value = profile
    if (csrf) setCsrfToken(csrf)
  }

  /** Restores the session from the cookie. Safe to call repeatedly. */
  function bootstrap(): Promise<void> {
    bootstrapping ??= (async () => {
      try {
        apply(await api.get<unknown>('/admin/auth/me'))
      } catch (err) {
        if (!isApiError(err) || err.status !== 401) console.warn('[auth] session check failed', err)
        admin.value = null
      } finally {
        loaded.value = true
        bootstrapping = null
      }
    })()
    return bootstrapping
  }

  /** Signs in; rejects with ApiError (e.g. invalid credentials, too_many_requests). */
  async function login(email: string, password: string): Promise<void> {
    const res = await api.post<unknown>('/admin/auth/login', { email: email.trim(), password })
    apply(res)
    if (!admin.value) throw new Error('login response has no admin profile')
    if (!getCsrfToken()) console.warn('[auth] login response carried no csrf_token')
    loaded.value = true
  }

  /** Ends the session server-side (best effort) and locally. */
  async function logout(): Promise<void> {
    try {
      await api.post('/admin/auth/logout')
    } catch (err) {
      if (!isApiError(err) || err.status !== 401) console.warn('[auth] logout failed', err)
    } finally {
      clear()
    }
  }

  /** Drops the local session (after a 401 or logout). */
  function clear(): void {
    admin.value = null
    setCsrfToken(null)
  }

  return { admin, loaded, isAuthenticated, permissions, can, bootstrap, login, logout, clear }
})
