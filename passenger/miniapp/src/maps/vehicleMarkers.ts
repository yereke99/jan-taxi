// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Driver car marker (rotates with heading, glides between updates) and the
// user's location dot with an accuracy halo sized in real metres.

import { Marker, type Map as MapLibreMap } from 'maplibre-gl'
import { onScopeDispose, watch } from 'vue'
import type { LatLon } from '../utils/geo'
import { prefersReducedMotion } from '../utils/motion'
import type { MapRef } from './context'
import './markers.css'

const CAR_SVG =
  '<svg viewBox="0 0 22 36" aria-hidden="true">' +
  '<rect x="1" y="1" width="20" height="34" rx="7" fill="#000"/>' +
  '<rect x="4" y="8" width="14" height="7" rx="2" fill="#fff" fill-opacity=".85"/>' +
  '<rect x="4" y="24" width="14" height="5" rx="2" fill="#fff" fill-opacity=".6"/>' +
  '</svg>'

const GLIDE_MS = 900

export interface CarPosition extends LatLon {
  /** Degrees clockwise from north; null keeps the previous rotation. */
  heading?: number | null
}

export interface CarMarker {
  update(pos: CarPosition): void
  remove(): void
}

/** Creates a car marker on `map`. Positions glide unless reduced motion is on. */
export function createCarMarker(map: MapLibreMap, initial: CarPosition, label = ''): CarMarker {
  const el = document.createElement('div')
  el.className = 'jt-car-marker'
  el.innerHTML = CAR_SVG
  if (label) {
    el.setAttribute('role', 'img')
    el.setAttribute('aria-label', label)
  }
  const marker = new Marker({ element: el, anchor: 'center', rotationAlignment: 'map', pitchAlignment: 'map' })
    .setLngLat([initial.lon, initial.lat])
    .setRotation(initial.heading ?? 0)
    .addTo(map)

  let frame = 0

  function glide(to: LatLon): void {
    cancelAnimationFrame(frame)
    const from = marker.getLngLat()
    if (prefersReducedMotion()) {
      marker.setLngLat([to.lon, to.lat])
      return
    }
    const start = performance.now()
    const step = (now: number): void => {
      const t = Math.min(1, (now - start) / GLIDE_MS)
      const e = 1 - (1 - t) ** 3
      marker.setLngLat([from.lng + (to.lon - from.lng) * e, from.lat + (to.lat - from.lat) * e])
      if (t < 1) frame = requestAnimationFrame(step)
    }
    frame = requestAnimationFrame(step)
  }

  return {
    update(pos) {
      if (typeof pos.heading === 'number' && Number.isFinite(pos.heading)) marker.setRotation(pos.heading)
      glide(pos)
    },
    remove() {
      cancelAnimationFrame(frame)
      marker.remove()
    },
  }
}

/** Metres per CSS pixel at a latitude and zoom (512px tiles, Web Mercator). */
export function metersPerPixel(lat: number, zoom: number): number {
  return (40_075_016.686 * Math.abs(Math.cos((lat * Math.PI) / 180))) / (512 * 2 ** zoom)
}

export interface UserLocation extends LatLon {
  accuracy_m?: number | null
}

export interface UserLocationMarker {
  update(loc: UserLocation): void
  remove(): void
}

const MAX_HALO_PX = 600

export function createUserLocationMarker(map: MapLibreMap, initial: UserLocation, label = ''): UserLocationMarker {
  const el = document.createElement('div')
  el.className = 'jt-user-location'
  if (label) {
    el.setAttribute('role', 'img')
    el.setAttribute('aria-label', label)
  }
  const halo = document.createElement('div')
  halo.className = 'jt-user-location__halo'
  const dot = document.createElement('div')
  dot.className = 'jt-user-location__dot'
  el.append(halo, dot)

  let current = initial
  const marker = new Marker({ element: el, anchor: 'center' }).setLngLat([initial.lon, initial.lat]).addTo(map)

  function sizeHalo(): void {
    const acc = current.accuracy_m
    if (typeof acc !== 'number' || !Number.isFinite(acc) || acc <= 0) {
      halo.style.display = 'none'
      return
    }
    const diameter = Math.min(MAX_HALO_PX, (2 * acc) / metersPerPixel(current.lat, map.getZoom()))
    halo.style.display = diameter < 24 ? 'none' : 'block'
    halo.style.width = `${diameter}px`
    halo.style.height = `${diameter}px`
  }

  map.on('zoom', sizeHalo)
  sizeHalo()

  return {
    update(loc) {
      current = loc
      marker.setLngLat([loc.lon, loc.lat])
      sizeHalo()
    },
    remove() {
      map.off('zoom', sizeHalo)
      marker.remove()
    },
  }
}

/** Composable: keeps a car marker in sync with `map`; set(null) hides it. */
export function useCarMarker(map: MapRef, label = ''): { set(pos: CarPosition | null): void } {
  let marker: CarMarker | null = null
  let last: CarPosition | null = null
  const apply = (): void => {
    const m = map.value
    if (!m || !last) {
      marker?.remove()
      marker = null
      return
    }
    if (marker) marker.update(last)
    else marker = createCarMarker(m, last, label)
  }
  const stop = watch(map, () => {
    marker?.remove()
    marker = null
    apply()
  })
  onScopeDispose(() => {
    stop()
    marker?.remove()
  })
  return {
    set(pos) {
      last = pos
      apply()
    },
  }
}

/** Composable: keeps the user-location dot in sync with `map`; set(null) hides it. */
export function useUserLocationMarker(map: MapRef, label = ''): { set(loc: UserLocation | null): void } {
  let marker: UserLocationMarker | null = null
  let last: UserLocation | null = null
  const apply = (): void => {
    const m = map.value
    if (!m || !last) {
      marker?.remove()
      marker = null
      return
    }
    if (marker) marker.update(last)
    else marker = createUserLocationMarker(m, last, label)
  }
  const stop = watch(map, () => {
    marker?.remove()
    marker = null
    apply()
  })
  onScopeDispose(() => {
    stop()
    marker?.remove()
  })
  return {
    set(loc) {
      last = loc
      apply()
    },
  }
}
