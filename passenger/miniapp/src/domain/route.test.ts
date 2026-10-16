import { describe, expect, it } from 'vitest'
import {
  addStop,
  canAddStop,
  clampMaxPoints,
  createStops,
  isComplete,
  moveStop,
  normalizeStops,
  outsideIndices,
  removeStop,
  routeFingerprint,
  setPlace,
  stopsFromPlaces,
  toStopInputs,
  type PlaceValue,
  type RouteStop,
} from './route'

function keys(): () => string {
  let n = 0
  return () => `k${++n}`
}

function place(name: string, lat = 48.8, lon = 58.1, inServiceArea: boolean | null = true): PlaceValue {
  return { lat, lon, address: name, subtitle: null, source: 'search', inServiceArea }
}

function names(stops: RouteStop[]): Array<string | null> {
  return stops.map((s) => s.place?.address ?? null)
}

function roles(stops: RouteStop[]): string[] {
  return stops.map((s) => s.role)
}

/** A–E route with distinct places. */
function fullRoute(max = 5): RouteStop[] {
  const k = keys()
  let s = createStops(k)
  while (canAddStop(s, max)) s = addStop(s, max, k)
  return s.map((st, i) => ({ ...st, place: place(`P${i}`, 48.8 + i / 100, 58.1) }))
}

describe('route editor rules', () => {
  it('starts with an empty pickup and destination', () => {
    const s = createStops(keys())
    expect(roles(s)).toEqual(['pickup', 'destination'])
    expect(isComplete(s)).toBe(false)
    expect(toStopInputs(s)).toBeNull()
  })

  it('adds intermediate stops before the destination up to the maximum', () => {
    const k = keys()
    let s = createStops(k)
    s = setPlace(s, 0, place('A'))
    s = setPlace(s, 1, place('B'))
    s = addStop(s, 5, k)
    expect(roles(s)).toEqual(['pickup', 'intermediate', 'destination'])
    expect(names(s)).toEqual(['A', null, 'B'])
    s = addStop(s, 5, k)
    s = addStop(s, 5, k)
    expect(s).toHaveLength(5)
    expect(canAddStop(s, 5)).toBe(false)
    expect(addStop(s, 5, k)).toBe(s)
  })

  it('respects a lower max_points from the public config and clamps invalid values', () => {
    const k = keys()
    let s = createStops(k)
    s = addStop(s, 3, k)
    expect(s).toHaveLength(3)
    expect(addStop(s, 3, k)).toBe(s)
    expect(clampMaxPoints(9)).toBe(5)
    expect(clampMaxPoints(1)).toBe(2)
    expect(clampMaxPoints(Number.NaN)).toBe(5)
  })

  it('removes only intermediate stops', () => {
    const s = fullRoute()
    expect(removeStop(s, 0)).toBe(s)
    expect(removeStop(s, 4)).toBe(s)
    const r = removeStop(s, 2)
    expect(names(r)).toEqual(['P0', 'P1', 'P3', 'P4'])
    expect(roles(r)).toEqual(['pickup', 'intermediate', 'intermediate', 'destination'])
  })

  it('reorders intermediate stops only; pickup and destination keep their roles', () => {
    const s = fullRoute()
    expect(names(moveStop(s, 1, 3))).toEqual(['P0', 'P2', 'P3', 'P1', 'P4'])
    expect(names(moveStop(s, 3, 1))).toEqual(['P0', 'P3', 'P1', 'P2', 'P4'])
    expect(moveStop(s, 0, 2)).toBe(s)
    expect(moveStop(s, 2, 0)).toBe(s)
    expect(moveStop(s, 4, 2)).toBe(s)
    expect(moveStop(s, 2, 4)).toBe(s)
    expect(roles(moveStop(s, 1, 3))).toEqual(['pickup', 'intermediate', 'intermediate', 'intermediate', 'destination'])
  })

  it('keeps stable keys through moves (list identity for animations)', () => {
    const s = fullRoute()
    const moved = moveStop(s, 1, 2)
    expect(moved[2]!.key).toBe(s[1]!.key)
    expect(moved[1]!.key).toBe(s[2]!.key)
  })

  it('never reorders automatically: setting places keeps the order the user chose', () => {
    // A far-away stop in the middle would be "shorter" elsewhere; it must stay put.
    const k = keys()
    let s = createStops(k)
    s = addStop(s, 5, k)
    s = setPlace(s, 0, place('Pickup', 48.8, 58.1))
    s = setPlace(s, 2, place('Destination', 48.81, 58.11))
    s = setPlace(s, 1, place('Far stop', 47.1, 51.9))
    expect(names(s)).toEqual(['Pickup', 'Far stop', 'Destination'])
    expect(toStopInputs(s)!.map((x) => x.address)).toEqual(['Pickup', 'Far stop', 'Destination'])
  })

  it('normalizes a route without changing the order of the stops that remain', () => {
    const s = fullRoute()
    const n = normalizeStops(s, 3, keys())
    expect(names(n)).toEqual(['P0', 'P1', 'P4'])
    expect(roles(n)).toEqual(['pickup', 'intermediate', 'destination'])
    const one = normalizeStops([s[0]!], 5, keys())
    expect(roles(one)).toEqual(['pickup', 'destination'])
    expect(names(one)).toEqual(['P0', null])
  })

  it('builds "order again" stops in the original order', () => {
    const places = [place('A'), place('B', 48.9), place('C', 49)]
    const s = stopsFromPlaces(places, 5, keys())
    expect(names(s)).toEqual(['A', 'B', 'C'])
    expect(roles(s)).toEqual(['pickup', 'intermediate', 'destination'])
  })

  it('reports points outside Kazakhstan and rounds coordinates for the API', () => {
    let s = createStops(keys())
    s = setPlace(s, 0, place('A', 48.82641234, 58.14421234))
    s = setPlace(s, 1, place('Abroad', 55.75, 37.61, false))
    expect(outsideIndices(s)).toEqual([1])
    expect(toStopInputs(s)![0]).toEqual({ lat: 48.826412, lon: 58.144212, address: 'A' })
  })

  it('fingerprints the routable part so any edit re-quotes', () => {
    const s = fullRoute()
    const f = routeFingerprint(s)
    expect(routeFingerprint(moveStop(s, 1, 2))).not.toBe(f)
    expect(routeFingerprint(setPlace(s, 4, place('Other', 49, 58)))).not.toBe(f)
    expect(routeFingerprint([...s])).toBe(f)
  })
})
