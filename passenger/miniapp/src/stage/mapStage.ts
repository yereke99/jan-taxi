// The map stage: one MapLibre map (created once by MapShell) shared by every
// map-first screen — home, route editor, place picker, confirmation and the
// live order. Each screen renders only its bottom sheet and drives the map
// through this context; whatever a screen puts on the map is removed when it
// unmounts (useStageScreen), so the next screen starts clean.

import type { Map as MapLibreMap } from 'maplibre-gl'
import { computed, inject, onBeforeUnmount, reactive, ref, shallowRef, watch, type ComputedRef, type InjectionKey, type Ref, type ShallowRef } from 'vue'
import type { MapPadding, PinCallout, RouteLayer, StopMarkers } from '../maps'
import { useCarMarker, useRouteLayer, useStopMarkers, useUserLocationMarker, type CarPosition, type UserLocation } from '../maps'
import { effectiveSafeInsets, useTelegramFullscreen, type Insets } from '../telegram'
import { boundsOf, type LatLon } from '../utils/geo'
import { motionMs } from '../utils/motion'

/**
 * Free map area: the camera keeps this margin below the top safe area (which
 * includes Telegram's fullscreen header) and above the sheet (which includes
 * the floating tab bar), and FIT_SIDE_PX from the screen sides when framing.
 */
const MAP_MARGIN_PX = 16
const FIT_SIDE_PX = 32
const FIT_MAX_ZOOM = 16
const FOCUS_ZOOM = 16
/** Smallest free area a fit is attempted in; below it the sheet margin gives way. */
const MIN_FIT_AREA_PX = 96

export interface StageControls {
  /** Profile/menu button (home only). */
  menu: boolean
  /** Glass back button in the same place (booking steps). */
  back: boolean
  /** Locate button (centres on the passenger; in picker mode moves the pin there). */
  locate: boolean
  /** Re-frames the screen's content (live order). */
  recenter: (() => void) | null
  /** Shows the low-GPS-accuracy chip. */
  accuracyChip: boolean
}

export interface MapStage {
  map: ShallowRef<MapLibreMap | null>
  sheetHeight: Ref<number>
  safe: ComputedRef<Insets>
  /** Camera padding: safe area + controls on top, the sheet at the bottom. */
  padding: ComputedRef<MapPadding>
  stops: StopMarkers
  route: RouteLayer
  car: { set(pos: CarPosition | null): void }
  user: { set(loc: UserLocation | null): void }
  /** Centre pin ("move the map under the pin") with its optional address callout. */
  pin: { visible: boolean; label: string; callout: PinCallout | null; onCallout: (() => void) | null }
  controls: StageControls
  /** Handler for dragged stop markers (route editor). */
  setStopDrag(handler: ((index: number, point: LatLon) => void) | null): void
  /** Frames the points above the sheet; one point is centred at a street zoom. */
  fit(points: readonly LatLon[], opts?: { animate?: boolean; maxZoom?: number }): void
  /** Centres on a point, keeping the zoom unless one is given. */
  centerOn(point: LatLon, zoom?: number): void
  /** Map centre under the pin (the padded centre), null before the map exists. */
  center(): LatLon | null
  onMoveStart(cb: (userInitiated: boolean) => void): () => void
  onMoveEnd(cb: (center: LatLon, userInitiated: boolean) => void): () => void
  /** Makes the calling screen the stage owner, starting from a clean map. */
  claim(): symbol
  /** Clears the map if `token` still owns the stage. */
  release(token: symbol): void
}

export const STAGE_KEY: InjectionKey<MapStage> = Symbol('jt-map-stage')

function defaultControls(): StageControls {
  return { menu: false, back: false, locate: false, recenter: null, accuracyChip: false }
}

/**
 * Builds the stage; MapShell calls it once, provides it and calls attach() when
 * the map loads. `onCameraTaken` runs whenever a screen takes over the camera
 * (a fit or a new screen), e.g. to end "follow my location".
 */
