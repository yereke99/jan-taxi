// Trip history rows read pickup A → destination: the passenger's own words
// exactly as written (a typed pickup A, a manual destination), any other
// address through the "Point on the map" filter. A completed trip with a
// passenger's promotion (§23.6) carries a small mark with Jan Taxi's part
// next to its fare; the totals stay on fares.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import * as api from '../api/orders'
import { normalizeSummary } from '../api/orders'
import { i18n } from '../i18n'
import { ROUTE } from '../router/names'
import HistoryPage from './HistoryPage.vue'

vi.mock('../api/orders', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/orders')>()
  return { ...real, listHistory: vi.fn() }
})

const mocked = vi.mocked(api)
const COORDS = '48.826400, 58.144200'

/** Money is formatted with no-break spaces. */
const plain = (s: string) => s.replace(/\u00a0/g, ' ')

function row(id: string, pickup: Record<string, unknown>, destination: Record<string, unknown>, over: Record<string, unknown> = {}) {
  return normalizeSummary({
    id,
    number: 1031,
    status: 'completed',
    category: 'taxi',
    fare: 700,
    currency: 'KZT',
    commission_amount: 50,
    commission_charged: 50,
    payment_method: 'cash',
    pickup: { seq: 0, kind: 'pickup', mode: 'geocoded', lat: 48.8264, lon: 58.1442, address_typed: false, ...pickup },
    destination: { seq: 1, kind: 'destination', mode: 'geocoded', lat: 48.8319, lon: 58.1585, address_typed: false, ...destination },
    stop_count: 2,
    distance_m: null,
    created_at: '2026-11-01T09:00:00Z',
    completed_at: '2026-11-01T09:20:00Z',
    cancelled_at: null,
    cancelled_by: null,
    ...over,
  })
}

async function mountHistory() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/history', name: ROUTE.history, component: HistoryPage }] })
  await router.push('/history')
  await router.isReady()
  const w = mount(HistoryPage, { global: { plugins: [i18n, router] } })
  await flushPromises()
  return w
}

beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'kk'
})

describe('HistoryPage', () => {
  it("shows the passenger's words verbatim and other unnamed points as a point on the map", async () => {
    mocked.listHistory.mockResolvedValue({
      items: [
        row('o1', { address: COORDS, address_typed: true }, { mode: 'manual', lat: null, lon: null, address: 'Әженің үйі' }),
        row('o2', { address: COORDS }, { address: 'Ембі базары' }),
      ],
      next_cursor: null,
    })
    const w = await mountHistory()
    expect(w.findAll('.hist__route').map((p) => p.text())).toEqual([`${COORDS} → Әженің үйі`, 'Картадағы нүкте → Ембі базары'])
  })

  it("marks Jan Taxi's part next to the fare of a completed trip with a promotion; the totals stay on fares", async () => {
    const place = { address: 'Абай көшесі, 12' }
    const to = { address: 'Ембі базары' }
    mocked.listHistory.mockResolvedValue({
      items: [
        row('o1', place, to, { fare: 1500, promo_discount: 400, cash_due: 1100 }),
        row('o2', place, to),
        // A cancelled order's promotion credited nothing.
        row('o3', place, to, { status: 'cancelled', fare: 1000, promo_discount: 400, cash_due: 600, completed_at: null, cancelled_at: '2026-11-01T09:10:00Z', cancelled_by: 'passenger' }),
      ],
      next_cursor: null,
    })
    const w = await mountHistory()
    const items = w.findAll('.hist__item')
    expect(plain(items[0]!.find('.hist__promo').text())).toBe('+400 ₸ акция')
    expect(plain(items[0]!.find('.hist__money').text())).toBe('1 500 ₸')
    expect(items[1]!.find('.hist__promo').exists()).toBe(false)
    expect(items[2]!.find('.hist__promo').exists()).toBe(false)
    expect(plain(w.find('.hist__totals-value').text())).toBe('2 200 ₸')
    expect(w.text()).not.toMatch(/омисси/)
  })

  it('names the mark in the chosen language', async () => {
    i18n.global.locale.value = 'en'
    mocked.listHistory.mockResolvedValue({
      items: [row('o1', { address: 'Абай көшесі, 12' }, { address: 'Ембі базары' }, { fare: 1500, promo_discount: 400, cash_due: 1100 })],
      next_cursor: null,
    })
    const w = await mountHistory()
    expect(plain(w.find('.hist__promo').text())).toBe('+400 ₸ promotion')
  })
})
