// Order detail before accepting: the fare is shown, the commission never is
// (it is still charged on acceptance, §22.2), and a 402 insufficient_balance
// keeps the order on screen with a plain top-up request and button — never
// the amount the order needs, which is its commission. A route with a manual
// stop has no metrics: one chip says so instead of dashes. With a passenger's
// promotion (§23.6) the fare rows add the cash to collect and Jan Taxi's part.

import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import * as api from '../api/orders'
import { normalizeDetail } from '../api/orders'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'
import { ApiError } from '../services/http'
import OrderDetailPage from './OrderDetailPage.vue'

vi.mock('../api/orders', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/orders')>()
  return { ...real, getOrder: vi.fn(), acceptOrder: vi.fn() }
})
vi.mock('../services/realtime', () => ({ useRealtime: () => ({ on: () => () => undefined, onResync: () => () => undefined }) }))
vi.mock('../api/wallet', () => ({ getWallet: vi.fn().mockResolvedValue({ balance: 20, reserved: 0, available: 20, currency: 'KZT', min_online_balance: 0, updated_at: '' }) }))

/** Money is formatted with no-break spaces. */
const plain = (s: string) => s.replace(/\u00a0/g, ' ')

const ID = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
const mocked = vi.mocked(api)

function order(over: Record<string, unknown> = {}) {
  return normalizeDetail({
    id: ID,
    number: 1024,
    status: 'searching',
    is_mine: false,
    category: 'taxi',
    vehicle_class: 'minivan',
    vehicle_class_name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' },
    passenger_count: 6,
    cargo_size: null,
    cargo_volume_l: 0,
    fare: 3000,
    commission_amount: 50,
    commission_bps: 1000,
    payment_method: 'cash',
    distance_m: 6800,
    duration_s: 840,
    distance_to_pickup_m: 1200,
    stops: [],
    stop_count: 2,
    route: null,
    passenger: null,
    events: [],
    can_accept: true,
    locality: 'Алматы',
    tariff_region: { code: 'KAZ', name: { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } },
    ...over,
  })
}

/** Texts of the route chips under the stops. */
function chips(w: VueWrapper): string[] {
  return w.findAll('.odetail__chip').map((c) => plain(c.text()))
}

async function mountPage() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/orders/:id', name: ROUTE.order, component: OrderDetailPage },
      { path: '/wallet/topup', name: ROUTE.topup, component: { template: '<div />' } },
      { path: '/trip', name: ROUTE.trip, component: { template: '<div />' } },
      { path: '/orders', name: ROUTE.orders, component: { template: '<div />' } },
    ],
  })
  await router.push(`/orders/${ID}`)
  await router.isReady()
  const w = mount(OrderDetailPage, {
    global: {
      plugins: [i18n, router],
      stubs: {
        TripMap: true,
        JSheet: { template: '<section><slot name="header" /><slot /><footer><slot name="footer" /></footer></section>' },
      },
    },
  })
  await flushPromises()
  return { w, router }
}

