// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { installScrollBoundaryGuard } from './scrollBoundary'

function geometry(el: HTMLElement, { height = 400, content = 800, top = 0 } = {}): void {
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: height })
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: content })
  el.scrollTop = top
}

function touch(el: Element, type: string, x: number, y: number, count = 1): Event {
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'touches', { value: Array.from({ length: count }, () => ({ clientX: x, clientY: y })) })
  el.dispatchEvent(event)
  return event
}

describe('legacy Safari scroll boundaries', () => {
  let root: HTMLDivElement
  let stop: () => void

  beforeEach(() => {
    vi.stubGlobal('CSS', { supports: () => false })
    root = document.createElement('div')
    root.className = 'jt-scroll'
    document.body.appendChild(root)
    geometry(root)
    stop = installScrollBoundaryGuard(document)
  })

  afterEach(() => {
    stop()
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  function drag(el: Element, delta: number): Event {
    touch(el, 'touchstart', 50, 100)
    return touch(el, 'touchmove', 50, 100 + delta)
  }

  it('blocks outward drags at each boundary but preserves ordinary scrolling', () => {
    expect(drag(root, 20).defaultPrevented).toBe(true)
    expect(drag(root, -20).defaultPrevented).toBe(false)
    root.scrollTop = 400
    expect(drag(root, -20).defaultPrevented).toBe(true)
    expect(drag(root, 20).defaultPrevented).toBe(false)
    root.scrollTop = 150
    expect(drag(root, -20).defaultPrevented).toBe(false)
    expect(drag(root, 20).defaultPrevented).toBe(false)
  })

  it('keeps a short page stationary in both directions', () => {
    geometry(root, { height: 400, content: 300 })
    expect(drag(root, 20).defaultPrevented).toBe(true)
    expect(drag(root, -20).defaultPrevented).toBe(true)
  })

  it('allows a focused textarea and its parent to consume a drag before blocking it', () => {
    const area = document.createElement('textarea')
    area.style.overflowY = 'auto'
    root.appendChild(area)
    geometry(area, { height: 100, content: 250, top: 50 })
    area.focus()
    expect(drag(area, 20).defaultPrevented).toBe(false)
    area.scrollTop = 0
    root.scrollTop = 20
    expect(drag(area, 20).defaultPrevented).toBe(false)
    root.scrollTop = 0
    expect(drag(area, 20).defaultPrevented).toBe(true)
    area.scrollTop = 150
    expect(drag(area, -20).defaultPrevented).toBe(false)
  })

  it('leaves horizontal and multi-touch gestures alone', () => {
    touch(root, 'touchstart', 50, 100)
    expect(touch(root, 'touchmove', 100, 110).defaultPrevented).toBe(false)
    touch(root, 'touchstart', 50, 100, 2)
    expect(touch(root, 'touchmove', 50, 130, 2).defaultPrevented).toBe(false)
    touch(root, 'touchstart', 50, 100)
    expect(touch(root, 'touchmove', 50, 130, 2).defaultPrevented).toBe(false)
  })

  it('lets a nested app scroll view pass a drag to its enclosing page', () => {
    const nested = document.createElement('div')
    nested.className = 'jt-scroll'
    nested.style.overflowY = 'auto'
    root.appendChild(nested)
    geometry(nested, { height: 100, content: 250 })
    root.scrollTop = 20
    expect(drag(nested, 20).defaultPrevented).toBe(false)
    root.scrollTop = 0
    expect(drag(nested, 20).defaultPrevented).toBe(true)
  })

  it.each(['maplibregl-map', 'jt-sheet'])('leaves %s gestures to their own handlers', (className) => {
    const region = document.createElement('div')
    region.className = className
    const target = document.createElement('button')
    region.appendChild(target)
    root.appendChild(region)
    expect(drag(target, 20).defaultPrevented).toBe(false)
  })

  it('does not intercept touches outside an app scroll view or after disposal', () => {
    const outside = document.createElement('div')
    document.body.appendChild(outside)
    expect(drag(outside, 20).defaultPrevented).toBe(false)
    touch(root, 'touchstart', 50, 100)
    stop()
    expect(touch(root, 'touchmove', 50, 130).defaultPrevented).toBe(false)
  })

  it('installs no touch interception when CSS can suppress overscroll', () => {
    stop()
    vi.stubGlobal('CSS', { supports: () => true })
    const add = vi.spyOn(document, 'addEventListener')
    stop = installScrollBoundaryGuard(document)
    expect(add).not.toHaveBeenCalled()
    expect(drag(root, 20).defaultPrevented).toBe(false)
  })
})
