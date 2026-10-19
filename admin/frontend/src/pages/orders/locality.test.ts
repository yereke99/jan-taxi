// Orders show where they happen (the settlement of pickup A) apart from the
// region whose tariff priced them: an Almaty order priced by the nationwide
// region reads «Алматы» and «Тариф: Қазақстан», never the nearest city.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { localityText } from '../../api/common'
import { citiesApi } from '../../api/config'
import { orderQuery, orderTariffRegion, ordersApi, type AdminOrderListItem } from '../../api/orders'
import { setCities } from '../../composables/useCities'
import { setVehicleClasses } from '../../composables/useVehicleClasses'
import { i18n } from '../../i18n'
import OrdersPage from './OrdersPage.vue'

const KAZ = { code: 'KAZ', name: { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } }
const EMB = { id: 'c1', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Embi' } }

function row(extra: Partial<AdminOrderListItem> = {}): AdminOrderListItem {
  return {
    id: 'o1034',
    number: 1034,
    status: 'searching',
    category: 'taxi',
    service_type: 'passenger',
    city: { id: 'c0', ...KAZ },
    locality: 'Алматы',
    tariff_region: KAZ,
    fare: 700,
    currency: 'KZT',
    commission_amount: 50,
    payment_method: 'cash',
    passenger: { id: 'p1', telegram_user_id: 1, first_name: 'Айгерім', last_name: '', username: null, phone: null, is_blocked: false },
    driver: null,
    pickup: { seq: 0, kind: 'pickup', lat: 43.2389, lon: 76.8897, address: 'Абай даңғылы, 10' },
    destination: { seq: 1, kind: 'destination', lat: 43.25, lon: 76.9, address: 'Достық даңғылы, 5' },
    stop_count: 2,
    distance_m: 3000,
    has_open_dispute: false,
    created_at: '2026-10-19T08:00:00Z',
    accepted_at: null,
    completed_at: null,
    cancelled_at: null,
    cancelled_by: null,
    cancel_reason_code: null,
    vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
    passenger_count: 1,
    cargo_size: null,
    cargo_volume_l: 0,
    ...extra,
  }
}

describe('order place helpers', () => {
  it('keeps a real settlement name and drops coordinates', () => {
    expect(localityText('  Алматы ')).toBe('Алматы')
    expect(localityText('43.238900, 76.889700')).toBeNull()
    expect(localityText('')).toBeNull()
    expect(localityText(null)).toBeNull()
  })

  it('reads the tariff region, falling back to the tariff city of older payloads', () => {
    expect(orderTariffRegion(row())).toEqual(KAZ)
    expect(orderTariffRegion(row({ tariff_region: null, city: EMB }))).toEqual({ code: 'EMB', name: EMB.name })
  })

  it('searches the locality through the list search', () => {
    expect(orderQuery({ q: ' Алматы ' }).q).toBe('Алматы')
    expect(orderQuery({ q: '' }).q).toBeUndefined()
  })
})

describe('OrdersPage', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setVehicleClasses([])
    setCities([])
    vi.spyOn(citiesApi, 'list').mockResolvedValue({ items: [], page: 1, page_size: 100, total: 0 })
    i18n.global.locale.value = 'kk'
  })

  afterEach(() => {
    i18n.global.locale.value = 'kk'
  })

  async function mountPage(path = '/orders') {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/orders', name: 'orders', component: OrdersPage },
        { path: '/orders/:id', name: 'order-detail', component: { template: '<div />' } },
      ],
    })
    await router.push(path)
    await router.isReady()
    const w = mount({ template: '<RouterView />' }, { global: { plugins: [i18n, router] } })
    await flushPromises()
    return w
  }

  it('has a locality column with the tariff region below it', async () => {
    vi.spyOn(ordersApi, 'list').mockResolvedValue({ items: [row(), row({ id: 'o2', number: 1035, locality: null, tariff_region: { code: 'EMB', name: EMB.name } })], page: 1, page_size: 20, total: 2 })
    const w = await mountPage()
    const headers = w.findAll('th').map((h) => h.text())
    expect(headers).toContain('Елді мекен')
    const rows = w.findAll('tbody tr')
    expect(rows[0]!.text()).toContain('Алматы')
    expect(rows[0]!.text()).toContain('Тариф: Қазақстан')
    expect(rows[0]!.text()).not.toContain('Ембі')
    expect(rows[1]!.text()).toContain('Елді мекен белгісіз')
    expect(rows[1]!.text()).toContain('Тариф: Ембі')
  })

  it('the search accepts a locality', async () => {
    const list = vi.spyOn(ordersApi, 'list').mockResolvedValue({ items: [], page: 1, page_size: 20, total: 0 })
    const w = await mountPage('/orders?q=Алматы')
    expect(list.mock.calls[0]![0]).toMatchObject({ q: 'Алматы' })
    expect(w.find('input[type="search"]').attributes('placeholder')).toContain('елді мекен')
  })
})
