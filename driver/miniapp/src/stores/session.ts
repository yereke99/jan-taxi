// Session bootstrap for the driver app: public config, Telegram
// authentication, the current user with the driver summary (GET /me, a flat
// object) and the UI language.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getMe, updateLanguage, type Me } from '../api/me'
import { i18n } from '../i18n'
import { applyLocale, isLocale, type Locale } from '../i18n/core'
import { authenticate } from '../services/auth'
import { defaultPublicConfig, fetchPublicConfig, type PublicConfig } from '../services/config'
import { isApiError } from '../services/http'
import type { ModerationStatus } from '../utils/status'

export type SessionStatus = 'idle' | 'loading' | 'ready' | 'outside_telegram' | 'error'

/** Driver profile status, or 'none' before the first registration save. */
export type DriverState = ModerationStatus | 'none'

export const useSessionStore = defineStore('session', () => {
  const status = ref<SessionStatus>('idle')
  const error = shallowRef<unknown>(null)
  const me = ref<Me | null>(null)
  const config = ref<PublicConfig>(defaultPublicConfig())
  const configLoaded = ref(false)
  const language = ref<Locale>('kk')
  const savingLanguage = ref(false)

  let settled: Promise<void> = Promise.resolve()
  let bootstrapping: Promise<void> | null = null

  const isReady = computed(() => status.value === 'ready')
  const driver = computed(() => me.value?.driver ?? null)
  const driverState = computed<DriverState>(() => driver.value?.status ?? 'none')
  const isApproved = computed(() => driverState.value === 'approved')

  async function loadConfig(): Promise<void> {
    try {
      config.value = await fetchPublicConfig('driver')
      configLoaded.value = true
    } catch (err) {
      // Map and navigation fall back to documented defaults; top-ups wait for a retry.
      console.warn('[session] public config unavailable, using defaults', err)
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

  /** Keeps the cached driver summary in step with a known online state. */
  function setOnline(online: boolean): void {
    if (me.value?.driver) me.value.driver.is_online = online
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
    configLoaded,
    language,
    savingLanguage,
    isReady,
    driver,
    driverState,
    isApproved,
    bootstrap,
    whenSettled,
    refreshMe,
    refreshConfig,
    setOnline,
    setLanguage,
  }
})
