// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { formatDistance, formatDuration, formatMoney, formatRelativeTime } from './format'

const S = String.fromCharCode(0xa0)

describe('formatMoney', () => {
  it.each([
    [1500, 'kk', `1${S}500${S}₸`],
    [1500, 'ru', `1${S}500${S}₸`],
    [1500, 'en', `1,500${S}₸`],
    [500, 'kk', `500${S}₸`],
    [1234567, 'ru', `1${S}234${S}567${S}₸`],
    [0, 'kk', `0${S}₸`],
  ] as const)('%i in %s', (amount, locale, expected) => {
    expect(formatMoney(amount, locale)).toBe(expected)
  })

  it('marks negative amounts with a minus sign', () => {
    expect(formatMoney(-2000, 'kk')).toBe(`−2${S}000${S}₸`)
  })
})

describe('formatDistance', () => {
  it.each([
    [2400, 'kk', `2,4${S}км`],
    [2400, 'ru', `2,4${S}км`],
    [2400, 'en', `2.4${S}km`],
    [850, 'kk', `850${S}м`],
    [854, 'en', `850${S}m`],
    [3, 'ru', `10${S}м`],
    [999, 'kk', `1,0${S}км`],
    [125_400, 'kk', `125${S}км`],
  ] as const)('%i m in %s', (m, locale, expected) => {
    expect(formatDistance(m, locale)).toBe(expected)
  })
})

describe('formatDuration', () => {
  it.each([
    [720, 'kk', `12${S}мин`],
    [720, 'ru', `12${S}мин`],
    [720, 'en', `12${S}min`],
    [10, 'en', `1${S}min`],
    [3900, 'kk', `1${S}сағ 5${S}мин`],
    [3900, 'ru', `1${S}ч 5${S}мин`],
    [7200, 'en', `2${S}h`],
  ] as const)('%i s in %s', (s, locale, expected) => {
    expect(formatDuration(s, locale)).toBe(expected)
  })
})

describe('formatRelativeTime', () => {
  const now = Date.parse('2026-10-16T12:00:00Z')

  it('says "just now" within 45 seconds', () => {
    expect(formatRelativeTime(now - 30_000, 'kk', now)).toBe('жаңа ғана')
    expect(formatRelativeTime(now - 30_000, 'ru', now)).toBe('только что')
    expect(formatRelativeTime(now - 30_000, 'en', now)).toBe('just now')
  })

  it('formats past minutes and hours', () => {
    expect(formatRelativeTime(now - 5 * 60_000, 'kk', now)).toBe(`5${S}минут бұрын`)
    expect(formatRelativeTime(now - 5 * 60_000, 'ru', now)).toBe(`5${S}мин назад`)
    expect(formatRelativeTime(now - 3 * 3_600_000, 'en', now)).toBe(`3${S}h ago`)
  })

  it('formats the future', () => {
    expect(formatRelativeTime(now + 10 * 60_000, 'kk', now)).toBe(`10${S}минуттан кейін`)
    expect(formatRelativeTime(now + 10 * 60_000, 'ru', now)).toBe(`через 10${S}мин`)
  })

  it('uses yesterday for one day', () => {
    expect(formatRelativeTime(new Date(now - 86_400_000), 'kk', now)).toBe('кеше')
    expect(formatRelativeTime('2026-10-15T12:00:00Z', 'ru', now)).toBe('вчера')
  })

  it('returns a dash for invalid input', () => {
    expect(formatRelativeTime('not a date', 'en', now)).toBe('—')
  })
})
