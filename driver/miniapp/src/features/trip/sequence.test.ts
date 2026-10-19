import { describe, expect, it } from 'vitest'
import type { Stop } from '../../api/types'
import { actionLabelKey } from './labels'
import { currentTarget, primaryAction, remainingStops, type TripState } from './sequence'

function stop(seq: number, kind: Stop['kind'], done = false): Stop {
  return {
    id: `s${seq}`,
    seq,
    kind,
    lat: 48.82 + seq / 100,
    lon: 58.14 + seq / 100,
    address: `Address ${seq}`,
    address_details: null,
    completed_at: done ? '2026-10-16T10:00:00Z' : null,
  }
}

// Shuffled on purpose: actions must follow seq, not array order.
const STOPS = [stop(3, 'destination'), stop(0, 'pickup'), stop(2, 'intermediate'), stop(1, 'intermediate')]

function state(over: Partial<TripState>): TripState {
  return { status: 'accepted', stops: STOPS, can_arrive: false, can_start: false, can_complete: false, ...over }
}

describe('primaryAction', () => {
  it('accepted → "arrived" (driver_arriving = waiting at the pickup)', () => {
    expect(primaryAction(state({ status: 'accepted', can_arrive: true, can_start: true }))).toEqual({ kind: 'arrive' })
  })

  it('driver_arriving → start', () => {
    expect(primaryAction(state({ status: 'driver_arriving', can_start: true }))).toEqual({ kind: 'start' })
  })

  it('in_progress → intermediate stops strictly in seq order, then complete', () => {
    const s1 = state({ status: 'in_progress', stops: [stop(0, 'pickup', true), ...STOPS.filter((s) => s.seq > 0)] })
    expect(primaryAction(s1)).toEqual({ kind: 'complete_stop', stopId: 's1', seq: 1, letter: 'B' })

    const s2 = state({
      status: 'in_progress',
      stops: [stop(0, 'pickup', true), stop(1, 'intermediate', true), stop(2, 'intermediate'), stop(3, 'destination')],
    })
    expect(primaryAction(s2)).toEqual({ kind: 'complete_stop', stopId: 's2', seq: 2, letter: 'C' })

    const s3 = state({
      status: 'in_progress',
      can_complete: true,
      stops: [stop(0, 'pickup', true), stop(1, 'intermediate', true), stop(2, 'intermediate', true), stop(3, 'destination')],
    })
    expect(primaryAction(s3)).toEqual({ kind: 'complete' })
  })

  it('a two-point trip completes right after the start', () => {
    const s = state({ status: 'in_progress', can_complete: true, stops: [stop(0, 'pickup', true), stop(1, 'destination')] })
    expect(primaryAction(s)).toEqual({ kind: 'complete' })
  })

  it('follows the server flags and closed states have no action', () => {
    expect(primaryAction(state({ status: 'accepted' }))).toBeNull()
    expect(primaryAction(state({ status: 'in_progress', stops: [stop(0, 'pickup', true), stop(1, 'destination')] }))).toBeNull()
    expect(primaryAction(state({ status: 'completed' }))).toBeNull()
    expect(primaryAction(state({ status: 'cancelled' }))).toBeNull()
  })
})

describe('targets and labels', () => {
  it('the pickup is the target before the start, then the next uncompleted stop', () => {
    expect(currentTarget({ status: 'accepted', stops: STOPS })?.seq).toBe(0)
    expect(currentTarget({ status: 'driver_arriving', stops: STOPS })?.seq).toBe(0)
    const moving = [stop(0, 'pickup', true), stop(1, 'intermediate', true), stop(2, 'intermediate'), stop(3, 'destination')]
    expect(currentTarget({ status: 'in_progress', stops: moving })?.seq).toBe(2)
    expect(remainingStops({ status: 'in_progress', stops: moving }).map((s) => s.seq)).toEqual([2, 3])
    expect(remainingStops({ status: 'accepted', stops: STOPS }).map((s) => s.seq)).toEqual([0, 1, 2, 3])
    expect(remainingStops({ status: 'completed', stops: STOPS })).toEqual([])
  })

  it('labels every action', () => {
    expect(actionLabelKey({ kind: 'arrive' })).toBe('trip.arrive')
    expect(actionLabelKey({ kind: 'start' })).toBe('trip.start')
    expect(actionLabelKey({ kind: 'complete_stop', stopId: 's1', seq: 1, letter: 'B' })).toBe('trip.stop_done')
    expect(actionLabelKey({ kind: 'complete' })).toBe('trip.complete')
  })
})
