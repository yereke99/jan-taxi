import { describe, expect, it } from 'vitest'
import { hasStopCoordinates, type StopSummary } from '../../api/orders'

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
