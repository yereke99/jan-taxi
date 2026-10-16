// Shared with the other Mini App — keep both copies identical (make verify-shared).

import type { Map as MapLibreMap } from 'maplibre-gl'
import { describe, expect, it, vi } from 'vitest'
import { computeFitPadding, fitToPoints, paddingBeyondCamera } from './fitToPoints'

describe('computeFitPadding', () => {
  const size = { width: 390, height: 800 }

  it('adds safe areas and uses the sheet height at the bottom', () => {
    expect(computeFitPadding(size, {}, 300, { top: 93, right: 0, bottom: 34, left: 0 })).toEqual({
      top: 48 + 93,
      right: 48,
      bottom: 48 + 300,
      left: 48,
    })
  })

  it('uses the bottom safe area when there is no sheet', () => {
    expect(computeFitPadding(size, { top: 10, bottom: 10, left: 10, right: 10 }, 0, { top: 0, right: 0, bottom: 34, left: 0 })).toEqual({
      top: 10,
      right: 10,
      bottom: 44,
      left: 10,
    })
  })

  it('scales padding down so part of the map stays visible', () => {
    const p = computeFitPadding({ width: 300, height: 500 }, {}, 600)
    expect(p.top + p.bottom).toBeLessThanOrEqual(500 - 64)
    expect(p.bottom).toBeGreaterThan(p.top)
    const q = computeFitPadding({ width: 100, height: 800 }, { left: 80, right: 80 })
    expect(q.left + q.right).toBeLessThanOrEqual(100 - 64)
  })
})

describe('fitToPoints', () => {
  function fakeMap(camera: { top: number; right: number; bottom: number; left: number }) {
    const map = {
      getContainer: () => ({ clientWidth: 390, clientHeight: 800 }),
      getPadding: () => camera,
      getZoom: () => 12,
      fitBounds: vi.fn(),
      easeTo: vi.fn(),
    }
    return { map, api: map as unknown as MapLibreMap }
  }
  const safe = { top: 90, right: 0, bottom: 34, left: 0 }
  const route = [
    { lat: 48.82, lon: 58.14 },
    { lat: 48.84, lon: 58.16 },
  ]

  it('does not count the sheet twice when the camera padding already holds it', () => {
    const { map, api } = fakeMap({ top: 90, right: 0, bottom: 300, left: 0 })
    fitToPoints(api, route, { sheetHeight: 300, safeArea: safe, padding: { top: 56, bottom: 32, left: 40, right: 40 }, animate: false })
    expect(map.fitBounds).toHaveBeenCalledTimes(1)
    expect(map.fitBounds.mock.calls[0]![1].padding).toEqual({ top: 56, right: 40, bottom: 32, left: 40 })
  })

  it('passes the whole inset when the camera has no padding', () => {
    const { map, api } = fakeMap({ top: 0, right: 0, bottom: 0, left: 0 })
    fitToPoints(api, route, { sheetHeight: 300, safeArea: safe, animate: false })
    expect(map.fitBounds.mock.calls[0]![1].padding).toEqual({ top: 48 + 90, right: 48, bottom: 48 + 300, left: 48 })
  })

  it('centres a single point without replacing the camera padding', () => {
    const { map, api } = fakeMap({ top: 90, right: 0, bottom: 300, left: 0 })
    fitToPoints(api, [route[0]!], { sheetHeight: 300, safeArea: safe, padding: { top: 60, bottom: 20, left: 40, right: 40 }, animate: false })
    const opts = map.easeTo.mock.calls[0]![0]
    expect(opts.padding).toBeUndefined()
    expect(opts.offset).toEqual([0, 20])
    expect(opts.center).toEqual([58.14, 48.82])
  })

  it('paddingBeyondCamera never goes negative', () => {
    expect(paddingBeyondCamera({ top: 10, right: 10, bottom: 10, left: 10 }, { top: 50, bottom: 5 })).toEqual({ top: 0, right: 10, bottom: 5, left: 10 })
  })
})
