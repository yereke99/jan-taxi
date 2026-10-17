// Admin i18n: composition API, Kazakh default and fallback. The operator's
// language choice is a per-browser preference (localStorage).

import { createI18n, useI18n } from 'vue-i18n'
import { isApiError } from '../services/api'
import type { Locale } from '../utils/format'
import { en, kk, ru } from './messages'

export type { Locale } from '../utils/format'

export const LOCALES: readonly Locale[] = ['kk', 'ru', 'en']
export const DEFAULT_LOCALE: Locale = 'kk'
const STORAGE_KEY = 'jt.admin.locale'

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (LOCALES as readonly string[]).includes(v)
}

function storedLocale(): Locale {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return isLocale(v) ? v : DEFAULT_LOCALE
  } catch {
    return DEFAULT_LOCALE
  }
}

export const i18n = createI18n({
  legacy: false,
  locale: storedLocale(),
  fallbackLocale: DEFAULT_LOCALE,
  messages: { kk, ru, en },
  missingWarn: import.meta.env.DEV,
  fallbackWarn: false,
  // Messages are rendered by interpolation only (never v-html); some describe Telegram HTML tags.
  warnHtmlMessage: false,
})

if (typeof document !== 'undefined') document.documentElement.lang = i18n.global.locale.value

export function setLocale(locale: Locale): void {
  i18n.global.locale.value = locale
  document.documentElement.lang = locale
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // Preference only; ignore when storage is unavailable.
  }
}

/** User-facing sentence for an error thrown by the API client. */
export function useErrorText(): (err: unknown) => string {
  const { t, te } = useI18n()
  return (err) => {
    if (isApiError(err)) {
      const key = `errors.${err.code}`
      if (te(key, DEFAULT_LOCALE)) return t(key)
    }
    return t('errors.unknown')
  }
}
