// The followed car: one marker moved and rotated directly, frames only while
// gliding on a visible map, the camera on the car until the driver pans.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref, shallowRef, type EffectScope } from 'vue'
import type { Map as MapLibreMap } from 'maplibre-gl'
import type { TrackPoint } from '../features/location/follow'
import { useFollowCar, type FollowCar } from './useFollowCar'

const markers = vi.hoisted(() => [] as Array<{ el: HTMLElement; lngLat: [number, number]; removed: boolean }>)

vi.mock('maplibre-gl', () => ({
  Marker: class {
    rec: { el: HTMLElement; lngLat: [number, number]; removed: boolean }
    constructor(opts: { element: HTMLElement }) {
      this.rec = { el: opts.element, lngLat: [0, 0], removed: false }
      markers.push(this.rec)
    }
    setLngLat(l: [number, number]) {
      this.rec.lngLat = l
      return this
    }
    addTo() {
      return this
    }
    remove() {
      this.rec.removed = true
    }
  },
}))

/** ~1 m of latitude / longitude (at 48.8°) in degrees. */
const M_LAT = 1 / 111_195
const M_LON = 1 / (111_195 * Math.cos((48.82 * Math.PI) / 180))

function fakeMap() {
  const once: Array<() => void> = []
  const container = document.createElement('div')
  return {
    container,
    getCanvasContainer: () => container,
    jumpTo: vi.fn(),
    easeTo: vi.fn(() => {
      // MapLibre fires moveend when the ease completes.
      for (const cb of once.splice(0)) cb()
    }),
    once: vi.fn((_type: string, cb: () => void) => once.push(cb)),
    getZoom: () => 14,
    getBearing: () => 0,
  }
}

let frames: Array<FrameRequestCallback> = []
let clock = 0
let scope: EffectScope

function runFrame(at: number): void {
  clock = at
  const due = frames
  frames = []
  for (const cb of due) cb(at)
}

function fix(over: Partial<TrackPoint> = {}): TrackPoint {
  return { lat: 48.82, lon: 58.14, accuracy_m: 8, heading: null, speed_mps: null, timestamp: 1_000_000, ...over }
}

function setup(active = true) {
  const m = fakeMap()
  const map = shallowRef<MapLibreMap | null>(m as unknown as MapLibreMap)
  const visible = ref(active)
  const follow = scope.run(() => useFollowCar(map, { label: 'car', zoom: 16, active: visible }))! as FollowCar
  return { m, follow, visible }
}

const car = () => markers[markers.length - 1]!
const rotation = () => car().el.firstElementChild!.getAttribute('style') ?? ''

beforeEach(() => {
  markers.length = 0
  frames = []
  clock = 0
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => frames.push(cb))
  vi.stubGlobal('cancelAnimationFrame', () => {
    frames = []
  })
  vi.spyOn(performance, 'now').mockImplementation(() => clock)
  scope = effectScope()
})

afterEach(() => {
  scope.stop()
  vi.unstubAllGlobals()
})

