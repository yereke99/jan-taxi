// Locale-bound formatters for templates: money, dates, distances and the
// localized names the API returns as {kk, ru, en}.

import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LocalizedText } from '../api/common'
import {
  formatDate,
  formatDateTime,
  formatDistance,
  formatDuration,
  formatMoney,
  formatRelativeTime,
  type Locale,
} from '../utils/format'

export function useFormat() {
  const { locale } = useI18n()
  const loc = computed<Locale>(() => (locale.value === 'ru' || locale.value === 'en' ? locale.value : 'kk'))

  return {
    locale: loc,
    money: (n: number | null | undefined): string => (n === null || n === undefined ? '—' : formatMoney(n, loc.value)),
    /** Signed money with an explicit plus for credits. */
    signedMoney: (n: number | null | undefined): string => {
      if (n === null || n === undefined) return '—'
      const s = formatMoney(n, loc.value)
      return n > 0 ? `+${s}` : s
    },
    dateTime: (s: string | null | undefined): string => (s ? formatDateTime(s, loc.value) : '—'),
    date: (s: string | null | undefined): string => (s ? formatDate(s, loc.value) : '—'),
    relative: (s: string | null | undefined): string => (s ? formatRelativeTime(s, loc.value) : '—'),
    distance: (m: number | null | undefined): string => (m === null || m === undefined ? '—' : formatDistance(m, loc.value)),
    duration: (sec: number | null | undefined): string => (sec === null || sec === undefined ? '—' : formatDuration(sec, loc.value)),
    name: (t: LocalizedText | null | undefined): string => (t ? t[loc.value] || t.kk || t.ru || t.en : '—'),
    count: (n: number | null | undefined): string =>
      n === null || n === undefined ? '—' : new Intl.NumberFormat(loc.value === 'en' ? 'en-US' : 'ru-RU').format(n),
    percentFromBps: (bps: number | null | undefined): string =>
      bps === null || bps === undefined ? '—' : `${(bps / 100).toFixed(bps % 100 === 0 ? 0 : 2).replace('.', loc.value === 'en' ? '.' : ',')}%`,
  }
}

export type Formatters = ReturnType<typeof useFormat>
