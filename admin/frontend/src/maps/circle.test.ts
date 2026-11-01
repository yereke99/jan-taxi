import { describe, expect, it } from 'vitest'
import { bearingDegrees, destinationPoint, distanceMeters } from '../utils/geo'
import { CIRCLE_STEPS, circleBounds, geodesicCircle, geodesicRing } from './circle'

const EMBI = { lat: 48.8264, lon: 58.1442 }

/** Shoelace area in degrees²: positive for a counter-clockwise ring. */
function signedArea(ring: [number, number][]): number {
  let a = 0
  for (let i = 0; i < ring.length - 1; i++) a += ring[i]![0] * ring[i + 1]![1] - ring[i + 1]![0] * ring[i]![1]
  return a / 2
}

describe('geodesy', () => {
  it('walks a distance along a bearing and measures it back', () => {
    for (const bearing of [0, 45, 90, 180, 271.5]) {
      const p = destinationPoint(EMBI, 7100, bearing)
      expect(distanceMeters(EMBI, p)).toBeCloseTo(7100, 3)
      // Angles compare modulo 360 (due north may come back as 359.999…).
      expect(Math.abs(((bearingDegrees(EMBI, p) - bearing + 540) % 360) - 180)).toBeLessThan(1e-6)
    }
  })

  it('goes north along the meridian', () => {
    const p = destinationPoint(EMBI, 10_000, 0)
    expect(p.lon).toBeCloseTo(EMBI.lon, 9)
    expect(p.lat).toBeGreaterThan(EMBI.lat)
    expect(bearingDegrees(p, EMBI)).toBeCloseTo(180, 6)
  })
})

describe('geodesicCircle', () => {
  it('puts every vertex exactly radius metres from the centre', () => {
    for (const radius of [200, 2800, 50_000]) {
      const ring = geodesicRing(EMBI, radius)
      for (const [lon, lat] of ring) expect(distanceMeters(EMBI, { lat, lon })).toBeCloseTo(radius, 3)
    }
  })

  it('is a closed counter-clockwise GeoJSON ring starting due north', () => {
    const g = geodesicCircle(EMBI, 2800)
    expect(g.type).toBe('Polygon')
    const ring = g.coordinates[0]!
    expect(ring).toHaveLength(CIRCLE_STEPS + 1)
    expect(ring[ring.length - 1]).toEqual(ring[0])
    expect(ring[0]![0]).toBeCloseTo(EMBI.lon, 9)
    expect(ring[0]![1]).toBeGreaterThan(EMBI.lat)
    expect(signedArea(ring)).toBeGreaterThan(0)
  })

  it('is wider in degrees of longitude than of latitude away from the equator', () => {
    const [[w, s], [e, n]] = circleBounds(EMBI, 5000)
    expect(n - s).toBeCloseTo((2 * 5000) / 111_195, 3)
    expect((e - w) / (n - s)).toBeCloseTo(1 / Math.cos((EMBI.lat * Math.PI) / 180), 2)
    expect(w).toBeLessThan(EMBI.lon)
    expect(e).toBeGreaterThan(EMBI.lon)
  })
})
