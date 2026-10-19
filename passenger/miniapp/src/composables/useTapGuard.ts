// Tells a deliberate tap from the end of a scroll or a sheet drag, for rows
// whose click changes something that costs money (the vehicle class).
//
// A row counts as tapped only when the pointer went down and up on it, moved
// less than TAP_SLOP_PX, nothing scrolled in between and the row itself did
// not move under the finger (the sheet snapping or being dragged). A click
// without such a press is ignored, except a keyboard or assistive-technology
// activation (click with detail 0), which is always deliberate.
//
//   const tap = useTapGuard()
//   <button v-on="tap.handlers" @click="tap.isTap($event) && choose(o)">

import { onBeforeUnmount } from 'vue'

/** Largest finger movement that still counts as a tap. */
export const TAP_SLOP_PX = 8

interface Press {
  pointerId: number
  el: Element
  x: number
  y: number
  top: number
  /** Moved beyond the slop, or something scrolled while pressed. */
  spoiled: boolean
}

function topOf(el: Element): number {
  return el.getBoundingClientRect().top
}

function far(dx: number, dy: number): boolean {
  return Math.hypot(dx, dy) >= TAP_SLOP_PX
}

export function useTapGuard() {
  let press: Press | null = null
  /** The element whose press ended as a tap; its next click is genuine. */
  let armed: Element | null = null

  function onScroll(): void {
    if (press) press.spoiled = true
  }

  function listen(on: boolean): void {
    if (typeof document === 'undefined') return
    if (on) document.addEventListener('scroll', onScroll, { capture: true, passive: true })
    else document.removeEventListener('scroll', onScroll, { capture: true })
  }

  function onPointerDown(e: PointerEvent): void {
    armed = null
    listen(false)
    press = null
    if (!e.isPrimary || (e.pointerType === 'mouse' && e.button !== 0)) return
    const el = e.currentTarget as Element
    press = { pointerId: e.pointerId, el, x: e.clientX, y: e.clientY, top: topOf(el), spoiled: false }
    listen(true)
  }

  function onPointerMove(e: PointerEvent): void {
    if (press && e.pointerId === press.pointerId && far(e.clientX - press.x, e.clientY - press.y)) press.spoiled = true
  }

  function onPointerUp(e: PointerEvent): void {
    const p = press
    if (!p || e.pointerId !== p.pointerId) return
    press = null
    listen(false)
    const moved = p.spoiled || far(e.clientX - p.x, e.clientY - p.y) || Math.abs(topOf(p.el) - p.top) >= TAP_SLOP_PX
    armed = moved ? null : p.el
  }

  function onPointerCancel(): void {
    press = null
    armed = null
    listen(false)
  }

  /** True when this click ends a genuine tap on its element (or comes from the keyboard). */
  function isTap(e: MouseEvent): boolean {
    const ok = e.detail === 0 || (armed !== null && armed === e.currentTarget)
    armed = null
    return ok
  }

  onBeforeUnmount(() => listen(false))

  return {
    handlers: {
      pointerdown: onPointerDown,
      pointermove: onPointerMove,
      pointerup: onPointerUp,
      pointercancel: onPointerCancel,
    },
    isTap,
  }
}
