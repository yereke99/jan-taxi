// Home mini-map without GPS: no invented position (default map centre, no
// car, "not determined yet"), an old fix is shown as the last known one, and
// the live location stream is held only while the map is mounted.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { i18n } from '../../i18n'
import { useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { LocationError } from '../../telegram'
import HomeMap from './HomeMap.vue'

vi.mock('../../telegram', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../telegram')>()
  return { ...real, getLocation: vi.fn(), watchLocation: vi.fn(() => () => undefined) }
})
vi.mock('../../api/driver', () => ({ reportLocation: vi.fn().mockResolvedValue(undefined) }))

const telegram = await import('../../telegram')

/** Records the props MapView was created with. */
const created: Array<{ center: unknown; zoom: unknown }> = []
const MapViewStub = defineComponent({
  name: 'MapView',
  props: ['styleUrl', 'center', 'zoom'],
  setup(props, { slots }) {
    created.push({ center: props.center, zoom: props.zoom })
    return () => h('div', { class: 'jt-map' }, slots.default?.())
  },
})

let pinia: Pinia

function mountMap() {
  return mount(HomeMap, { global: { plugins: [pinia, i18n], stubs: { MapView: MapViewStub } } })
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  i18n.global.locale.value = 'kk'
  created.length = 0
  vi.mocked(telegram.getLocation).mockReset()
})

describe('HomeMap', () => {
  it('without GPS shows the default centre and says the position is unknown', async () => {
    vi.mocked(telegram.getLocation).mockRejectedValue(new LocationError('denied'))
    const w = mountMap()
    await flushPromises()
    const session = useSessionStore()
    expect(created[0]).toEqual({ center: session.config.map.default_center, zoom: session.config.map.default_zoom })
    expect(w.find('.home-map__overlay').text()).toBe('Орныңыз әлі анықталмады')
    expect(w.find('.jt-car-marker').exists()).toBe(false)
    expect(telegram.watchLocation).not.toHaveBeenCalled()
  })

  it('opens at the last known position and labels it when it is old', async () => {
    vi.mocked(telegram.getLocation).mockRejectedValue(new LocationError('unavailable'))
    const fix = { lat: 48.82, lon: 58.14, accuracy_m: 10, heading: null, speed_mps: null, timestamp: Date.now() - 5 * 60_000 }
    useLocationStore().lastFix = fix
    const w = mountMap()
    await flushPromises()
    expect(created[0]).toMatchObject({ center: fix, zoom: 16 })
    expect(w.find('.home-map__overlay').text()).toBe('Соңғы белгілі орныңыз')
  })

  it('holds the live stream while mounted and releases it on unmount', async () => {
    vi.mocked(telegram.getLocation).mockResolvedValue({ lat: 48.82, lon: 58.14, accuracy_m: 5, heading: null, speed_mps: null, timestamp: Date.now(), source: 'browser' })
    const stop = vi.fn()
    vi.mocked(telegram.watchLocation).mockReturnValue(stop)
    const w = mountMap()
    await flushPromises()
    const location = useLocationStore()
    expect(location.live).toBe(true)
    expect(w.find('.home-map__overlay').exists()).toBe(false)
    w.unmount()
    expect(stop).toHaveBeenCalledTimes(1)
    expect(location.live).toBe(false)
  })
})
