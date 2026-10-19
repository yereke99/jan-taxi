import { describe, expect, it } from 'vitest'
import { FOLLOW_TUNING, deviceHeading, judgeFix, lerpPosition, nearestTurn, normalizeDeg, type TrackPoint } from './follow'

/** ~1 m of latitude in degrees. */
const M = 1 / 111_195

function pt(over: Partial<TrackPoint> = {}): TrackPoint {
  return { lat: 48.82, lon: 58.14, accuracy_m: 8, heading: null, speed_mps: null, timestamp: 1_000_000, ...over }
}

/** `metres` north of `p`, `ms` later. */
function north(p: TrackPoint, metres: number, ms = 1000, over: Partial<TrackPoint> = {}): TrackPoint {
  return { ...p, lat: p.lat + metres * M, timestamp: p.timestamp + ms, heading: null, speed_mps: null, ...over }
}

describe('judgeFix', () => {
  it('shows the first fix at once, with the device heading when moving', () => {
    expect(judgeFix(null, pt())).toEqual({ accept: true, heading: null, snap: true, glideMs: 0 })
    expect(judgeFix(null, pt({ heading: 270, speed_mps: 8 }))).toMatchObject({ accept: true, heading: 270, snap: true })
  })

  it('ignores GPS jitter below 3 m', () => {
    const a = pt()
    expect(judgeFix(a, north(a, 2.5))).toEqual({ accept: false, reason: 'jitter' })
    expect(judgeFix(a, north(a, 4))).toMatchObject({ accept: true, snap: false })
  })

  it('glides a little longer than the time between fixes (no stop-and-go)', () => {
    const a = pt()
    expect(judgeFix(a, north(a, 10, 1000))).toMatchObject({ accept: true, glideMs: 1200 })
    expect(judgeFix(a, north(a, 10, 100))).toMatchObject({ glideMs: FOLLOW_TUNING.minGlideMs })
    expect(judgeFix(a, north(a, 10, 4000))).toMatchObject({ glideMs: FOLLOW_TUNING.maxGlideMs })
  })

  it('ignores inaccurate fixes while a good one is recent, then uses them', () => {
    const a = pt({ accuracy_m: 10 })
    const poor = north(a, 40, 1000, { accuracy_m: 120 })
    expect(judgeFix(a, poor)).toEqual({ accept: false, reason: 'inaccurate' })
    const later = north(a, 40, FOLLOW_TUNING.poorGraceMs + 1000, { accuracy_m: 120 })
    expect(judgeFix(a, later)).toMatchObject({ accept: true })
    // Already showing a poor fix: a better or equally poor one may move the car.
    expect(judgeFix(pt({ accuracy_m: 150 }), north(pt({ accuracy_m: 150 }), 40, 1000, { accuracy_m: 120 }))).toMatchObject({ accept: true })
  })

  it('rejects fixes older than the shown one', () => {
    const a = pt()
    expect(judgeFix(a, north(a, 20, -500))).toEqual({ accept: false, reason: 'out_of_order' })
  })

  it('uses the device course when moving, else the bearing between fixes', () => {
    const a = pt()
    expect(judgeFix(a, north(a, 12, 1000, { heading: 45, speed_mps: 12 }))).toMatchObject({ heading: 45 })
    // No course from the device: north-bound movement gives 0°.
    const derived = judgeFix(a, north(a, 12))
    expect(derived.accept && Math.round(derived.heading ?? -1)).toBe(0)
    // Course reported while crawling is noise: derive it instead.
    const slow = judgeFix(a, north(a, 12, 1000, { heading: 200, speed_mps: 0.2 }))
    expect(slow.accept && Math.round(slow.heading ?? -1)).toBe(0)
  })

  it('keeps the heading for short moves (too noisy to derive one)', () => {
    const a = pt()
    expect(judgeFix(a, north(a, 4))).toMatchObject({ accept: true, heading: null })
  })

  it('jumps without animation after a long gap or a teleport', () => {
    const a = pt()
    expect(judgeFix(a, north(a, 50, FOLLOW_TUNING.snapGapMs + 1))).toMatchObject({ accept: true, snap: true, glideMs: 0 })
    expect(judgeFix(a, north(a, 5_000, 1000))).toMatchObject({ accept: true, snap: true })
  })
})

describe('deviceHeading', () => {
  it('drops missing, out-of-range and parked courses', () => {
    expect(deviceHeading(pt({ heading: null }))).toBeNull()
    expect(deviceHeading(pt({ heading: Number.NaN }))).toBeNull()
    expect(deviceHeading(pt({ heading: -1 }))).toBeNull()
    expect(deviceHeading(pt({ heading: 90, speed_mps: 0 }))).toBeNull()
    expect(deviceHeading(pt({ heading: 90, speed_mps: null }))).toBe(90)
    expect(deviceHeading(pt({ heading: 360, speed_mps: 5 }))).toBe(0)
  })
})

describe('angles and interpolation', () => {
  it('turns the short way round', () => {
    expect(nearestTurn(350, 10)).toBe(370)
    expect(nearestTurn(10, 350)).toBe(-10)
    expect(nearestTurn(720 + 90, 80)).toBe(800)
    expect(normalizeDeg(nearestTurn(0, 180))).toBe(180)
    expect(normalizeDeg(-30)).toBe(330)
  })

  it('interpolates positions within the segment only', () => {
    const a = { lat: 0, lon: 0 }
    const b = { lat: 10, lon: 20 }
    expect(lerpPosition(a, b, 0.5)).toEqual({ lat: 5, lon: 10 })
    expect(lerpPosition(a, b, -1)).toEqual(a)
    expect(lerpPosition(a, b, 2)).toEqual(b)
  })
})
