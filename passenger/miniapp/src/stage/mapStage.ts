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
import { boundsOf, haversineMeters, type LatLon } from '../utils/geo'
import { motionMs } from '../utils/motion'

/**
 * Free map area: the camera keeps this margin below the top safe area (which
 * includes Telegram's fullscreen header) and above the sheet (which includes
 * the floating tab bar), and FIT_SIDE_PX from the screen sides when framing.
 * FIT_MARKER_PX more keeps whole stop markers (28px circles centred on their
 * points) inside that area.
 */
const MAP_MARGIN_PX = 16
const FIT_SIDE_PX = 40
const FIT_MARKER_PX = 18
/** Extra right margin while the floating locate/recenter buttons sit above the sheet. */
const FIT_CONTROLS_PX = 50
const FIT_MAX_ZOOM = 16
const FOCUS_ZOOM = 16
/** Floating top button row (8px + 48px button + 8px) above a pin screen. */
const TOP_BUTTON_ROW_PX = 64
/** Centre pin plus its address callout (and the lift while dragging), above the pin tip. */
const PIN_WITH_CALLOUT_PX = 120
/** Camera moves farther than this (and off screen) jump instead of animating. */
const JUMP_DISTANCE_M = 30_000
/** Smallest map area a route is framed in; margins shrink before it does. */
const MIN_FIT_AREA_PX = 40

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
  /** True while the passenger drags or zooms the map (not during programmatic moves). */
  gesture: Ref<boolean>
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
  centerOn(point: LatLon, zoom?: number, opts?: { duration?: number; linear?: boolean }): void
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
  const pin = reactive<MapStage['pin']>({ visible: false, label: '', callout: null, onCallout: null })
  const controls = reactive<StageControls>(defaultControls())
  const padding = computed<MapPadding>(() => {
    const s = safe.value
    const bottom = Math.max(sheetHeight.value, s.bottom) + MAP_MARGIN_PX
    let top = s.top + MAP_MARGIN_PX
    // The centre pin sits in the middle of the padded area. With a tall sheet
    // that middle moves up until the address callout runs into the floating
    // top button (or Telegram's header); the padding then lowers the pin.
    if (pin.visible && pin.callout) {
      const h = tg.state.viewportHeight || window.innerHeight
      const topButton = controls.menu || (controls.back && !tg.state.isTelegram)
      const minTip = s.top + (topButton ? TOP_BUTTON_ROW_PX : 0) + PIN_WITH_CALLOUT_PX
      const needed = 2 * minTip - (h - bottom)
      if (needed > top && needed < h - bottom - MIN_FIT_AREA_PX) top = needed
    }
    return { top, right: s.right, bottom, left: s.left }
  })

  /**
   * Screen padding for framing: the free map area between the top safe area
   * and the sheet, minus margins for whole markers. When a tall sheet leaves
   * little room the margins shrink first (down to MIN_FIT_AREA_PX of map), so
   * the route still fits between the header and the sheet; only a sheet that
   * covers almost everything makes the bottom margin give way.
   */
  function fitPadding(m: MapLibreMap): MapPadding {
    const c = m.getContainer()
    const p = padding.value
    const w = c.clientWidth
    const h = c.clientHeight
    const top0 = safe.value.top
    const bottom0 = Math.max(sheetHeight.value, safe.value.bottom)
    const free = h - top0 - bottom0
    const margin = Math.max(0, Math.min(MAP_MARGIN_PX + FIT_MARKER_PX, (free - MIN_FIT_AREA_PX) / 2))
    const top = Math.min(top0 + margin, Math.max(0, h - MIN_FIT_AREA_PX))
    const bottom = Math.max(0, Math.min(bottom0 + margin, h - top - MIN_FIT_AREA_PX))
    const side = Math.min(FIT_SIDE_PX, Math.max(0, (w - MIN_FIT_AREA_PX) / 2))
    const buttons = controls.locate || controls.recenter !== null ? Math.min(FIT_CONTROLS_PX, Math.max(0, w - 2 * side - MIN_FIT_AREA_PX)) : 0
    return { top, bottom, left: p.left + side, right: p.right + side + buttons }
  }

  // --- Camera ----------------------------------------------------------------
  // Programmatic moves are remembered until they finish: when the free area
  // changes meanwhile (a sheet appears, grows or docks) the move is re-run for
  // the new padding instead of being cut short by the padding animation, so a
  // framed route or a centred point always ends up in the visible area.

  type CameraOp =
    | { kind: 'fit'; points: readonly LatLon[]; maxZoom?: number }
    | { kind: 'center'; point: LatLon; zoom?: number; linear?: boolean }

  let activeOp: CameraOp | null = null
  const gesture = ref(false)
  let paddingDirty = false

  /** Cross-country moves (e.g. from the default city to the passenger) jump instead of flying. */
  function farAway(m: MapLibreMap, point: LatLon): boolean {
    const c = m.getCenter()
    return !m.getBounds().contains([point.lon, point.lat]) && haversineMeters({ lat: c.lat, lon: c.lng }, point) > JUMP_DISTANCE_M
  }

  function runOp(m: MapLibreMap, op: CameraOp, requested: number): void {
    const target = op.kind === 'center' ? op.point : op.points[0]
    const duration = target && farAway(m, target) ? 0 : requested
    if (op.kind === 'center') {
      m.easeTo({
        center: [op.point.lon, op.point.lat],
        zoom: op.zoom ?? Math.max(m.getZoom(), FOCUS_ZOOM - 1),
        padding: padding.value,
        duration,
        ...(op.linear ? { easing: (t: number) => t } : {}),
      })
    } else {
      const bounds = boundsOf(op.points)
      if (!bounds) return
      const pad = fitPadding(m)
      const [[w, s], [e, n]] = bounds
      if (Math.abs(e - w) < 1e-6 && Math.abs(n - s) < 1e-6) {
        m.easeTo({ center: [w, s], zoom: Math.max(m.getZoom(), FOCUS_ZOOM - 1), padding: padding.value, duration })
      } else {
        // Absolute padding: the free area is computed here from the current
        // safe areas and sheet height, not added to the map's padding.
        try {
          m.fitBounds(bounds, { padding: pad, absolutePadding: true, maxZoom: op.maxZoom ?? FIT_MAX_ZOOM, duration, linear: false })
        } catch {
          // No room at all (sheet fully expanded): keep the camera.
          return
        }
      }
    }
    // A jump finishes synchronously; only animations stay "active".
    activeOp = duration > 0 ? op : null
  }

  function applyPadding(m: MapLibreMap): void {
    paddingDirty = false
    if (activeOp) runOp(m, activeOp, motionMs(350))
    else m.easeTo({ padding: padding.value, duration: motionMs(250) })
  }

  watch(
    padding,
    () => {
      const m = map.value
      if (!m) return
      // Never fight the passenger's finger: apply once the gesture ends.
      if (gesture.value) paddingDirty = true
      else applyPadding(m)
    },
    { deep: true },
  )

  let dragHandler: ((index: number, point: LatLon) => void) | null = null
  const stops = useStopMarkers(map, {
    onDragEnd: (index, point) => dragHandler?.(index, point),
    label: labels.stop,
  })
  const route = useRouteLayer(map)
  const car = useCarMarker(map, labels.car)
  const user = useUserLocationMarker(map, labels.user)

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
    m.setPadding(padding.value)
    m.on('movestart', (e) => {
      const user = e.originalEvent !== undefined
      if (user) {
        gesture.value = true
        activeOp = null
      }
      for (const cb of [...moveStart]) cb(user)
    })
    m.on('moveend', (e) => {
      const user = e.originalEvent !== undefined
      // An interrupted animation also ends here; runOp re-marks a re-run one.
      activeOp = null
      if (gesture.value) {
        gesture.value = false
        if (paddingDirty) applyPadding(m)
      }
      const c = centerOf(m)
      for (const cb of [...moveEnd]) cb(c, user)
    })
  }

  const stage: MapStage = {
    map,
    gesture,
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
      if (!m || !boundsOf(points)) return
      onCameraTaken()
      runOp(m, { kind: 'fit', points: [...points], maxZoom: opts.maxZoom }, opts.animate === false ? 0 : motionMs(500))
    },
    centerOn(point, zoom, opts = {}) {
      const m = map.value
      if (!m) return
      runOp(m, { kind: 'center', point, zoom, linear: opts.linear }, opts.duration ?? motionMs(450))
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
