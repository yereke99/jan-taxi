// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Frames a set of points (stops, driver, route bbox) in the part of the map
// that is actually visible: below the Telegram header/safe area and above the
// bottom sheet.
//
// MapLibre adds the map's camera padding (MapView `padding`, map.setPadding)
// to the padding given to fitBounds. fitToPoints therefore takes the insets
// that really cover the map (sheet, safe areas) plus breathing room, and only
// passes the part the camera padding does not already apply — callers never
// need to know whether the screen keeps its sheet in the camera padding.

import type { Map as MapLibreMap } from 'maplibre-gl'
import type { Insets } from '../telegram'
import { boundsOf, type LatLon } from '../utils/geo'
import { motionMs } from '../utils/motion'
import { FIT_MAX_ZOOM } from './constants'

export interface FitPadding {
  top: number
  right: number
  bottom: number
  left: number
}

export interface FitOptions {
  /** Extra breathing room around the points (default 48px each side). */
  padding?: Partial<FitPadding>
  /** Height of the bottom sheet currently covering the map, px. */
  sheetHeight?: number
  /** Effective safe insets (effectiveSafeInsets(tg.state)). */
  safeArea?: Insets
  maxZoom?: number
  /** Animate the camera (ignored under reduced motion). Default true. */
  animate?: boolean
}

const DEFAULT_PAD = 48
/** Keeps at least this much of the map un-padded so fitBounds stays valid. */
const MIN_VISIBLE = 64

/** Shrinks a pair proportionally so that a + b <= total - MIN_VISIBLE. */
function fitPair(a: number, b: number, total: number): [number, number] {
  const max = Math.max(0, total - MIN_VISIBLE)
  const sum = a + b
  if (sum <= max || sum === 0) return [a, b]
  const k = max / sum
  return [Math.floor(a * k), Math.floor(b * k)]
}

/**
 * Combines the base padding with safe areas and the sheet, scaled down when
 * needed so the remaining visible area is at least MIN_VISIBLE in each axis.
 */
export function computeFitPadding(
  size: { width: number; height: number },
  base: Partial<FitPadding> = {},
  sheetHeight = 0,
  safe: Insets = { top: 0, right: 0, bottom: 0, left: 0 },
): FitPadding {
  const rawTop = (base.top ?? DEFAULT_PAD) + safe.top
  // The sheet already includes the bottom safe area in its height.
  const rawBottom = (base.bottom ?? DEFAULT_PAD) + (sheetHeight > 0 ? sheetHeight : safe.bottom)
  const rawLeft = (base.left ?? DEFAULT_PAD) + safe.left
  const rawRight = (base.right ?? DEFAULT_PAD) + safe.right
  const [top, bottom] = fitPair(rawTop, rawBottom, size.height)
  const [left, right] = fitPair(rawLeft, rawRight, size.width)
  return { top, right, bottom, left }
}

/** The part of `desired` that the camera padding does not already cover. */
export function paddingBeyondCamera(desired: FitPadding, camera: Partial<FitPadding> = {}): FitPadding {
  return {
    top: Math.max(0, desired.top - (camera.top ?? 0)),
    right: Math.max(0, desired.right - (camera.right ?? 0)),
    bottom: Math.max(0, desired.bottom - (camera.bottom ?? 0)),
    left: Math.max(0, desired.left - (camera.left ?? 0)),
  }
}

/** Moves the camera so every point is visible. No-op for an empty list. */
export function fitToPoints(map: MapLibreMap, points: readonly LatLon[], opts: FitOptions = {}): void {
  const bounds = boundsOf(points)
  if (!bounds) return
  const canvas = map.getContainer()
  const desired = computeFitPadding(
    { width: canvas.clientWidth, height: canvas.clientHeight },
    opts.padding,
    opts.sheetHeight,
    opts.safeArea,
  )
  const padding = paddingBeyondCamera(desired, map.getPadding())
  const maxZoom = opts.maxZoom ?? FIT_MAX_ZOOM
  const duration = opts.animate === false ? 0 : motionMs(600)
  const [[w, s], [e, n]] = bounds
  if (w === e && s === n) {
    // easeTo's `padding` would replace the camera padding for good; shift the
    // target within the padded viewport instead.
    const offset: [number, number] = [(padding.left - padding.right) / 2, (padding.top - padding.bottom) / 2]
    map.easeTo({ center: [w, s], zoom: Math.min(maxZoom, Math.max(map.getZoom(), 15)), offset, duration })
    return
  }
  map.fitBounds(bounds, { padding, maxZoom, duration, linear: false })
}
