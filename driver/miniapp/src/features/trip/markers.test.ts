// Order map markers: a stop without a map point (a manual stop) gets no
// marker, and the others keep the letter of their seq, as in the stop list.

import { describe, expect, it } from 'vitest'
import type { Stop } from '../../api/types'
import { stopMarkerPoints } from './markers'

function stop(seq: number, kind: Stop['kind'], lat: number | null, lon: number | null, over: Partial<Stop> = {}): Stop {
  return {
    id: `s${seq}`,
    seq,
    kind,
    mode: 'geocoded',
    lat,
    lon,
    address: `Address ${seq}`,
    address_typed: false,
    address_details: null,
    completed_at: null,
    ...over,
  }
}

describe('stopMarkerPoints', () => {
  it('skips a manual stop and keeps the letters of the others', () => {
    // Shuffled on purpose: markers follow seq, not array order.
    const stops = [
      stop(2, 'destination', 48.8319, 58.1585),
      stop(0, 'pickup', 48.829, 58.1385),
      stop(1, 'intermediate', null, null, { mode: 'manual', address: 'Әженің үйі' }),
    ]
    const points = stopMarkerPoints(stops)
    expect(points.map((p) => p.letter)).toEqual(['A', 'C'])
    expect(points.map((p) => [p.lat, p.lon])).toEqual([
      [48.829, 58.1385],
      [48.8319, 58.1585],
    ])
  })

  it('letters every stop by its seq when all have coordinates', () => {
    const stops = [stop(0, 'pickup', 48.829, 58.1385), stop(1, 'intermediate', 48.8249, 58.1437), stop(2, 'destination', 48.8319, 58.1585)]
    expect(stopMarkerPoints(stops).map((p) => p.letter)).toEqual(['A', 'B', 'C'])
  })

  it('greys completed stops only on a live trip', () => {
    const stops = [stop(0, 'pickup', 48.829, 58.1385, { completed_at: '2026-10-16T10:00:00Z' }), stop(1, 'destination', 48.8319, 58.1585)]
    expect(stopMarkerPoints(stops, true).map((p) => p.done)).toEqual([true, false])
    expect(stopMarkerPoints(stops).map((p) => p.done)).toEqual([false, false])
  })
})
