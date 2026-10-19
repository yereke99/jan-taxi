// The driver's car on the home map, followed like in the big ride-hailing
// apps: the black car glides between fixes and turns to its heading, and the
// camera keeps it centred until the driver moves the map (resume() brings the
// camera back).
//
// MapLibre's jumpTo/easeTo cancel a gesture in progress, so the camera is not
// touched while a finger or the mouse is down on the map: the driver's drag
// always wins and pauses following (the screen calls pause() on a
// user-initiated movestart; a wheel zoom pauses here).
//
// Cost control: one marker for the life of the map; the animation runs in
// requestAnimationFrame only while a glide or turn is in progress and the map
// is on screen in an active app (`active`); no reactive state changes per
// frame (the marker element is moved and rotated directly with a CSS
// transform); a fix that does not move the car (GPS jitter, poor accuracy)
// costs nothing; weak Android devices (html[data-jt-low-end]) draw at about
// 30 fps instead of 60.

import { Marker, type Map as MapLibreMap } from 'maplibre-gl'
import { onScopeDispose, readonly, ref, watch, type Ref, type ShallowRef } from 'vue'
import { easeOutCubic, judgeFix, lerpPosition, nearestTurn, type TrackPoint } from '../features/location/follow'
import type { LatLon } from '../utils/geo'
import { motionMs, prefersReducedMotion } from '../utils/motion'

// Same artwork as the shared car marker (maps/vehicleMarkers.ts): black body,
// windscreen at the front (top of the drawing = heading 0, north).
const CAR_SVG =
  '<svg viewBox="0 0 22 36" aria-hidden="true">' +
  '<rect x="1" y="1" width="20" height="34" rx="7" fill="#000"/>' +
  '<rect x="4" y="8" width="14" height="7" rx="2" fill="#fff" fill-opacity=".85"/>' +
  '<rect x="4" y="24" width="14" height="5" rx="2" fill="#fff" fill-opacity=".6"/>' +
  '</svg>'

/** Duration of a turn to a new heading. */
const TURN_MS = 450
/** Camera ease when following starts or resumes. */
const RECENTER_MS = 450
const STALE_OPACITY = '0.45'
/** Minimum time between two drawn frames on low-end devices (~30 fps). */
const LOW_END_FRAME_MS = 30

function minFrameMs(): number {
  return typeof document !== 'undefined' && document.documentElement.hasAttribute('data-jt-low-end') ? LOW_END_FRAME_MS : 0
}

export interface FollowCarOptions {
  /** Accessible name of the car marker. */
  label: string
  /** Zoom the camera uses while following. */
  zoom: number
  /** False while the map is off screen or the Mini App is hidden: no frames, no camera work. */
  active: Readonly<Ref<boolean>>
}

export interface FollowCar {
  /** True while the camera keeps the car centred. */
  following: Readonly<Ref<boolean>>
  /** The latest device fix (about once a second); null hides the car. */
  setFix(fix: TrackPoint | null): void
  /** Dims the car when its position is only the last known one. */
  setStale(stale: boolean): void
  /** The driver moved the map: the camera stops following. */
  pause(): void
  /** Centres on the car and follows it again. False while the position is unknown. */
  resume(): boolean
}

