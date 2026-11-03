// Shared with the other Mini App — keep both copies identical (make verify-shared).

/**
 * Safari 15 ignores overscroll-behavior. Cancel only outward vertical drags in
 * app scroll views on those clients; leave map/sheet gestures and native
 * scrolling (including a nested textarea) to their own handlers.
 */
export function installScrollBoundaryGuard(doc: Document): () => void {
  const win = doc.defaultView
  if (!win || win.CSS?.supports?.('overscroll-behavior', 'none')) return () => undefined

  let gesture: { root: Element; target: Element; x: number; y: number; lastY: number } | null = null

  function start(event: TouchEvent): void {
    gesture = null
    const touch = event.touches[0]
    const target = event.target
    if (event.touches.length !== 1 || !touch || !(target instanceof Element)) return
    if (target.closest('.maplibregl-map, .jt-sheet, input[type="range"]')) return
    let root = target.closest('.jt-scroll')
    if (!root) return
    // A nested app scroll view can hand the drag to its enclosing page.
    let parent = root.parentElement?.closest('.jt-scroll')
    while (parent) {
      root = parent
      parent = root.parentElement?.closest('.jt-scroll')
    }
    gesture = { root, target, x: touch.clientX, y: touch.clientY, lastY: touch.clientY }
  }

  function move(event: TouchEvent): void {
    const touch = event.touches[0]
    if (!gesture || !touch || event.touches.length !== 1) {
      gesture = null
      return
    }
    const { root, target, x, y, lastY } = gesture
    gesture.lastY = touch.clientY
    if (!root.isConnected || !root.contains(target)) return
    const delta = touch.clientY - lastY
    if (!delta || Math.abs(touch.clientY - y) <= Math.abs(touch.clientX - x)) return

    for (let el: Element | null = target; el; el = el.parentElement) {
      const maximum = el.scrollHeight - el.clientHeight
      const scrollable = el === root || /^(auto|scroll|overlay)$/.test(win!.getComputedStyle(el).overflowY)
      if (scrollable && maximum > 1) {
        const top = Math.max(0, Math.min(maximum, el.scrollTop))
        if ((delta > 0 && top > 0) || (delta < 0 && top < maximum - 1)) return
      }
      if (el === root) break
    }
    if (event.cancelable) event.preventDefault()
  }

  function end(): void {
    gesture = null
  }

  doc.addEventListener('touchstart', start, { passive: true })
  doc.addEventListener('touchmove', move, { passive: false })
  doc.addEventListener('touchend', end, { passive: true })
  doc.addEventListener('touchcancel', end, { passive: true })
  return () => {
    end()
    doc.removeEventListener('touchstart', start)
    doc.removeEventListener('touchmove', move)
    doc.removeEventListener('touchend', end)
    doc.removeEventListener('touchcancel', end)
  }
}
