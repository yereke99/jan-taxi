// Order detail from "My orders" with a coupon or bonus (§23.6): a finished
// ride lists the trip fare, the discount and what the passenger paid; an
// order still running, what the passenger pays; a cancelled one only its
// fare (the discount stays on it as a snapshot) and that the coupon or bonus
// came back.

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { parseOrder } from '../api/parse'
import type { Order } from '../api/types'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'
import OrderDetailPage from './OrderDetailPage.vue'

/** The order the page shows (instead of GET /orders/:id with its realtime sync). */
const shown = vi.hoisted(() => ({ order: null as Order | null }))

vi.mock('../composables/useLiveOrder', async () => {
  const { ref, shallowRef } = await import('vue')
  return {
    useLiveOrder: () => ({ order: shallowRef(shown.order), loading: ref(false), error: shallowRef(null), refresh: () => undefined, accept: () => undefined }),
  }
})
// The static route map needs WebGL; a stand-in is enough for the facts.
vi.mock('../maps', () => ({
  MapView: { template: '<div class="map-stand-in" />' },
  useRouteLayer: () => ({ setRoute: () => undefined }),
  useStopMarkers: () => ({ setStops: () => undefined }),
}))

/** Money uses no-break spaces. */
const plain = (s: string) => s.replace(/ /g, ' ')

const ORDER_ID = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
const COUPON = { kind: 'coupon', title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }, discount: 400, gross_fare: 1500, cash_due: 1100 }

/** A 1 500 ₸ taxi ride, completed, that a 400 ₸ coupon paid part of. */
function order(over: Record<string, unknown> = {}): Order {
  return parseOrder({
    id: ORDER_ID,
    number: 1024,
    status: 'completed',
    category: 'taxi',
    vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
    passenger_count: 1,
    fare: 1500,
    promo_discount: 400,
    cash_due: 1100,
    promotion: { ...COUPON, status: 'settled', compensation: 400, settled_at: '2026-11-02T10:15:00Z' },
    payment_method: 'cash',
    distance_m: 3200,
    duration_s: 540,
    stops: [
      { id: 's1', seq: 0, kind: 'pickup', lat: 48.82901, lon: 58.138517, address: 'Абай көшесі, 12' },
      { id: 's2', seq: 1, kind: 'destination', lat: 48.824852, lon: 58.143715, address: 'Ембі базары' },
    ],
    route: {},
    events: [],
    version: 3,
    created_at: '2026-11-02T10:00:00Z',
    completed_at: '2026-11-02T10:15:00Z',
    ...over,
  })
}

const Blank = { template: '<div />' }

async function mountDetail(o: Order) {
  shown.order = o
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: ROUTE.home, component: Blank },
      { path: '/ride', name: ROUTE.book, component: Blank },
      { path: '/order/:id', name: ROUTE.order, component: Blank },
      { path: '/orders', name: ROUTE.orders, component: Blank },
      { path: '/orders/:id', name: ROUTE.orderDetail, component: Blank },
    ],
  })
  await router.push(`/orders/${o.id}`)
  await router.isReady()
  const w = mount(OrderDetailPage, { global: { plugins: [i18n, router] } })
  await nextTick()
  return w
}

/** The trip facts as [label, value] texts. */
function facts(w: ReturnType<typeof mount>): string[][] {
  return w.findAll('.detail-fact').map((f) => [f.find('.detail-fact__label').text(), plain(f.find('.detail-fact__value').text())])
}

beforeEach(() => setActivePinia(createPinia()))

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('order detail: coupon or bonus (§23.6)', () => {
  it('lists the trip fare, the coupon and what the passenger paid for a completed ride', async () => {
    const w = await mountDetail(order())
    const rows = facts(w)
    expect(rows).toContainEqual(['Сапар құны', '1 500 ₸'])
    expect(rows).toContainEqual(['Купон', '−400 ₸'])
    expect(rows).toContainEqual(['Төленді', '1 100 ₸'])
    expect(rows.map((r) => r[0])).not.toContain('Төлейсіз')
    expect(w.find('.detail-fact.is-total .detail-fact__label').text()).toBe('Төленді')
    w.unmount()

    // Russian: in the past tense, never "К оплате" for a ride already paid.
    i18n.global.locale.value = 'ru'
    const ru = await mountDetail(order({ promotion: { ...COUPON, kind: 'bonus', title: null, status: 'settled' } }))
    expect(facts(ru)).toContainEqual(['Бонус', '−400 ₸'])
    expect(ru.find('.detail-fact.is-total .detail-fact__label').text()).toBe('Оплачено')
    ru.unmount()
  })

  it('says paid for a disputed ride and what the passenger pays while the order runs', async () => {
    const disputed = await mountDetail(order({ status: 'disputed' }))
    expect(disputed.find('.detail-fact.is-total .detail-fact__label').text()).toBe('Төленді')
    disputed.unmount()

    const running = await mountDetail(order({ status: 'accepted', completed_at: null, promotion: { ...COUPON, status: 'reserved' } }))
    expect(facts(running)).toContainEqual(['Төлейсіз', '1 100 ₸'])
    running.unmount()
  })

  it('shows a cancelled order at its fare, without the discount rows, and says the coupon came back', async () => {
    const w = await mountDetail(
      order({ status: 'cancelled', completed_at: null, cancelled_at: '2026-11-02T10:05:00Z', cancelled_by: 'passenger', promotion: { ...COUPON, status: 'released' } }),
    )
    const rows = facts(w)
    expect(rows).toContainEqual(['Баға', '1 500 ₸'])
    expect(rows.map((r) => r[0])).not.toContain('Купон')
    expect(rows.flat()).not.toContain('1 100 ₸')
    expect(w.find('.detail-fact.is-total').exists()).toBe(false)
    expect(w.find('.detail-text').text()).toContain('Купон қайтарылды: мерзімі біткенше оны қайта пайдалана аласыз.')
    w.unmount()

    const bonus = await mountDetail(
      order({ status: 'cancelled', completed_at: null, cancelled_by: 'passenger', promotion: { ...COUPON, kind: 'bonus', title: null, status: 'released' } }),
    )
    expect(bonus.find('.detail-text').text()).toContain('Бонус балансыңызға қайтарылды.')
    bonus.unmount()
  })

  it('shows the fare alone for an order without a promotion', async () => {
    const w = await mountDetail(order({ promo_discount: 0, cash_due: 1500, promotion: null }))
    expect(facts(w)).toContainEqual(['Баға', '1 500 ₸'])
    expect(w.find('.detail-fact.is-total').exists()).toBe(false)
    w.unmount()
  })
})
