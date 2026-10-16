// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Lettered stop markers A–E (black circle, white letter). Optionally draggable;
// a drag reports the new position so the caller can re-validate and re-quote.
// Per-stop state: `done` greys a completed stop, `pulse` adds a restrained
// pulse (pickup while searching for a driver).

import { Marker } from 'maplibre-gl'
import { onScopeDispose, watch } from 'vue'
import type { LatLon } from '../utils/geo'
import { STOP_LETTERS } from './constants'
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

export function useStopMarkers(map: MapRef, options: StopMarkersOptions = {}): StopMarkers {
  const markers: Marker[] = []
  let stops: StopMarkerPoint[] = []
  let draggable = options.draggable ?? false

  function render(): void {
    const m = map.value
    if (!m) return
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
      marker.on('dragend', () => {
        const ll = marker.getLngLat()
        const idx = markers.indexOf(marker)
        if (idx >= 0) options.onDragEnd?.(idx, { lat: ll.lat, lon: ll.lng })
      })
      markers.push(marker)
    })
  }

  function clear(): void {
    stops = []
    while (markers.length) markers.pop()?.remove()
  }

  const stop = watch(map, (m) => {
    // A new map instance invalidates markers attached to the old one.
    while (markers.length) markers.pop()?.remove()
    if (m) render()
  })

  onScopeDispose(() => {
    stop()
    clear()
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