export function useFollowCar(map: ShallowRef<MapLibreMap | null>, options: FollowCarOptions): FollowCar {
  const following = ref(true)

  let marker: Marker | null = null
  let markerEl: HTMLElement | null = null
  let body: HTMLElement | null = null
  let stale = false

  /** Last fix that moved the car (the reference for jitter and heading). */
  let accepted: TrackPoint | null = null
  // Position glide: from → to over `glide` ms starting at t0.
  let from: LatLon | null = null
  let to: LatLon | null = null
  let t0 = 0
  let glide = 0
  // Heading turn (unwrapped degrees, so 350° → 10° turns 20°).
  let headingFrom = 0
  let headingTo = 0
  let r0 = -Infinity
  let hasHeading = false

  let raf = 0
  let lastDrawn = -Infinity
  const frameGap = minFrameMs()
  /** The camera has to move to the car (first fix, a jump, resume). */
  let recenterPending = true
  /** A camera ease is running: the per-frame follow waits for it. */
  let easing = false
  let easeToken = 0
  /** Pointers down on the map: hands off the camera until they are released. */
  const pointers = new Set<number>()
  let container: HTMLElement | null = null

  const animated = (): boolean => !prefersReducedMotion()

  function positionAt(now: number): LatLon | null {
    if (!from || !to) return to
    return glide > 0 ? lerpPosition(from, to, (now - t0) / glide) : to
  }

  function headingAt(now: number): number {
    return headingFrom + (headingTo - headingFrom) * easeOutCubic((now - r0) / TURN_MS)
  }

  function ensureMarker(m: MapLibreMap, p: LatLon): void {
    if (marker) return
    const el = document.createElement('div')
    el.className = 'jt-car-marker'
    if (options.label) {
      el.setAttribute('role', 'img')
      el.setAttribute('aria-label', options.label)
    }
    const b = document.createElement('div')
    b.style.cssText = 'display:block;width:22px;height:36px;transform-origin:50% 50%;will-change:transform'
    b.innerHTML = CAR_SVG
    el.append(b)
    el.style.opacity = stale ? STALE_OPACITY : ''
    markerEl = el
    body = b
    marker = new Marker({ element: el, anchor: 'center' }).setLngLat([p.lon, p.lat]).addTo(m)
  }

  function removeMarker(): void {
    marker?.remove()
    marker = null
    markerEl = null
    body = null
  }

  function ease(m: MapLibreMap, center: LatLon): void {
    const token = ++easeToken
    easing = true
    // Registered first: a zero-length ease (reduced motion) ends synchronously.
    m.once('moveend', () => {
      if (token !== easeToken) return
      easing = false
      kick()
    })
    m.easeTo({ center: [center.lon, center.lat], zoom: Math.max(m.getZoom(), options.zoom), duration: motionMs(RECENTER_MS) })
  }

  /** Draws the car (and moves the camera) for time `now`. */
  function draw(m: MapLibreMap, now: number): void {
    const p = positionAt(now)
    if (!p) return
    ensureMarker(m, p)
    marker!.setLngLat([p.lon, p.lat])
    body!.style.transform = `rotate(${(headingAt(now) - m.getBearing()).toFixed(1)}deg)`
    if (!following.value || pointers.size > 0) return
    if (recenterPending) {
      recenterPending = false
      ease(m, to ?? p)
    } else if (!easing) {
      m.jumpTo({ center: [p.lon, p.lat] })
    }
  }

  function frame(now: number): void {
    raf = 0
    const m = map.value
    if (!m || !options.active.value) return
    const busy = now - t0 < glide || now - r0 < TURN_MS
    // Low-end devices skip every other frame while gliding (the last one is always drawn).
    if (busy && now - lastDrawn < frameGap) {
      raf = requestAnimationFrame(frame)
      return
    }
    lastDrawn = now
    draw(m, now)
    if (busy) raf = requestAnimationFrame(frame)
  }

  /** Schedules drawing when there is something to draw and the map is visible. */
  function kick(): void {
    if (raf || !map.value || !options.active.value || !to) return
    raf = requestAnimationFrame(frame)
  }

  function stop(): void {
    cancelAnimationFrame(raf)
    raf = 0
  }

  function onPointerUp(e: PointerEvent): void {
    pointers.delete(e.pointerId)
    if (pointers.size > 0) return
    window.removeEventListener('pointerup', onPointerUp, true)
    window.removeEventListener('pointercancel', onPointerUp, true)
    // A tap without a drag keeps following: put the camera back on the car.
    kick()
  }

  function onPointerDown(e: PointerEvent): void {
    pointers.add(e.pointerId)
    window.addEventListener('pointerup', onPointerUp, true)
    window.addEventListener('pointercancel', onPointerUp, true)
  }

  /** A wheel or trackpad zoom is a deliberate camera move. */
  function onWheel(): void {
    pause()
  }

  function listen(m: MapLibreMap | null): void {
    container?.removeEventListener('pointerdown', onPointerDown)
    container?.removeEventListener('wheel', onWheel)
    window.removeEventListener('pointerup', onPointerUp, true)
    window.removeEventListener('pointercancel', onPointerUp, true)
    pointers.clear()
    container = m ? m.getCanvasContainer() : null
    container?.addEventListener('pointerdown', onPointerDown, { passive: true })
    container?.addEventListener('wheel', onWheel, { passive: true })
  }

  function setFix(fix: TrackPoint | null): void {
    if (!fix) {
      stop()
      removeMarker()
      accepted = from = to = null
      hasHeading = false
      recenterPending = true
      return
    }
    const d = judgeFix(accepted, fix)
    if (!d.accept) return
    accepted = fix
    const now = performance.now()
    const target: LatLon = { lat: fix.lat, lon: fix.lon }
    // Off screen or reduced motion: no animation, the next frame shows the result.
    const smooth = animated() && options.active.value && !d.snap && to !== null
    if (d.heading !== null) {
      const current = hasHeading ? headingAt(now) : d.heading
      headingTo = nearestTurn(current, d.heading)
      headingFrom = smooth && hasHeading ? current : headingTo
      r0 = now
      hasHeading = true
    }
    if (smooth) {
      from = positionAt(now)
      glide = d.glideMs
    } else {
      from = target
      glide = 0
      if (d.snap) recenterPending = true
    }
    to = target
    t0 = now
    kick()
  }

  function setStale(next: boolean): void {
    stale = next
    if (markerEl) markerEl.style.opacity = next ? STALE_OPACITY : ''
  }

  function pause(): void {
    following.value = false
  }

  function resume(): boolean {
    following.value = true
    if (!to) return false
    recenterPending = true
    kick()
    return true
  }

  // The map arrives after its style has loaded (one instance per screen).
  const stopMapWatch = watch(
    map,
    (m, old) => {
      if (old && old !== m) {
        stop()
        removeMarker()
      }
      listen(m)
      recenterPending = true
      kick()
    },
    { immediate: true },
  )

  // Hidden: drop the frame loop. Visible again: draw the latest state once.
  const stopActiveWatch = watch(options.active, (on) => {
    if (on) {
      // Time kept running while hidden, so the glide is over: no catch-up animation.
      glide = 0
      r0 = -Infinity
      headingFrom = headingTo
      kick()
    } else {
      stop()
    }
  })

  onScopeDispose(() => {
    stopMapWatch()
    stopActiveWatch()
    listen(null)
    stop()
    easeToken++
    removeMarker()
  })

  return { following: readonly(following), setFix, setStale, pause, resume }
}
