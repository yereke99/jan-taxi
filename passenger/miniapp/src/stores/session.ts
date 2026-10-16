// Session bootstrap for the passenger app: public config, Telegram
// authentication, the current user (GET /me, a flat object) and the UI
// language.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getMe, updateLanguage, type Me } from '../api/me'
import { i18n } from '../i18n'
import { applyLocale, isLocale, type Locale } from '../i18n/core'
import { authenticate } from '../services/auth'
import { defaultPublicConfig, fetchPublicConfig, type PublicConfig } from '../services/config'
import { isApiError } from '../services/http'

export type SessionStatus = 'idle' | 'loading' | 'ready' | 'outside_telegram' | 'error'

export const useSessionStore = defineStore('session', () => {
  const status = ref<SessionStatus>('idle')
  const error = shallowRef<unknown>(null)
  const me = ref<Me | null>(null)
  const config = ref<PublicConfig>(defaultPublicConfig())
  const configLoaded = ref(false)
  const language = ref<Locale>('kk')
  const savingLanguage = ref(false)
  let bootstrapping: Promise<void> | null = null

  const isReady = computed(() => status.value === 'ready')
  const maxRoutePoints = computed(() => config.value.routing.max_points)

  async function loadConfig(): Promise<void> {
    try {
      config.value = await fetchPublicConfig('passenger')
      configLoaded.value = true
    } catch (err) {
      // The app still works with documented defaults (map style, Embi centre).
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
      await authenticate('passenger')
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
    return bootstrapping
  }

  /** Re-reads the public config (support contacts, links) when it failed at start. */
  async function ensureConfig(): Promise<void> {
    if (!configLoaded.value) await loadConfig()
  }

  /** Persists the language server-side first, then switches the UI. */
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
    maxRoutePoints,
    bootstrap,
    ensureConfig,
    setLanguage,
  }
})
