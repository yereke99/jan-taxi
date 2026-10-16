// Fare picker rules. The quote lists every allowed fare (fare.options, from
// the recommended minimum up to the tariff maximum in fare.step increments);
// the passenger may only choose one of them and never less than fare.min
// (the API answers fare_not_allowed otherwise).

import type { FareOptions } from '../api/types'

/** Allowed values in ascending order, never below the quote minimum. */
export function allowedFares(f: FareOptions): number[] {
  return f.options.filter((v) => v >= f.min && v <= f.max)
}

export function isAllowedFare(f: FareOptions, value: number | null | undefined): value is number {
  return typeof value === 'number' && allowedFares(f).includes(value)
}

/** The allowed fare closest to `value` (ties go up); the recommended one for null. */
export function clampFare(f: FareOptions, value: number | null | undefined): number {
  const list = allowedFares(f)
  if (list.length === 0) return f.recommended
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    return list.includes(f.recommended) ? f.recommended : list[0]!
  }
  let best = list[0]!
  for (const v of list) if (Math.abs(v - value) < Math.abs(best - value) || (Math.abs(v - value) === Math.abs(best - value) && v > best)) best = v
  return best
}

/** Next (dir 1) or previous (dir -1) allowed fare; stays at the bounds. */
export function stepFare(f: FareOptions, value: number, dir: 1 | -1): number {
  const list = allowedFares(f)
  if (list.length === 0) return value
  const i = list.indexOf(clampFare(f, value))
  const j = Math.min(list.length - 1, Math.max(0, i + dir))
  return list[j]!
}

export function canDecreaseFare(f: FareOptions, value: number): boolean {
  const list = allowedFares(f)
  return list.length > 0 && value > list[0]!
}

export function canIncreaseFare(f: FareOptions, value: number): boolean {
  const list = allowedFares(f)
  return list.length > 0 && value < list[list.length - 1]!
}

/**
 * Fare after a new quote: the passenger's previous choice when the new quote
 * still allows it, otherwise the new recommendation.
 */
export function fareAfterRequote(previous: number | null, f: FareOptions): number {
  return isAllowedFare(f, previous) ? previous : clampFare(f, null)
}
