// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Lettered stop markers A–E (black circle, white letter). Optionally draggable;
// a drag reports the new position so the caller can re-validate and re-quote.
// Per-stop state: `done` greys a completed stop, `pulse` adds a restrained
// pulse (pickup while searching for a driver). Markers that would cover each
// other at the current zoom are fanned out side by side (declutter.ts).

import { Marker, type Map as MapLibreMap } from 'maplibre-gl'
import { onScopeDispose, watch } from 'vue'
import type { LatLon } from '../utils/geo'
import { STOP_LETTERS } from './constants'
import { declutterOffsets } from './declutter'
import type { MapRef } from './context'
import './markers.css'

export interface StopMarkersOptions {
  draggable?: boolean
  /** Fired after the user drops marker `index` at `point`. */
  onDragEnd?: (index: number, point: LatLon) => void
  /** Accessible label per marker, e.g. "Pickup", "Stop 1", "Destination". */
  label?: (index: number, letter: string) => string
}

/** A stop position with optional display state. */
export interface StopMarkerPoint extends LatLon {
  done?: boolean
  pulse?: boolean
  /** Letter override when the list skips stops (e.g. an empty intermediate stop); default by index. */
  letter?: string
}

export interface StopMarkers {
  setStops(stops: readonly StopMarkerPoint[]): void
  setDraggable(draggable: boolean): void
  clear(): void
}

function createElement(): HTMLDivElement {
  const el = document.createElement('div')
  el.setAttribute('role', 'img')
  return el
}

function decorate(el: HTMLElement, letter: string, label: string, p: StopMarkerPoint, draggable: boolean): void {
  // MapLibre adds its own classes to the element: toggle ours, never reset.
  el.classList.add('jt-stop-marker')
  el.classList.toggle('is-done', p.done === true)
  el.classList.toggle('is-pulsing', p.pulse === true)
  el.classList.toggle('is-draggable', draggable)
  if (el.firstChild?.nodeType !== Node.TEXT_NODE || el.textContent !== letter) el.textContent = letter
  el.setAttribute('aria-label', label)
}

/** Marker diameter (28px) plus a small gap: closer markers are fanned out. */
const MARKER_MIN_DISTANCE_PX = 31

export function useStopMarkers(map: MapRef, options: StopMarkersOptions = {}): StopMarkers {
  const markers: Marker[] = []
  let stops: StopMarkerPoint[] = []
  let draggable = options.draggable ?? false
  let dragging = false
  let frame = 0

  /** Fans out markers that would cover each other at the current camera. */
  function layout(): void {
    const m = map.value
    if (!m || markers.length === 0 || dragging) return
    const c = m.getContainer()
    const pts = markers.map((mk) => m.project(mk.getLngLat()))
    const offsets = declutterOffsets(pts, { minDistance: MARKER_MIN_DISTANCE_PX, width: c.clientWidth, height: c.clientHeight })
    markers.forEach((mk, i) => {
      const [dx, dy] = offsets[i]!
      const cur = mk.getOffset()
      if (Math.abs(cur.x - dx) > 0.5 || Math.abs(cur.y - dy) > 0.5) mk.setOffset([dx, dy])
    })
  }

  function scheduleLayout(): void {
    if (frame) return
    frame = requestAnimationFrame(() => {
      frame = 0
      layout()
    })
  }

  let boundTo: MapLibreMap | null = null
  function bind(m: MapLibreMap | null): void {
    if (boundTo === m) return
    boundTo?.off('move', scheduleLayout)
    boundTo?.off('resize', scheduleLayout)
    boundTo = m
    m?.on('move', scheduleLayout)
    m?.on('resize', scheduleLayout)
  }

  function render(): void {
    const m = map.value
    if (!m) return
    bind(m)
    const wanted = stops.slice(0, STOP_LETTERS.length)
    while (markers.length > wanted.length) markers.pop()?.remove()
    wanted.forEach((p, i) => {
      const letter = p.letter ?? STOP_LETTERS[i]!
      const label = options.label?.(i, letter) ?? letter
      const canDrag = draggable && p.done !== true
      const existing = markers[i]
      if (existing) {
        existing.setLngLat([p.lon, p.lat])
        existing.setDraggable(canDrag)
        decorate(existing.getElement(), letter, label, p, canDrag)
        return
      }
      const el = createElement()
      decorate(el, letter, label, p, canDrag)
      const marker = new Marker({ element: el, anchor: 'center', draggable: canDrag })
        .setLngLat([p.lon, p.lat])
        .addTo(m)
      marker.on('dragstart', () => {
        dragging = true
      })
      marker.on('dragend', () => {
        dragging = false
        // The point is where the marker was dropped, including a fan-out offset.
        const ll = m.unproject(m.project(marker.getLngLat()).add(marker.getOffset()))
        const idx = markers.indexOf(marker)
        if (idx >= 0) options.onDragEnd?.(idx, { lat: ll.lat, lon: ll.lng })
      })
      markers.push(marker)
    })
    layout()
  }

  function clear(): void {
    stops = []
    dragging = false
    while (markers.length) markers.pop()?.remove()
  }

  const stop = watch(map, (m) => {
    // A new map instance invalidates markers attached to the old one.
    while (markers.length) markers.pop()?.remove()
    if (m) render()
    else bind(null)
  })

  onScopeDispose(() => {
    stop()
    clear()
    bind(null)
    if (frame) cancelAnimationFrame(frame)
    frame = 0
  })

  return {
    setStops(next) {
      stops = next.map((p) => ({ lat: p.lat, lon: p.lon, done: p.done, pulse: p.pulse, letter: p.letter }))
      render()
    },
    setDraggable(value) {
      draggable = value
      render()
    },
    clear,
  }
}
