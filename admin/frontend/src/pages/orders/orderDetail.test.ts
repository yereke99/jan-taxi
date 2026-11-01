// Order detail: the commission row names the village whose commission
// applied, and stops the passenger wrote (manual stops and a typed pickup A)
// carry the "written by the passenger" label.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ordersApi, type AdminOrder, type Stop } from '../../api/orders'
import { setVehicleClasses } from '../../composables/useVehicleClasses'
import { i18n } from '../../i18n'
import OrderDetailPage from './OrderDetailPage.vue'

vi.mock('./OrderRouteMap.vue', () => ({ default: { name: 'OrderRouteMap', props: ['stops', 'route', 'driverLocation'], template: '<div class="map-stub" />' } }))

const KAZ = { code: 'KAZ', name: { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } }

function stop(seq: number, extra: Partial<Stop>): Stop {
  return {
    id: `s${seq}`,
    seq,
    kind: seq === 0 ? 'pickup' : 'destination',
    address: `Нүкте ${seq}`,
    mode: 'geocoded',
    address_typed: false,
    lat: 49.14 + seq / 100,
    lon: 56.48,
    address_details: null,
    completed_at: null,
    ...extra,
  }
}

function order(extra: Partial<AdminOrder> = {}): AdminOrder {
  return {
    id: 'o1',
    number: 1201,
    status: 'searching',
    category: 'taxi',
    service_type: 'passenger',
    cargo_type: 'none',
    cargo_description: null,
    recipient_phone: null,
    prohibited_items_ack: false,
    comment: null,
    fare: 1000,
    currency: 'KZT',
    commission_bps: 700,
    commission_amount: 70,
    commission_mode: 'percent',
    commission_area: { id: 'a1', name: 'Шұбарқұдық' },
    payment_method: 'cash',
    tariff_id: 't1',
    quote_id: null,
    distance_m: null,
    duration_s: null,
    stop_count: 3,
    stops: [
      stop(0, { address: 'Мешіттің жанында, көк қақпа', address_typed: true }),
      stop(1, { kind: 'intermediate', address: 'Мектеп' }),
      stop(2, { address: 'Ескі диірмен', mode: 'manual', lat: null, lon: null }),
    ],
    route: { geometry: null, legs: null, bbox: null, distance_m: null, duration_s: null },
    passenger: { id: 'p1', telegram_user_id: 1, first_name: 'Айгерім', last_name: '', username: null, phone: null, is_blocked: false },
    driver: null,
    vehicle: null,
    driver_location: null,
    offers: [],
    events: [],
    reservation: null,
    ledger: [],
    dispute: null,
    rating: null,
    cancel_reason: null,
    cancel_reason_code: null,
    cancelled_by: null,
    driver_eta_s: null,
    driver_eta_at: null,
    version: 1,
    created_at: '2026-11-01T08:00:00Z',
    updated_at: '2026-11-01T08:00:00Z',
    search_expires_at: null,
    accepted_at: null,
    arrived_at: null,
    started_at: null,
    completed_at: null,
    cancelled_at: null,
    locality: 'Шұбарқұдық',
    tariff_region: KAZ,
    city: { id: 'c0', ...KAZ },
    vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
    passenger_count: 1,
    cargo_size: null,
    cargo_volume_l: 0,
    ...extra,
  }
}

async function mountDetail(o: AdminOrder) {
  vi.spyOn(ordersApi, 'get').mockResolvedValue(o)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/orders', name: 'orders', component: { template: '<div />' } },
      { path: '/orders/:id', name: 'order-detail', component: OrderDetailPage, props: true },
      { path: '/drivers/:id', name: 'driver-detail', component: { template: '<div />' } },
    ],
  })
  await router.push('/orders/o1')
  await router.isReady()
  const w = mount({ template: '<RouterView />' }, { global: { plugins: [i18n, router] } })
  await flushPromises()
  return w
}

function commissionValue(w: Awaited<ReturnType<typeof mountDetail>>): string {
  const labels = w.findAll('dt')
  const i = labels.findIndex((d) => d.text() === 'Комиссия')
  return w.findAll('dd')[i]!.text()
}

beforeEach(() => {
  setActivePinia(createPinia())
  setVehicleClasses([])
  i18n.global.locale.value = 'ru'
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('OrderDetailPage', () => {
  it('names the village whose commission applied', async () => {
    const w = await mountDetail(order())
    expect(commissionValue(w)).toBe('70\u00a0₸ · 7% · Шұбарқұдық')
  })

  it('keeps the name of a deleted village and shows nothing for the general commission', async () => {
    let w = await mountDetail(order({ commission_area: { id: null, name: 'Жаңажол' } }))
    expect(commissionValue(w)).toBe('70\u00a0₸ · 7% · Жаңажол (село удалено)')
    w.unmount()
    w = await mountDetail(order({ commission_area: null, commission_mode: 'fixed', commission_bps: 0, commission_amount: 50 }))
    expect(commissionValue(w)).toBe('50\u00a0₸ · фиксированная')
    w.unmount()
    const older = order()
    delete older.commission_area
    w = await mountDetail(older)
    expect(commissionValue(w)).toBe('70\u00a0₸ · 7%')
  })

  it('marks the typed pickup and the manual stop as written by the passenger', async () => {
    const w = await mountDetail(order())
    const items = w.findAll('.stops__item')
    expect(items).toHaveLength(3)
    expect(items[0]!.text()).toContain('Мешіттің жанында, көк қақпа')
    expect(items[0]!.text()).toContain('Место указано пассажиром')
    expect(items[1]!.text()).not.toContain('Место указано пассажиром')
    expect(items[2]!.text()).toContain('Место указано пассажиром')
  })
})
