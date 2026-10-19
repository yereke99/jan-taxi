// Following the driver's car on the home map (like the big ride-hailing
// apps): which device fixes move the car, which way it points and how it
// glides between fixes. Pure functions; the map side lives in
// composables/useFollowCar.

import { bearingDegrees, haversineMeters, type LatLon } from '../../utils/geo'

/** One device fix as the follower sees it. */
export interface TrackPoint extends LatLon {
  /** Horizontal accuracy radius in metres, when known. */
  accuracy_m: number | null
  /** Device course in degrees clockwise from north, when known. */
  heading: number | null
  speed_mps: number | null
  /** Epoch ms when the device took the fix. */
  timestamp: number
}

export interface FollowTuning {
  /** Movements shorter than this are GPS jitter: the car stays put. */
  minMoveM: number
  /** A heading is derived from two fixes only when they are at least this far apart. */
  minBearingMoveM: number
  /** Fixes less accurate than this are ignored while a good fix is recent. */
  maxAccuracyM: number
  /** After this long without a good fix, a less accurate one is used anyway. */
  poorGraceMs: number
  /** The device course is trusted only above this speed (it is noise when parked). */
  minHeadingSpeedMps: number
  /** Jumps farther than this, or after a longer gap, are not animated. */
  snapDistanceM: number
  snapGapMs: number
  /**
   * A glide lasts the time since the previous fix times this factor, so the
   * car is still moving when the next fix (about a second later, give or
   * take) arrives: continuous motion instead of stop-and-go.
   */
  glideFactor: number
  /** Glide duration bounds. */
  minGlideMs: number
  maxGlideMs: number
}

export const FOLLOW_TUNING: FollowTuning = {
  minMoveM: 3,
  minBearingMoveM: 6,
  maxAccuracyM: 50,
  poorGraceMs: 20_000,
  minHeadingSpeedMps: 0.5,
  snapDistanceM: 1_000,
  snapGapMs: 10_000,
  glideFactor: 1.2,
  minGlideMs: 300,
  maxGlideMs: 1_800,
}

export type FixRejection = 'out_of_order' | 'inaccurate' | 'jitter'

export type FixDecision =
  | { accept: false; reason: FixRejection }
  | {
      accept: true
      /** New heading for the car, or null to keep the current one. */
      heading: number | null
      /** Jump without animation (first fix, long gap or a teleport). */
      snap: boolean
      /** How long to glide from the shown position to this fix. */
      glideMs: number
    }

function finite(v: number | null | undefined): v is number {
  return typeof v === 'number' && Number.isFinite(v)
}

/** The device course when it can be trusted (moving, valid range), else null. */
export function deviceHeading(p: TrackPoint, tuning: FollowTuning = FOLLOW_TUNING): number | null {
  if (!finite(p.heading) || p.heading < 0 || p.heading > 360) return null
  if (finite(p.speed_mps) && p.speed_mps < tuning.minHeadingSpeedMps) return null
  return p.heading % 360
}

/**
 * Decides whether `next` moves the car shown at `prev` (the last accepted
 * fix): jitter under a few metres and inaccurate fixes are ignored, the
 * heading comes from the device or, without one, from the two positions.
 */
export function judgeFix(prev: TrackPoint | null, next: TrackPoint, tuning: FollowTuning = FOLLOW_TUNING): FixDecision {
  if (!prev) return { accept: true, heading: deviceHeading(next, tuning), snap: true, glideMs: 0 }
  const dt = next.timestamp - prev.timestamp
  if (dt < 0) return { accept: false, reason: 'out_of_order' }
  const poor = finite(next.accuracy_m) && next.accuracy_m > tuning.maxAccuracyM
  const prevGood = !finite(prev.accuracy_m) || prev.accuracy_m <= tuning.maxAccuracyM
  if (poor && prevGood && dt < tuning.poorGraceMs) return { accept: false, reason: 'inaccurate' }
  const distance = haversineMeters(prev, next)
  if (distance < tuning.minMoveM) return { accept: false, reason: 'jitter' }
  const heading = deviceHeading(next, tuning) ?? (distance >= tuning.minBearingMoveM ? bearingDegrees(prev, next) : null)
  const snap = distance > tuning.snapDistanceM || dt > tuning.snapGapMs
  return { accept: true, heading, snap, glideMs: snap ? 0 : clamp(dt * tuning.glideFactor, tuning.minGlideMs, tuning.maxGlideMs) }
}

export function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v))
}

/** Straight-line interpolation (fine for the metres between two fixes). */
export function lerpPosition(a: LatLon, b: LatLon, t: number): LatLon {
  const k = clamp(t, 0, 1)
  return { lat: a.lat + (b.lat - a.lat) * k, lon: a.lon + (b.lon - a.lon) * k }
}

/** Angle in [0, 360). */
export function normalizeDeg(deg: number): number {
  return ((deg % 360) + 360) % 360
}

/**
 * The angle equal to `to` (mod 360) that is nearest to `from`, so a turn from
 * 350° to 10° rotates 20° clockwise instead of 340° back.
 */
export function nearestTurn(from: number, to: number): number {
  const delta = ((((to - from) % 360) + 540) % 360) - 180
  return from + delta
}

/** iOS-like ease-out for the rotation. */
export function easeOutCubic(t: number): number {
  const k = clamp(t, 0, 1)
  return 1 - (1 - k) ** 3
}
