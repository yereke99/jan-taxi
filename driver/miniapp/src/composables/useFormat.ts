// Formatters bound to the current UI language.

import { computed, type ComputedRef } from 'vue'
import { useI18n } from 'vue-i18n'
import { normalizeLocale, type Locale } from '../i18n/core'
import {
  formatDate,
  formatDateTime,
  formatDistance,
  formatDuration,
  formatMoney,
  formatRelativeTime,
  formatTime,
} from '../utils/format'

export interface Formatters {
  locale: ComputedRef<Locale>
  money: (amount: number) => string
  /** Signed amount for ledger rows: "+2 000 ₸" / "−200 ₸". */
  signedMoney: (amount: number) => string
  distance: (meters: number) => string
  duration: (seconds: number) => string
  relative: (date: string | number | Date) => string
  date: (date: string | number | Date) => string
  dateTime: (date: string | number | Date) => string
  time: (date: string | number | Date) => string
  /** Commission percent from basis points: 1000 → "10%". */
  percent: (bps: number) => string
}

export function useFormat(): Formatters {
  const { locale: i18nLocale } = useI18n()
  const locale = computed(() => normalizeLocale(i18nLocale.value))
  return {
    locale,
    money: (a) => formatMoney(a, locale.value),
    signedMoney: (a) => (a > 0 ? `+${formatMoney(a, locale.value)}` : formatMoney(a, locale.value)),
    distance: (m) => formatDistance(m, locale.value),
    duration: (s) => formatDuration(s, locale.value),
    relative: (d) => formatRelativeTime(d, locale.value),
    date: (d) => formatDate(d, locale.value),
    dateTime: (d) => formatDateTime(d, locale.value),
    time: (d) => formatTime(d),
    percent: (bps) => {
      const v = bps / 100
      const s = Number.isInteger(v) ? String(v) : v.toFixed(1)
      return `${locale.value === 'en' ? s : s.replace('.', ',')}%`
    },
  }
}
