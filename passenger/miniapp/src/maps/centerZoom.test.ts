// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { mount } from '@vue/test-utils'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { describe, expect, it } from 'vitest'
import { shallowRef } from 'vue'
import CenterPin from './CenterPin.vue'
import { zoomAroundCenter } from './centerZoom'
import { MAP_KEY } from './context'

/** A zoom handler like MapLibre's: enable() keeps the anchor of a handler that is already on. */
function handler(enabled: boolean) {
  const h = {
    enabled,
    around: null as string | null,
    isEnabled: () => h.enabled,
    enable(options?: { around: 'center' }) {
      if (h.enabled) return
      h.enabled = true
      h.around = options?.around ?? null
    },
    disable() {
      h.enabled = false
    },
  }
  return h
}

function fakeMap(interactive = true) {
  return {
    touchZoomRotate: handler(interactive),
    scrollZoom: handler(interactive),
    doubleClickZoom: handler(interactive),
    on: () => undefined,
    off: () => undefined,
  }
}

describe('zoomAroundCenter', () => {
  it('zooms pinch and scroll around the centre, turns double-tap zoom off and restores the defaults', () => {
    const m = fakeMap()
    const restore = zoomAroundCenter(m)
    expect(m.touchZoomRotate).toMatchObject({ enabled: true, around: 'center' })
    expect(m.scrollZoom).toMatchObject({ enabled: true, around: 'center' })
    expect(m.doubleClickZoom.enabled).toBe(false)

    restore()
    expect(m.touchZoomRotate).toMatchObject({ enabled: true, around: null })
    expect(m.scrollZoom).toMatchObject({ enabled: true, around: null })
    expect(m.doubleClickZoom.enabled).toBe(true)
  })

  it('leaves the handlers of a non-interactive map off', () => {
    const m = fakeMap(false)
    zoomAroundCenter(m)()
    expect([m.touchZoomRotate.enabled, m.scrollZoom.enabled, m.doubleClickZoom.enabled]).toEqual([false, false, false])
  })
})

describe('CenterPin', () => {
  it('keeps the point under the pin while zooming and gives the defaults back when it goes', () => {
    const m = fakeMap()
    const w = mount(CenterPin, { global: { provide: { [MAP_KEY]: shallowRef(m as unknown as MapLibreMap) } } })
    expect(m.scrollZoom.around).toBe('center')
    expect(m.touchZoomRotate.around).toBe('center')
    expect(m.doubleClickZoom.enabled).toBe(false)

    w.unmount()
    expect(m.scrollZoom.around).toBeNull()
    expect(m.touchZoomRotate.around).toBeNull()
    expect(m.doubleClickZoom.enabled).toBe(true)
  })
})
