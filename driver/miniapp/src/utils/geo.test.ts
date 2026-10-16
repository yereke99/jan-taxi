// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { boundsOf, haversineMeters, isInKazakhstanBBox, isValidLatLon } from './geo'

const EMBI = { lat: 48.8264, lon: 58.1442 }
const AKTOBE = { lat: 50.2839, lon: 57.167 }

describe('geo', () => {
  it('computes great-circle distances', () => {
    const d = haversineMeters(EMBI, AKTOBE)
    expect(d).toBeGreaterThan(170_000)
    expect(d).toBeLessThan(190_000)
    expect(haversineMeters(EMBI, EMBI)).toBe(0)
  })

  it.each([
    [EMBI, true],
    [{ lat: 43.238, lon: 76.889 }, true], // Almaty
    [{ lat: 55.7558, lon: 37.6173 }, false], // Moscow
    [{ lat: 40.0, lon: 70.0 }, false], // south of the bbox
  ])('bbox check %j → %s', (p, expected) => {
    expect(isInKazakhstanBBox(p)).toBe(expected)
  })

  it('rejects invalid coordinates', () => {
    expect(isValidLatLon({ lat: 0, lon: 0 })).toBe(false)
    expect(isValidLatLon({ lat: 91, lon: 10 })).toBe(false)
    expect(isValidLatLon({ lat: Number.NaN, lon: 10 })).toBe(false)
    expect(isValidLatLon(null)).toBe(false)
    expect(isValidLatLon(EMBI)).toBe(true)
  })

  it('computes bounds in lon/lat order', () => {
    expect(boundsOf([])).toBeNull()
    expect(boundsOf([EMBI, AKTOBE])).toEqual([
      [57.167, 48.8264],
      [58.1442, 50.2839],
    ])
  })
})
