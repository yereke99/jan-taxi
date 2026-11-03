// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import JNavBar from './JNavBar.vue'

describe('JNavBar title collapse', () => {
  let barHeight = 137 // 93px Telegram safe area + 44px compact row.
  let intersect: IntersectionObserverCallback
  let resize: ResizeObserverCallback
  let options: IntersectionObserverInit
  const disconnect = vi.fn()
  const stopResize = vi.fn()

  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ height: barHeight }) as DOMRect)
    vi.stubGlobal('IntersectionObserver', class {
      constructor(callback: IntersectionObserverCallback, init: IntersectionObserverInit) {
        intersect = callback
        options = init
      }
      observe = vi.fn()
      disconnect = disconnect
    })
    vi.stubGlobal('ResizeObserver', class {
      constructor(callback: ResizeObserverCallback) { resize = callback }
      observe = vi.fn()
      disconnect = stopResize
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
    barHeight = 137
  })

  it('collapses against the Telegram safe area and remeasures it after rotation', async () => {
    const scroll = document.createElement('div')
    scroll.className = 'jt-scroll'
    document.body.appendChild(scroll)
    const wrapper = mount(JNavBar, { props: { title: 'Профиль' }, attachTo: scroll })
    expect(options.root).toBe(scroll)
    expect(options.rootMargin).toBe('-93px 0px 0px 0px')
    expect(wrapper.get('.jt-navbar__bar').classes()).toContain('is-overlay')
    expect(wrapper.get('.jt-navbar__title').classes()).not.toContain('is-visible')

    intersect([{ isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('.jt-navbar__title').classes()).toContain('is-visible')

    barHeight = 100
    resize([], {} as ResizeObserver)
    expect(options.rootMargin).toBe('-56px 0px 0px 0px')
    wrapper.unmount()
    expect(disconnect).toHaveBeenCalled()
    expect(stopResize).toHaveBeenCalled()
  })

  it('keeps the full navigation row when it contains page controls', () => {
    const wrapper = mount(JNavBar, { props: { title: 'Orders' }, slots: { trailing: '<button>Filter</button>' } })
    expect(wrapper.get('.jt-navbar__bar').classes()).not.toContain('is-overlay')
    expect(options.rootMargin).toBe('-137px 0px 0px 0px')
    wrapper.unmount()
  })

  it('keeps the compact title visible on pages without a large title', () => {
    const wrapper = mount(JNavBar, { props: { title: 'Details', large: false } })
    expect(wrapper.get('.jt-navbar__bar').classes()).not.toContain('is-overlay')
    expect(wrapper.get('.jt-navbar__title').classes()).toContain('is-visible')
    expect(wrapper.find('.jt-navbar__large').exists()).toBe(false)
    wrapper.unmount()
  })
})
