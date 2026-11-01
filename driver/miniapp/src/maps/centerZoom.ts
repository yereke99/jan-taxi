// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// While the centre pin is shown, the point under it is the selection (the
// pickup), so zooming must not move the map beneath it: pinch and scroll zoom
// around the map centre instead of the fingers or the cursor, and double-tap
// zoom, which MapLibre anchors at the tapped point, is off. One-finger
// "double-tap and drag" zoom already keeps the centre.

interface ZoomHandler {
  isEnabled(): boolean
  enable(options?: { around: 'center' }): void
  disable(): void
}

/** The zoom handlers of a MapLibre map. */
export interface ZoomHandlers {
  touchZoomRotate: ZoomHandler
  scrollZoom: ZoomHandler
  doubleClickZoom: { isEnabled(): boolean; enable(): void; disable(): void }
}

/**
 * Anchors zoom gestures at the map centre and returns a function that
 * restores MapLibre's defaults. Handlers that were off (a non-interactive
 * map) stay off.
 */
export function zoomAroundCenter(m: ZoomHandlers): () => void {
  const touch = m.touchZoomRotate.isEnabled()
  const scroll = m.scrollZoom.isEnabled()
  const doubleClick = m.doubleClickZoom.isEnabled()
  // enable() keeps the anchor of a handler that is already on: switch it off first.
  if (touch) {
    m.touchZoomRotate.disable()
    m.touchZoomRotate.enable({ around: 'center' })
  }
  if (scroll) {
    m.scrollZoom.disable()
    m.scrollZoom.enable({ around: 'center' })
  }
  if (doubleClick) m.doubleClickZoom.disable()
  return () => {
    if (touch) {
      m.touchZoomRotate.disable()
      m.touchZoomRotate.enable()
    }
    if (scroll) {
      m.scrollZoom.disable()
      m.scrollZoom.enable()
    }
    if (doubleClick) m.doubleClickZoom.enable()
  }
}
