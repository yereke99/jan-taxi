import { describe, expect, it } from 'vitest'
import {
  DEFAULT_TWOGIS_BASE,
  formatPoint,
  fullRouteUrl,
  MAX_VIA_POINTS,
  nextStopUrl,
  normalizeTwoGisBase,
  planNavigation,
  preferredMode,
} from './twogis'

const EMBI = { lat: 48.8264, lon: 58.1442 }
const A = { lat: 48.8301, lon: 58.1405 }
const B = { lat: 48.8233, lon: 58.152 }
const C = { lat: 48.835, lon: 58.161 }

describe('formatPoint', () => {
  it('puts longitude first with 6 decimals and a dot', () => {
    expect(formatPoint(EMBI)).toBe('58.144200,48.826400')
  })

  it('does not depend on the UI locale', () => {
    const p = { lat: 43.2389498, lon: 76.889709 }
    expect(formatPoint(p)).toBe('76.889709,43.238950')
    expect(formatPoint(p)).not.toContain(' ')
  })
})

describe('normalizeTwoGisBase', () => {
  it('defaults to https://2gis.ru', () => {
    expect(normalizeTwoGisBase(null)).toBe(DEFAULT_TWOGIS_BASE)
    expect(normalizeTwoGisBase('')).toBe(DEFAULT_TWOGIS_BASE)
  })

  it('accepts the configured https 2GIS host and drops any path', () => {
    expect(normalizeTwoGisBase('https://2gis.kz')).toBe('https://2gis.kz')
    expect(normalizeTwoGisBase('https://2gis.kz/almaty/')).toBe('https://2gis.kz')
  })

  it('rejects custom schemes, http and foreign hosts', () => {
    expect(normalizeTwoGisBase('dgis://2gis.ru')).toBe(DEFAULT_TWOGIS_BASE)
    expect(normalizeTwoGisBase('http://2gis.ru')).toBe(DEFAULT_TWOGIS_BASE)
    expect(normalizeTwoGisBase('https://evil.example')).toBe(DEFAULT_TWOGIS_BASE)
  })
})

describe('nextStopUrl', () => {
  it('leaves point A empty so 2GIS starts from the current location', () => {
    expect(nextStopUrl('https://2gis.ru', A)).toBe('https://2gis.ru/directions/tab/car/points/|58.140500,48.830100')
  })
})

describe('fullRouteUrl', () => {
  it('lists origin and every stop in order, separated by a literal |', () => {
    expect(fullRouteUrl('https://2gis.ru', EMBI, [A, B, C])).toBe(
      'https://2gis.ru/directions/tab/car/points/58.144200,48.826400|58.140500,48.830100|58.152000,48.823300|58.161000,48.835000',
    )
  })

  it('keeps "|" unencoded through URL parsing (openLink re-serialises the URL)', () => {
    const url = fullRouteUrl('https://2gis.ru', EMBI, [A, B])
    expect(new URL(url).toString()).toBe(url)
    expect(url).not.toContain('%7C')
  })

  it('allows at most 8 via points', () => {
    const nine = Array.from({ length: MAX_VIA_POINTS + 1 }, (_, i) => ({ lat: 48.8 + i / 1000, lon: 58.1 }))
    expect(() => fullRouteUrl('https://2gis.ru', EMBI, nine)).not.toThrow() // 8 via + destination
    const ten = [...nine, C]
    expect(() => fullRouteUrl('https://2gis.ru', EMBI, ten)).toThrow(RangeError)
  })

  it('rejects invalid coordinates', () => {
    expect(() => fullRouteUrl('https://2gis.ru', { lat: Number.NaN, lon: 1 }, [A])).toThrow(RangeError)
    expect(() => nextStopUrl('https://2gis.ru', { lat: 0, lon: 0 })).toThrow(RangeError)
  })
})

describe('preferredMode', () => {
  it.each([
    ['ios', 'next'],
    ['android', 'full'],
    ['tdesktop', 'full'],
    ['macos', 'full'],
    ['weba', 'full'],
    ['browser', 'full'],
  ] as const)('%s → %s', (platform, mode) => {
    expect(preferredMode(platform)).toBe(mode)
  })
})

describe('planNavigation', () => {
  const base = 'https://2gis.ru'

  it('Android with GPS: full route from the driver through all remaining stops', () => {
    const plan = planNavigation({ base, platform: 'android', origin: EMBI, remaining: [A, B, C] })
    expect(plan).not.toBeNull()
    expect(plan!.primaryMode).toBe('full')
    expect(plan!.primaryUrl).toBe(fullRouteUrl(base, EMBI, [A, B, C]))
    expect(plan!.nextUrl).toBe(nextStopUrl(base, A))
    expect(plan!.note).toBeNull()
  })

  it('iOS: next stop with empty A and a visible note when more stops remain', () => {
    const plan = planNavigation({ base, platform: 'ios', origin: EMBI, remaining: [A, B] })
    expect(plan!.primaryMode).toBe('next')
    expect(plan!.primaryUrl).toBe('https://2gis.ru/directions/tab/car/points/|58.140500,48.830100')
    expect(plan!.note).toBe('ios')
  })

  it('iOS with a single remaining stop needs no note', () => {
    expect(planNavigation({ base, platform: 'ios', origin: EMBI, remaining: [C] })!.note).toBeNull()
  })

  it('no GPS: next-stop mode with empty A, never an invented origin', () => {
    const plan = planNavigation({ base, platform: 'android', origin: null, remaining: [A, B, C] })
    expect(plan!.primaryMode).toBe('next')
    expect(plan!.primaryUrl).toContain('/points/|')
    expect(plan!.note).toBe('no_gps')
  })

  it('more than 8 via points: next stop with a note', () => {
    const many = Array.from({ length: MAX_VIA_POINTS + 2 }, (_, i) => ({ lat: 48.8 + i / 1000, lon: 58.1 + i / 1000 }))
    const plan = planNavigation({ base, platform: 'android', origin: EMBI, remaining: many })
    expect(plan!.primaryMode).toBe('next')
    expect(plan!.note).toBe('too_many_points')
  })

  it('always offers the next-stop link', () => {
    for (const platform of ['ios', 'android', 'tdesktop']) {
      expect(planNavigation({ base, platform, origin: EMBI, remaining: [A, B] })!.nextUrl).toBe(nextStopUrl(base, A))
    }
  })

  it('uses the configured base', () => {
    const plan = planNavigation({ base: normalizeTwoGisBase('https://2gis.kz'), platform: 'android', origin: EMBI, remaining: [A] })
    expect(plan!.primaryUrl.startsWith('https://2gis.kz/directions/tab/car/points/')).toBe(true)
  })

  it('returns null when nothing remains or a stop is invalid', () => {
    expect(planNavigation({ base, platform: 'android', origin: EMBI, remaining: [] })).toBeNull()
    expect(planNavigation({ base, platform: 'android', origin: EMBI, remaining: [A, { lat: 200, lon: 0 }] })).toBeNull()
  })
})