describe('OrderDetailPage', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    i18n.global.locale.value = 'kk'
    mocked.getOrder.mockResolvedValue(order())
  })

  it('shows the requested class with the passenger count and the fare, never the commission', async () => {
    const { w } = await mountPage()
    expect(w.text()).toContain('Минивэн')
    expect(w.text()).toContain('6 жолаушы')
    expect(plain(w.text())).toContain('3 000 ₸')
    expect(w.text()).not.toMatch(/омисси|Таза табыс|10\s?%/)
    expect(plain(w.text())).not.toContain('50 ₸')
    expect(w.find('footer').text()).toContain('Тапсырысты алу')
  })

  it('accepting moves to the trip screen', async () => {
    mocked.acceptOrder.mockResolvedValue({ ...order(), status: 'accepted', is_mine: true, can_accept: false })
    const { w, router } = await mountPage()
    await w.findAll('footer button').at(-1)!.trigger('click')
    await flushPromises()
    expect(mocked.acceptOrder).toHaveBeenCalledWith(ID)
    expect(router.currentRoute.value.name).toBe(ROUTE.trip)
  })

  it('shows the locality of pickup A next to the route, not the tariff region', async () => {
    const { w } = await mountPage()
    const place = w.find('.odetail__locality')
    expect(place.exists()).toBe(true)
    expect(place.text()).toContain('Алматы')
    expect(w.text()).not.toContain('Қазақстан')
  })

  it('shows the trip distance and duration when the route is known', async () => {
    const { w } = await mountPage()
    expect(chips(w)).toEqual(['6,8 км', '14 мин', 'Сізден 1,2 км'])
  })

  it('a route without metrics shows one «Маршрут есептелмеген» chip, never dashes', async () => {
    mocked.getOrder.mockResolvedValue(order({ distance_m: null, duration_s: null }))
    const { w } = await mountPage()
    expect(chips(w)).toEqual(['Маршрут есептелмеген', 'Сізден 1,2 км'])
    expect(w.find('.odetail__chips').text()).not.toContain('—')
  })

  it('leaves out only the metric that is unknown', async () => {
    mocked.getOrder.mockResolvedValue(order({ duration_s: null }))
    const { w } = await mountPage()
    expect(chips(w)).toEqual(['6,8 км', 'Сізден 1,2 км'])
    mocked.getOrder.mockResolvedValue(order({ distance_m: null }))
    const { w: other } = await mountPage()
    expect(chips(other)).toEqual(['14 мин', 'Сізден 1,2 км'])
  })

  it("a passenger's promotion: the fare stays, with the cash to collect and Jan Taxi's part after the ride", async () => {
    const promotion = { kind: '', title: null, discount: 400, gross_fare: 1500, cash_due: 1100, status: 'reserved', compensation: null, settled_at: null }
    mocked.getOrder.mockResolvedValue(order({ fare: 1500, promo_discount: 400, cash_due: 1100, promotion }))
    const { w } = await mountPage()
    expect(plain(w.find('.odetail__title').text())).toContain('1 500 ₸')
    const text = plain(w.text())
    expect(text).toContain('Жолаушыдан алу')
    expect(text).toContain('1 100 ₸')
    expect(text).toContain('Jan Taxi акциясы')
    expect(text).toContain('+400 ₸')
    expect(text).toContain('Сапардан кейін балансқа түседі')
    expect(text).not.toMatch(/омисси/)
    expect(text).not.toContain('50 ₸')
    expect(w.find('footer').text()).toContain('Тапсырысты алу')
  })

  it("a past trip: a credited part says so, a cancelled order's promotion is not shown", async () => {
    const promotion = { kind: '', title: null, discount: 400, gross_fare: 1500, cash_due: 1100, compensation: 400, settled_at: '2026-11-02T10:20:00Z' }
    const past = { is_mine: true, can_accept: false, fare: 1500, promo_discount: 400, cash_due: 1100 }
    mocked.getOrder.mockResolvedValue(order({ ...past, status: 'completed', promotion: { ...promotion, status: 'settled' } }))
    const { w } = await mountPage()
    expect(plain(w.text())).toContain('Балансқа түсті')
    mocked.getOrder.mockResolvedValue(
      order({ ...past, status: 'cancelled', cancelled_by: 'passenger', promotion: { ...promotion, status: 'released', compensation: null, settled_at: null } }),
    )
    const { w: cancelled } = await mountPage()
    expect(cancelled.text()).not.toContain('Jan Taxi акциясы')
    expect(cancelled.text()).not.toContain('Жолаушыдан алу')
  })

  it('a 402 keeps the order open with a top-up request and button, never the amount the order needs', async () => {
    mocked.acceptOrder.mockRejectedValue(
      new ApiError(402, 'insufficient_balance', 'x', {}, { required: 50, available: 20, currency: 'KZT', topup_min_amount: 1000 }),
    )
    const { w, router } = await mountPage()
    const accept = w.findAll('footer button').at(-1)!
    await accept.trigger('click')
    await flushPromises()
    const footer = w.find('footer')
    expect(footer.find('[role="alert"]').text()).toBe('Тапсырысты алу үшін балансты толтырыңыз')
    expect(footer.text()).not.toMatch(/омисси/)
    expect(plain(footer.text())).not.toMatch(/\b(50|20|30) ₸/)
    const topUp = footer.findAll('button')[0]!
    expect(plain(topUp.text())).toContain('Толтыру · 1 000 ₸')
    await topUp.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe(ROUTE.topup)
    expect(router.currentRoute.value.query.amount).toBe('1000')
  })
})