export function createMapStage(
  labels: { stop: (index: number, letter: string) => string; car: string; user: string },
  onCameraTaken: () => void = () => undefined,
): {
  stage: MapStage
  attach: (m: MapLibreMap) => void
} {
  const tg = useTelegramFullscreen()
  const map = shallowRef<MapLibreMap | null>(null)
  const sheetHeight = ref(0)
  const safe = computed(() => effectiveSafeInsets(tg.state))
  const padding = computed<MapPadding>(() => ({
    top: safe.value.top + MAP_MARGIN_PX,
    right: safe.value.right,
    bottom: Math.max(sheetHeight.value, safe.value.bottom) + MAP_MARGIN_PX,
    left: safe.value.left,
  }))

  /** Screen padding for framing: the free map area, never smaller than MIN_FIT_AREA_PX. */
  function fitPadding(m: MapLibreMap): MapPadding {
    const c = m.getContainer()
    const p = padding.value
    const side = Math.min(FIT_SIDE_PX, Math.max(0, (c.clientWidth - MIN_FIT_AREA_PX) / 2))
    const top = Math.min(p.top, Math.max(0, c.clientHeight - MIN_FIT_AREA_PX))
    const bottom = Math.max(0, Math.min(p.bottom, c.clientHeight - top - MIN_FIT_AREA_PX))
    return { top, bottom, left: p.left + side, right: p.right + side }
  }

  let dragHandler: ((index: number, point: LatLon) => void) | null = null
  const stops = useStopMarkers(map, {
    onDragEnd: (index, point) => dragHandler?.(index, point),
    label: labels.stop,
  })
  const route = useRouteLayer(map)
  const car = useCarMarker(map, labels.car)
  const user = useUserLocationMarker(map, labels.user)
  const pin = reactive<MapStage['pin']>({ visible: false, label: '', callout: null, onCallout: null })
  const controls = reactive<StageControls>(defaultControls())

  let owner: symbol | null = null

  function clear(): void {
    dragHandler = null
    stops.clear()
    stops.setDraggable(false)
    route.clear()
    car.set(null)
    pin.visible = false
    pin.label = ''
    pin.callout = null
    pin.onCallout = null
    Object.assign(controls, defaultControls())
  }

  const moveStart = new Set<(userInitiated: boolean) => void>()
  const moveEnd = new Set<(center: LatLon, userInitiated: boolean) => void>()

  function centerOf(m: MapLibreMap): LatLon {
    const c = m.getCenter()
    return { lat: c.lat, lon: c.lng }
  }

  function attach(m: MapLibreMap): void {
    m.on('movestart', (e) => {
      for (const cb of [...moveStart]) cb(e.originalEvent !== undefined)
    })
    m.on('moveend', (e) => {
      const c = centerOf(m)
      for (const cb of [...moveEnd]) cb(c, e.originalEvent !== undefined)
    })
  }

  const stage: MapStage = {
    map,
    sheetHeight,
    safe,
    padding,
    stops,
    route,
    car,
    user,
    pin,
    controls,
    setStopDrag(handler) {
      dragHandler = handler
      stops.setDraggable(handler !== null)
    },
    fit(points, opts = {}) {
      const m = map.value
      const bounds = boundsOf(points)
      if (!m || !bounds) return
      onCameraTaken()
      const duration = opts.animate === false ? 0 : motionMs(500)
      const pad = fitPadding(m)
      const [[w, s], [e, n]] = bounds
      if (Math.abs(e - w) < 1e-6 && Math.abs(n - s) < 1e-6) {
        m.easeTo({ center: [w, s], zoom: Math.max(m.getZoom(), FOCUS_ZOOM - 1), padding: pad, duration })
        return
      }
      // Absolute padding: the free area is computed here from the current
      // safe areas and sheet height, not added to a padding that may still be
      // animating; the map keeps this padding afterwards.
      try {
        m.fitBounds(bounds, { padding: pad, absolutePadding: true, maxZoom: opts.maxZoom ?? FIT_MAX_ZOOM, duration, linear: false })
      } catch {
        // No room at all (sheet fully expanded): keep the camera.
      }
    },
    centerOn(point, zoom) {
      map.value?.easeTo({ center: [point.lon, point.lat], zoom: zoom ?? Math.max(map.value.getZoom(), FOCUS_ZOOM - 1), duration: motionMs(450) })
    },
    center() {
      return map.value ? centerOf(map.value) : null
    },
    onMoveStart(cb) {
      moveStart.add(cb)
      return () => moveStart.delete(cb)
    },
    onMoveEnd(cb) {
      moveEnd.add(cb)
      return () => moveEnd.delete(cb)
    },
    claim() {
      onCameraTaken()
      clear()
      owner = Symbol('screen')
      return owner
    },
    release(token) {
      if (owner !== token) return
      owner = null
      clear()
    },
  }

  return { stage, attach }
}

export function useMapStage(): MapStage {
  const stage = inject(STAGE_KEY, null)
  if (!stage) throw new Error('useMapStage() outside MapShell')
  return stage
}

/**
 * For a screen rendered inside MapShell: takes over the stage (clean map) and
 * clears the screen's map content when it unmounts.
 */
export function useStageScreen(): MapStage {
  const stage = useMapStage()
  const token = stage.claim()
  onBeforeUnmount(() => stage.release(token))
  return stage
}

/** Wait for the sheet/padding animation (MapView eases padding over 250 ms) before fitting. */
const AUTO_FIT_DELAY_MS = 320

export interface AutoFit {
  /** Frames the screen's points; `force` also overrides a camera the user moved. */
  refit(force?: boolean): void
}

/**
 * Keeps the screen's points framed above its sheet: refits when asked (new
 * content) and after the sheet height settles, unless the passenger has moved
 * the map since the last requested fit. Viewport resizes keep the camera.
 */
export function useAutoFit(stage: MapStage, points: () => LatLon[]): AutoFit {
  let timer: ReturnType<typeof setTimeout> | undefined
  let userMoved = false
  const offMove = stage.onMoveStart((user) => {
    if (user) userMoved = true
  })

  function schedule(): void {
    clearTimeout(timer)
    timer = setTimeout(() => {
      if (stage.map.value) stage.fit(points())
    }, AUTO_FIT_DELAY_MS)
  }

  const stopHeight = watch(stage.sheetHeight, () => {
    if (!userMoved) schedule()
  })
  const stopMap = watch(stage.map, (m) => {
    if (m) schedule()
  })

  onBeforeUnmount(() => {
    clearTimeout(timer)
    offMove()
    stopHeight()
    stopMap()
  })

  return {
    refit(force = true) {
      if (force) userMoved = false
      else if (userMoved) return
      schedule()
    },
  }
}
