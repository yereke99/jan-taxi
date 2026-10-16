import { describe, expect, it } from 'vitest'
import type { FareOptions } from '../api/types'
import { allowedFares, canDecreaseFare, canIncreaseFare, clampFare, fareAfterRequote, isAllowedFare, stepFare } from './fare'

// Shape of pricing.Compute: recommended == min, options min..max by step.
const fare: FareOptions = { recommended: 700, min: 700, max: 900, step: 50, options: [700, 750, 800, 850, 900] }

describe('fare picker bounds', () => {
  it('defaults to the recommended fare', () => {
    expect(clampFare(fare, null)).toBe(700)
  })

  it('cannot go below the quote minimum', () => {
    expect(stepFare(fare, 700, -1)).toBe(700)
    expect(canDecreaseFare(fare, 700)).toBe(false)
    expect(clampFare(fare, 500)).toBe(700)
    expect(isAllowedFare(fare, 650)).toBe(false)
  })

  it('cannot go above the maximum', () => {
    expect(stepFare(fare, 900, 1)).toBe(900)
    expect(canIncreaseFare(fare, 900)).toBe(false)
    expect(clampFare(fare, 5000)).toBe(900)
  })

  it('steps through the allowed options only', () => {
    expect(stepFare(fare, 700, 1)).toBe(750)
    expect(stepFare(fare, 850, -1)).toBe(800)
    // An off-grid value snaps to the nearest option first.
    expect(stepFare(fare, 760, 1)).toBe(800)
    expect(clampFare(fare, 775)).toBe(800)
  })

  it('ignores options outside [min, max] even if the payload contains them', () => {
    const odd: FareOptions = { ...fare, options: [600, 700, 750, 950] }
    expect(allowedFares(odd)).toEqual([700, 750])
    expect(stepFare(odd, 750, 1)).toBe(750)
  })

  it('keeps the passenger choice after a re-quote when still allowed', () => {
    expect(fareAfterRequote(800, fare)).toBe(800)
    const higher: FareOptions = { recommended: 850, min: 850, max: 1000, step: 50, options: [850, 900, 950, 1000] }
    expect(fareAfterRequote(800, higher)).toBe(850)
    expect(fareAfterRequote(null, higher)).toBe(850)
  })
})
