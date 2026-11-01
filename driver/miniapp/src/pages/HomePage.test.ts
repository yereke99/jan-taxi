// The active-trip banner on the home screen names the stop the driver heads
// to: the passenger's own words exactly as written (a typed pickup A, a
// manual stop), any other address through the "Point on the map" filter.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import * as api from '../api/orders'
import { normalizeDetail } from '../api/orders'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'
import { useTripStore } from '../stores/trip'
import HomePage from './HomePage.vue'

vi.mock('../api/orders', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/orders')>()
  return { ...real, getActive: vi.fn() }
})
vi.mock('../api/wallet', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/wallet')>()
  return { ...real, getWallet: vi.fn().mockRejectedValue(new Error('offline')) }
})
vi.mock('../api/driver', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/driver')>()
  return { ...real, getProfile: vi.fn().mockRejectedValue(new Error('offline')) }
})

const mocked = vi.mocked(api)
const COORDS = '48.826400, 58.144200'

function active(pickup: Record<string, unknown>) {
  return normalizeDetail({
    id: '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34',
    number: 1031,
    status: 'accepted',
    is_mine: true,
    category: 'taxi',
    fare: 700,
    stops: [
      { id: 's0', seq: 0, kind: 'pickup', mode: 'geocoded', lat: 48.8264, lon: 58.1442, address_typed: false, address_details: null, completed_at: null, ...pickup },
      { id: 's1', seq: 1, kind: 'destination', mode: 'manual', lat: null, lon: null, address: 'Әженің үйі', address_typed: false, address_details: null, completed_at: null },
    ],
    stop_count: 2,
  })
}

async function bannerText(pickup: Record<string, unknown>): Promise<string> {
  const o = active(pickup)
  useTripStore().apply(o)
  mocked.getActive.mockResolvedValue(o)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: Object.values(ROUTE).map((name) => ({ path: name === ROUTE.home ? '/' : `/${name}`, name, component: { template: '<div />' } })),
  })
  await router.push('/')
  await router.isReady()
  const w = mount(HomePage, { global: { plugins: [i18n, router], stubs: { OnlineCard: true, HomeMap: true, PrivateImage: true } } })
  await flushPromises()
  const text = w.find('.trip-banner__sub').text()
  w.unmount()
  return text
}

beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'kk'
})

describe('HomePage active trip banner', () => {
  it("shows a typed pickup A in the passenger's words", async () => {
    expect(await bannerText({ address: 'Мешіттің жанында, көк қақпа', address_typed: true })).toBe('A · Мешіттің жанында, көк қақпа')
    expect(await bannerText({ address: COORDS, address_typed: true })).toBe(`A · ${COORDS}`)
  })

  it('shows a map point without a name as a point on the map', async () => {
    expect(await bannerText({ address: COORDS })).toBe('A · Картадағы нүкте')
  })
})
