// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Locale-aware formatting for money, distance, duration and relative time.
// Implemented by hand instead of Intl because Kazakh locale data is missing
// from some Telegram WebViews, and the output must be identical everywhere.

export type Locale = 'kk' | 'ru' | 'en'

/** No-break space between number groups and before units. */
const NBSP = String.fromCharCode(0xa0)

function groupThousands(n: number, separator: string): string {
  const digits = Math.abs(Math.trunc(n)).toString()
  let out = ''
  for (let i = 0; i < digits.length; i++) {
    if (i > 0 && (digits.length - i) % 3 === 0) out += separator
    out += digits[i]
  }
  return (n < 0 ? '−' : '') + out
}

/** Formats an integer KZT amount: "1 500 ₸" (kk/ru), "1,500 ₸" (en). */
export function formatMoney(amount: number, locale: Locale): string {
  if (!Number.isFinite(amount)) return '—'
  const separator = locale === 'en' ? ',' : NBSP
  return `${groupThousands(Math.round(amount), separator)}${NBSP}₸`
}

function decimal(value: number, digits: number, locale: Locale): string {
  const s = value.toFixed(digits)
  return locale === 'en' ? s : s.replace('.', ',')
}

const DISTANCE_UNITS: Record<Locale, { m: string; km: string }> = {
  kk: { m: 'м', km: 'км' },
  ru: { m: 'м', km: 'км' },
  en: { m: 'm', km: 'km' },
}

/**
 * Formats a distance in metres: "850 м", "2,4 км", "120 км" (kk/ru) and
 * "850 m", "2.4 km" (en). Under 1 km rounds to 10 m; from 100 km whole km.
 */
export function formatDistance(meters: number, locale: Locale): string {
  if (!Number.isFinite(meters) || meters < 0) return '—'
  const u = DISTANCE_UNITS[locale]
  if (meters < 1000) {
    const rounded = Math.max(10, Math.round(meters / 10) * 10)
    if (rounded < 1000) return `${rounded}${NBSP}${u.m}`
  }
  const km = meters / 1000
  if (km >= 100) return `${groupThousands(Math.round(km), locale === 'en' ? ',' : NBSP)}${NBSP}${u.km}`
  return `${decimal(Math.round(km * 10) / 10, 1, locale)}${NBSP}${u.km}`
}

const DURATION_UNITS: Record<Locale, { min: string; h: string }> = {
  kk: { min: 'мин', h: 'сағ' },
  ru: { min: 'мин', h: 'ч' },
  en: { min: 'min', h: 'h' },
}

/** Formats a duration in seconds: "12 мин", "1 сағ 5 мин" / "1 ч 5 мин" / "1 h 5 min". */
export function formatDuration(seconds: number, locale: Locale): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const u = DURATION_UNITS[locale]
  const totalMin = Math.max(1, Math.round(seconds / 60))
  if (totalMin < 60) return `${totalMin}${NBSP}${u.min}`
  const h = Math.floor(totalMin / 60)
  const m = totalMin % 60
  return m === 0 ? `${h}${NBSP}${u.h}` : `${h}${NBSP}${u.h} ${m}${NBSP}${u.min}`
}

interface RelativeWords {
  justNow: string
  past: (n: number, unit: 'min' | 'h' | 'd') => string
  future: (n: number, unit: 'min' | 'h' | 'd') => string
  yesterday: string
  tomorrow: string
}

const RELATIVE: Record<Locale, RelativeWords> = {
  kk: {
    justNow: 'жаңа ғана',
    past: (n, unit) => `${n}${NBSP}${{ min: 'минут', h: 'сағат', d: 'күн' }[unit]} бұрын`,
    future: (n, unit) => `${n}${NBSP}${{ min: 'минуттан', h: 'сағаттан', d: 'күннен' }[unit]} кейін`,
    yesterday: 'кеше',
    tomorrow: 'ертең',
  },
  ru: {
    justNow: 'только что',
    past: (n, unit) => `${n}${NBSP}${{ min: 'мин', h: 'ч', d: 'дн.' }[unit]} назад`,
    future: (n, unit) => `через ${n}${NBSP}${{ min: 'мин', h: 'ч', d: 'дн.' }[unit]}`,
    yesterday: 'вчера',
    tomorrow: 'завтра',
  },
  en: {
    justNow: 'just now',
    past: (n, unit) => `${n}${NBSP}${{ min: 'min', h: 'h', d: 'd' }[unit]} ago`,
    future: (n, unit) => `in ${n}${NBSP}${{ min: 'min', h: 'h', d: 'd' }[unit]}`,
    yesterday: 'yesterday',
    tomorrow: 'tomorrow',
  },
}

/**
 * Short relative time ("5 мин бұрын", "через 3 ч", "yesterday").
 * `date` is a Date, epoch milliseconds or an RFC 3339 string.
 */
export function formatRelativeTime(date: Date | number | string, locale: Locale, now: number = Date.now()): string {
  const t = toMillis(date)
  if (t === null) return '—'
  const w = RELATIVE[locale]
  const diffSec = Math.round((t - now) / 1000)
  const abs = Math.abs(diffSec)
  if (abs < 45) return w.justNow
  const fmt = diffSec < 0 ? w.past : w.future
  if (abs < 3600) return fmt(Math.max(1, Math.round(abs / 60)), 'min')
  if (abs < 86400) return fmt(Math.round(abs / 3600), 'h')
  const days = Math.round(abs / 86400)
  if (days === 1) return diffSec < 0 ? w.yesterday : w.tomorrow
  return fmt(days, 'd')
}

function toMillis(date: Date | number | string): number | null {
  const t = date instanceof Date ? date.getTime() : typeof date === 'number' ? date : Date.parse(date)
  return Number.isFinite(t) ? t : null
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** Local wall-clock time "14:05". */
export function formatTime(date: Date | number | string): string {
  const t = toMillis(date)
  if (t === null) return '—'
  const d = new Date(t)
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}

/** Local date "16.10.2026" (kk/ru) or "16/10/2026" (en, day-first as used in Kazakhstan). */
export function formatDate(date: Date | number | string, locale: Locale): string {
  const t = toMillis(date)
  if (t === null) return '—'
  const d = new Date(t)
  const sep = locale === 'en' ? '/' : '.'
  return `${pad2(d.getDate())}${sep}${pad2(d.getMonth() + 1)}${sep}${d.getFullYear()}`
}

/** Local date and time "16.10.2026, 14:05". */
export function formatDateTime(date: Date | number | string, locale: Locale): string {
  const t = toMillis(date)
  if (t === null) return '—'
  return `${formatDate(t, locale)}, ${formatTime(t)}`
}
