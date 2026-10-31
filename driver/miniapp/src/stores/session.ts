// Session bootstrap for the driver app: public config (with the driver-only
// extras: vehicle classes, commission policy, welcome bonus), Telegram
// authentication, the current user with the driver summary (GET /me, a flat
// object) and the UI language.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getMe, updateLanguage, type Me, type MeDriver } from '../api/me'
import { i18n } from '../i18n'
import { applyLocale, isLocale, type Locale } from '../i18n/core'
import { authenticate } from '../services/auth'
import { emptyDriverExtras, normalizeDriverExtras, type DriverConfigExtras } from '../features/vehicle/classes'
import { defaultPublicConfig, normalizePublicConfig, type PublicConfig } from '../services/config'
import { http, isApiError } from '../services/http'
import type { ModerationStatus } from '../utils/status'

export type SessionStatus = 'idle' | 'loading' | 'ready' | 'outside_telegram' | 'error'

/** Driver profile status, or 'none' before the first registration save. */
export type DriverState = ModerationStatus | 'none'

export const useSessionStore = defineStore('session', () => {
  const status = ref<SessionStatus>('idle')
  const error = shallowRef<unknown>(null)
  const me = ref<Me | null>(null)
  const config = ref<PublicConfig>(defaultPublicConfig())
  const extras = ref<DriverConfigExtras>(emptyDriverExtras())
  const configLoaded = ref(false)
  const configLoading = ref(false)
  const language = ref<Locale>('kk')
  const savingLanguage = ref(false)

  let settled: Promise<void> = Promise.resolve()
  let bootstrapping: Promise<void> | null = null

  const isReady = computed(() => status.value === 'ready')
  const driver = computed(() => me.value?.driver ?? null)
  const driverState = computed<DriverState>(() => driver.value?.status ?? 'none')
  const isApproved = computed(() => driverState.value === 'approved')

  async function loadConfig(): Promise<void> {
    configLoading.value = true
    try {
      // One request: the shared fields go through the shared normaliser, the
      // driver-only ones (§21, §22) through features/vehicle.
      const raw = await http.get<unknown>('/public/config', { query: { app: 'driver' } })
      config.value = normalizePublicConfig(raw)
      extras.value = normalizeDriverExtras(raw)
      configLoaded.value = true
    } catch (err) {
      // Map and navigation fall back to documented defaults; top-ups and the
      // vehicle class step wait for a retry.
      console.warn('[session] public config unavailable, using defaults', err)
    } finally {
      configLoading.value = false
    }
  }

  function applyMe(next: Me): void {
    me.value = next
    language.value = next.language
    applyLocale(i18n, next.language)
  }

  async function run(): Promise<void> {
    status.value = 'loading'
    error.value = null
    const configTask = loadConfig()
    try {
      await authenticate('driver')
      applyMe(await getMe())
      status.value = 'ready'
    } catch (err) {
      error.value = err
      status.value = isApiError(err) && err.code === 'not_in_telegram' ? 'outside_telegram' : 'error'
    }
    await configTask
  }

  /** Signs in and loads the user; safe to call again after a failure. */
  function bootstrap(): Promise<void> {
    bootstrapping ??= run().finally(() => {
      bootstrapping = null
    })
    settled = bootstrapping
    return bootstrapping
  }

  /** Resolves once the current bootstrap attempt has finished (successfully or not). */
  function whenSettled(): Promise<void> {
    return settled
  }

  /** Re-reads /me (moderation decisions, online state changed elsewhere). */
  async function refreshMe(): Promise<void> {
    if (!isReady.value) return
    try {
      applyMe(await getMe())
    } catch (err) {
      console.warn('[session] /me refresh failed', err)
    }
  }

  /**
   * A driver route answered that the profile is missing or not approved
   * (403 driver_not_registered / driver_not_approved): the cached status is
   * out of date. Re-reading /me lets the runtime move the driver to the right
   * screen. Returns true for such errors so callers can skip their own message.
   */
  function handleAccessError(err: unknown): boolean {
    if (!isApiError(err) || err.status !== 403) return false
    if (err.code !== 'driver_not_registered' && err.code !== 'driver_not_approved') return false
    void refreshMe()
    return true
  }

  /** Keeps the cached driver summary in step with a known online state. */
  function setOnline(online: boolean): void {
    if (me.value?.driver) me.value.driver.is_online = online
  }

  /**
   * Takes a driver summary the API has just returned (the registration was
   * finished and approved in that request) before GET /me confirms it, so the
   * router lets the driver into the approved screens at once.
   */
  function adoptDriver(driver: MeDriver): void {
    if (me.value) me.value = { ...me.value, driver }
  }

  async function refreshConfig(): Promise<void> {
    await loadConfig()
  }

  /** Persists the language server-side first (shared with the bot), then switches the UI. */
  async function setLanguage(next: Locale): Promise<void> {
    if (!isLocale(next) || next === language.value) return
    savingLanguage.value = true
    try {
      applyMe(await updateLanguage(next))
    } finally {
      savingLanguage.value = false
    }
  }

  return {
    status,
    error,
    me,
    config,
    extras,
    configLoaded,
    configLoading,
    language,
    savingLanguage,
    isReady,
    driver,
    driverState,
    isApproved,
    bootstrap,
    whenSettled,
    refreshMe,
    handleAccessError,
    refreshConfig,
    setOnline,
    adoptDriver,
    setLanguage,
  }
})
