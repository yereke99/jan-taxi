// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { LocationError, validateLocation, type DeviceLocation } from './location'

const now = Date.parse('2026-10-16T12:00:00Z')

function fix(patch: Partial<DeviceLocation> = {}): DeviceLocation {
  return { lat: 48.8264, lon: 58.1442, accuracy_m: 12, heading: null, speed_mps: null, timestamp: now - 1000, source: 'browser', ...patch }
}

describe('validateLocation', () => {
  it('accepts a fresh, valid fix', () => {
    expect(validateLocation(fix(), now, 15_000)).toMatchObject({ lat: 48.8264, lon: 58.1442 })
  })

  it.each([
    ['null island', { lat: 0, lon: 0 }],
    ['latitude out of range', { lat: 95 }],
    ['NaN longitude', { lon: Number.NaN }],
  ])('rejects %s as invalid', (_name, patch) => {
    expect(() => validateLocation(fix(patch), now, 15_000)).toThrowError(expect.objectContaining({ code: 'invalid' }))
  })

  it('rejects a stale fix', () => {
    const err = (() => {
      try {
        validateLocation(fix({ timestamp: now - 60_000 }), now, 15_000)
        return null
      } catch (e) {
        return e
      }
    })()
    expect(err).toBeInstanceOf(LocationError)
    expect((err as LocationError).code).toBe('stale')
  })
})
