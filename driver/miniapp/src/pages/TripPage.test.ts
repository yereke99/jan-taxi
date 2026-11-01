// Trip screen header: the address of the stop the driver heads to. The
// passenger's own words (a typed pickup A, a manual stop) are shown exactly as
// written and marked «Жолаушы көрсеткен орын»; any other address as before.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import * as api from '../api/orders'
import { normalizeDetail } from '../api/orders'
import type { DriverOrderDetail } from '../api/types'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'
import { useLocationStore } from '../stores/location'
import { useTripStore } from '../stores/trip'
import TripPage from './TripPage.vue'

vi.mock('../api/orders', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/orders')>()
  return { ...real, getActive: vi.fn() }
})

const LABEL = 'Жолаушы көрсеткен орын'
const mocked = vi.mocked(api)

function trip(status: string, stops: Array<Record<string, unknown>>): DriverOrderDetail {
  return normalizeDetail({
    id: '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34',
    number: 1031,
    status,
    is_mine: true,
    category: 'taxi',
    vehicle_class: 'sedan',
    passenger_count: 1,
    fare: 700,
    currency: 'KZT',
    payment_method: 'cash',
    stops: stops.map((s, i) => ({
      id: `s${i}`,
      seq: i,
      kind: i === 0 ? 'pickup' : 'destination',
      mode: 'geocoded',
      lat: 48.829 + i / 1000,
      lon: 58.1385,
      address: `Address ${i}`,
      address_typed: false,
      address_details: null,
      completed_at: null,
      ...s,
    })),
    stop_count: stops.length,
    route: null,
    passenger: { first_name: 'Айгерім', phone: null, phone_verified: false, telegram_url: 'tg://user?id=1' },
    events: [],
    can_arrive: status === 'accepted',
    can_start: false,
    can_complete: false,
  })
}

async function mountTrip(o: DriverOrderDetail) {
  useTripStore().apply(o)
  mocked.getActive.mockResolvedValue(o)
  // A fresh fix: the screen does not ask the device for one.
  useLocationStore().lastFix = { lat: 48.8264, lon: 58.1442, accuracy_m: 5, heading: null, speed_mps: null, timestamp: Date.now() }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/trip', name: ROUTE.trip, component: TripPage },
      { path: '/', name: ROUTE.home, component: { template: '<div />' } },
      { path: '/orders', name: ROUTE.orders, component: { template: '<div />' } },
      { path: '/support', name: ROUTE.support, component: { template: '<div />' } },
    ],
  })
  await router.push('/trip')
  await router.isReady()
  const w = mount(TripPage, {
    global: {
      plugins: [i18n, router],
      stubs: {
        TripMap: true,
        JSheet: { template: '<section><slot name="header" /><slot /><footer><slot name="footer" /></footer></section>' },
      },
    },
  })
  await flushPromises()
  return w
}

describe('TripPage header', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    i18n.global.locale.value = 'kk'
  })

  it("a typed pickup A: the passenger's words with the label above the details", async () => {
    const w = await mountTrip(
      trip('accepted', [{ address: 'Мешіттің жанында, көк қақпа', address_typed: true, address_details: '2-кіреберіс' }, { address: 'Ембі базары' }]),
    )
    expect(w.find('.tpage__target').text()).toBe('Мешіттің жанында, көк қақпа')
    expect(w.findAll('.tpage__details').map((p) => p.text())).toEqual([LABEL, '2-кіреберіс'])
    w.unmount()
  })

  it('heading to a manual stop: its text with the label, and the route is not calculated', async () => {
    const w = await mountTrip(
      trip('in_progress', [
        { address: 'Абай көшесі, 12', completed_at: '2026-11-01T10:00:00Z' },
        { mode: 'manual', lat: null, lon: null, address: 'Әженің үйі' },
      ]),
    )
    expect(w.find('.tpage__target').text()).toBe('Әженің үйі')
    expect(w.find('.tpage__head').text()).toContain(LABEL)
    // No map point to hand to 2GIS, but the note stays.
    expect(w.find('.nav__note').text()).toBe('Маршрут есептелмеген')
    expect(w.find('.nav__main').exists()).toBe(false)
    w.unmount()
  })

  it('a map address: no label', async () => {
    const w = await mountTrip(trip('accepted', [{ address: 'Абай көшесі, 12' }, { address: 'Ембі базары' }]))
    expect(w.find('.tpage__target').text()).toBe('Абай көшесі, 12')
    expect(w.find('.tpage__head').text()).not.toContain(LABEL)
    w.unmount()
  })
})