describe('useFollowCar', () => {
  it('places the car on the first fix and brings the camera to it', () => {
    const { m, follow } = setup()
    follow.setFix(fix())
    runFrame(16)
    expect(markers).toHaveLength(1)
    expect(car().lngLat).toEqual([58.14, 48.82])
    expect(m.easeTo).toHaveBeenCalledWith(expect.objectContaining({ center: [58.14, 48.82], zoom: 16 }))
  })

  it('glides to the next fix, turns to the derived heading and keeps the camera on it', () => {
    const { m, follow } = setup()
    const a = fix()
    follow.setFix(a)
    runFrame(0)
    // 20 m east one second later, no device course.
    clock = 1000
    follow.setFix({ ...a, lon: a.lon + 20 * M_LON, timestamp: a.timestamp + 1000 })
    runFrame(1500)
    const mid = car().lngLat[0]
    expect(mid).toBeGreaterThan(a.lon)
    expect(mid).toBeLessThan(a.lon + 20 * M_LON)
    expect(m.jumpTo).toHaveBeenLastCalledWith({ center: car().lngLat })
    runFrame(2300)
    expect(car().lngLat[0]).toBeCloseTo(a.lon + 20 * M_LON, 9)
    expect(rotation()).toContain('rotate(90.0deg)')
    expect(frames).toHaveLength(0) // at rest: no more frames
    expect(markers).toHaveLength(1) // one marker for the life of the map
  })

  it('ignores jitter without scheduling frames', () => {
    const { follow } = setup()
    const a = fix()
    follow.setFix(a)
    runFrame(0)
    runFrame(16) // the catch-up frame after the camera ease
    expect(frames).toHaveLength(0)
    follow.setFix({ ...a, lat: a.lat + 1.5 * M_LAT, timestamp: a.timestamp + 1000 })
    expect(frames).toHaveLength(0)
  })

  it('pauses following when the driver moves the map and resumes on request', () => {
    const { m, follow } = setup()
    const a = fix()
    follow.setFix(a)
    runFrame(0)
    follow.pause()
    expect(follow.following.value).toBe(false)
    m.jumpTo.mockClear()
    m.easeTo.mockClear()
    clock = 1000
    follow.setFix({ ...a, lat: a.lat + 30 * M_LAT, timestamp: a.timestamp + 1000 })
    runFrame(1500)
    runFrame(2300)
    expect(car().lngLat[1]).toBeCloseTo(a.lat + 30 * M_LAT, 9) // the car still moves
    expect(m.jumpTo).not.toHaveBeenCalled() // the camera stays where the driver put it
    expect(follow.resume()).toBe(true)
    runFrame(2200)
    expect(m.easeTo).toHaveBeenCalledWith(expect.objectContaining({ center: car().lngLat }))
    expect(follow.following.value).toBe(true)
  })

  it('keeps its hands off the camera while the driver touches the map', () => {
    const { m, follow } = setup()
    const a = fix()
    follow.setFix(a)
    runFrame(0)
    runFrame(16)
    // A finger goes down (MapLibre would cancel the drag on jumpTo/easeTo).
    m.container.dispatchEvent(Object.assign(new Event('pointerdown'), { pointerId: 7 }))
    m.jumpTo.mockClear()
    clock = 1000
    follow.setFix({ ...a, lat: a.lat + 30 * M_LAT, timestamp: a.timestamp + 1000 })
    runFrame(1500)
    expect(m.jumpTo).not.toHaveBeenCalled()
    expect(car().lngLat[1]).toBeGreaterThan(a.lat) // the car keeps moving
    // A tap (no drag, so still following): released → the camera catches up.
    window.dispatchEvent(Object.assign(new Event('pointerup'), { pointerId: 7 }))
    runFrame(1600)
    expect(m.jumpTo).toHaveBeenCalled()
    expect(follow.following.value).toBe(true)
  })

  it('a wheel zoom pauses following', () => {
    const { m, follow } = setup()
    m.container.dispatchEvent(new Event('wheel'))
    expect(follow.following.value).toBe(false)
  })

  it('does no frame work while the map is hidden and shows the latest state when visible again', async () => {
    const { follow, visible } = setup()
    const a = fix()
    follow.setFix(a)
    runFrame(0)
    visible.value = false
    await nextTick()
    expect(frames).toHaveLength(0)
    clock = 1000
    follow.setFix({ ...a, lat: a.lat + 30 * M_LAT, timestamp: a.timestamp + 1000 })
    expect(frames).toHaveLength(0)
    visible.value = true
    await nextTick()
    expect(frames).toHaveLength(1)
    runFrame(1016)
    expect(car().lngLat[1]).toBeCloseTo(a.lat + 30 * M_LAT, 9)
    expect(frames).toHaveLength(0)
  })

  it('without a position: resume reports it and the car is hidden', () => {
    const { follow } = setup()
    expect(follow.resume()).toBe(false)
    follow.setFix(fix())
    runFrame(0)
    follow.setFix(null)
    expect(car().removed).toBe(true)
  })

  it('draws at about 30 fps on low-end devices', () => {
    document.documentElement.setAttribute('data-jt-low-end', '')
    try {
      const { follow } = setup()
      const a = fix()
      follow.setFix(a)
      runFrame(0)
      runFrame(16)
      clock = 1000
      follow.setFix({ ...a, lat: a.lat + 30 * M_LAT, timestamp: a.timestamp + 1000 })
      runFrame(1100)
      const drawn = car().lngLat[1]
      runFrame(1116) // skipped: too soon after the last drawn frame
      expect(car().lngLat[1]).toBe(drawn)
      expect(frames).toHaveLength(1)
      runFrame(1133)
      expect(car().lngLat[1]).toBeGreaterThan(drawn)
    } finally {
      document.documentElement.removeAttribute('data-jt-low-end')
    }
  })

  it('dims the car at the last known position', () => {
    const { follow } = setup()
    follow.setFix(fix())
    runFrame(0)
    follow.setStale(true)
    expect(car().el.style.opacity).toBe('0.45')
    follow.setStale(false)
    expect(car().el.style.opacity).toBe('')
  })
})
