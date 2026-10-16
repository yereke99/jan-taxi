// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// vue-i18n setup shared by both Mini Apps: composition API (legacy: false),
// Kazakh as default and fallback, core messages merged with app messages.

import { createI18n, useI18n } from 'vue-i18n'
import { isApiError } from '../../services/http'
import { LocationError } from '../../telegram/location'
import type { Locale } from '../../utils/format'
import { en } from './messages/en'
import { kk, type CoreMessages } from './messages/kk'
import { ru } from './messages/ru'

export type { Locale } from '../../utils/format'
export type { CoreMessages } from './messages/kk'

export const LOCALES: readonly Locale[] = ['kk', 'ru', 'en']
export const DEFAULT_LOCALE: Locale = 'kk'

export interface MessageTree {
  [key: string]: string | MessageTree
}

export const coreMessages: Record<Locale, CoreMessages> = { kk, ru, en }

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (LOCALES as readonly string[]).includes(v)
}

/** Maps an API/Telegram language code ("ru", "en-US", "kk") to a supported locale. */
export function normalizeLocale(code: string | null | undefined): Locale {
  const base = (code ?? '').toLowerCase().split(/[-_]/)[0]
  return isLocale(base) ? base : DEFAULT_LOCALE
}

function merge(base: MessageTree, extra: MessageTree | undefined): MessageTree {
  if (!extra) return base
  const out: MessageTree = { ...base }
  for (const [k, v] of Object.entries(extra)) {
    const prev = out[k]
    out[k] = typeof v === 'object' && typeof prev === 'object' ? merge(prev, v) : v
  }
  return out
}

/**
 * Creates the app's i18n instance with core + app messages. App message
 * objects should mirror their own kk schema the same way core does.
 */
export function createI18nInstance(appMessages: Partial<Record<Locale, MessageTree>> = {}) {
  const messages = {
    kk: merge(kk as unknown as MessageTree, appMessages.kk),
    ru: merge(ru as unknown as MessageTree, appMessages.ru),
    en: merge(en as unknown as MessageTree, appMessages.en),
  }
  return createI18n({
    legacy: false,
    locale: DEFAULT_LOCALE,
    fallbackLocale: DEFAULT_LOCALE,
    messages,
    missingWarn: import.meta.env.DEV,
    fallbackWarn: false,
  })
}

export type AppI18n = ReturnType<typeof createI18nInstance>

/** Switches the UI language and the document's lang attribute. */
export function applyLocale(i18n: AppI18n, locale: Locale): void {
  i18n.global.locale.value = locale
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

interface Translator {
  t: (key: string, named?: Record<string, unknown>) => string
  te: (key: string, locale?: string) => boolean
}

/** User-facing sentence for any error thrown by services (ApiError, LocationError, other). */
export function errorText(err: unknown, i18n: Translator): string {
  if (isApiError(err)) {
    const key = `errors.${err.code}`
    return i18n.te(key, DEFAULT_LOCALE) ? i18n.t(key) : i18n.t('errors.unknown')
  }
  if (err instanceof LocationError) return i18n.t(`gps.${err.code}`)
  return i18n.t('errors.unknown')
}

/** Composable form of errorText bound to the current component's i18n. */
export function useErrorText(): (err: unknown) => string {
  const { t, te } = useI18n()
  return (err) => errorText(err, { t: (k, n) => (n ? t(k, n) : t(k)), te: (k, l) => (l ? te(k, l) : te(k)) })
}

/** Short message for a per-field validation reason (ApiError.fields value). */
export function fieldErrorText(reason: string, i18n: Translator): string {
  const key = `field_errors.${reason}`
  return i18n.te(key, DEFAULT_LOCALE) ? i18n.t(key) : i18n.t('field_errors.invalid')
}

/** Composable form of fieldErrorText bound to the current component's i18n. */
export function useFieldErrorText(): (reason: string) => string {
  const { t, te } = useI18n()
  return (reason) => fieldErrorText(reason, { t: (k, n) => (n ? t(k, n) : t(k)), te: (k, l) => (l ? te(k, l) : te(k)) })
}
