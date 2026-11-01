import { describe, expect, it } from 'vitest'
import { hasStopCoordinates, isPassengerWritten, type StopSummary } from '../../api/orders'

const stop: StopSummary = { seq: 1, kind: 'destination', address: 'Ауыл орталығы', lat: 48.8, lon: 58.1 }

describe('manual destination coordinate safety', () => {
  it('keeps legacy geocoded stops and excludes a manual endpoint', () => {
    expect(hasStopCoordinates(stop)).toBe(true)
    expect(hasStopCoordinates({ ...stop, mode: 'manual', lat: null, lon: null })).toBe(false)
    expect(hasStopCoordinates({ ...stop, mode: 'manual' })).toBe(false)
  })

  it('never coerces missing or invalid coordinates into map points', () => {
    for (const lat of [null, Number.NaN, Number.POSITIVE_INFINITY, 91]) {
      expect(hasStopCoordinates({ ...stop, lat })).toBe(false)
    }
    expect(hasStopCoordinates({ ...stop, lon: null })).toBe(false)
    expect(hasStopCoordinates({ ...stop, lon: 181 })).toBe(false)
  })
})

describe('passenger-written addresses', () => {
  it('marks manual stops and a typed pickup A, never ordinary or older stops', () => {
    const manual: StopSummary = { ...stop, mode: 'manual', lat: null, lon: null }
    expect(isPassengerWritten(manual)).toBe(true)
    const typed: StopSummary = { ...stop, seq: 0, kind: 'pickup', mode: 'geocoded', address_typed: true }
    expect(isPassengerWritten(typed)).toBe(true)
    // A typed pickup keeps its map point.
    expect(hasStopCoordinates(typed)).toBe(true)
    expect(isPassengerWritten({ ...stop, mode: 'geocoded', address_typed: false })).toBe(false)
    expect(isPassengerWritten(stop)).toBe(false)
  })
})
