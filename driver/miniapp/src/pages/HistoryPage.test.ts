// Trip history rows read pickup A → destination: the passenger's own words
// exactly as written (a typed pickup A, a manual destination), any other
// address through the "Point on the map" filter.

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

function row(id: string, pickup: Record<string, unknown>, destination: Record<string, unknown>) {
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
  })
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
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/history', name: ROUTE.history, component: HistoryPage }] })
    await router.push('/history')
    await router.isReady()
    const w = mount(HistoryPage, { global: { plugins: [i18n, router] } })
    await flushPromises()
    expect(w.findAll('.hist__route').map((p) => p.text())).toEqual([`${COORDS} → Әженің үйі`, 'Картадағы нүкте → Ембі базары'])
  })
})
