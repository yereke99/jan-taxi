import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { driversApi, type AdminDriverListItem } from '../../api/drivers'
import { ordersApi, type AdminOrder } from '../../api/orders'
import { setVehicleClasses } from '../../composables/useVehicleClasses'
import { i18n } from '../../i18n'
import { distanceMeters } from '../../utils/geo'
import AssignDriverDialog from './AssignDriverDialog.vue'

const city = { id: 'c1', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Embi' } }

function driver(id: string, code: string, online: boolean): AdminDriverListItem {
  return {
    id,
    code,
    status: 'approved',
    first_name: 'Тест',
    last_name: code,
    phone: null,
    phone_verified: true,
    city,
    is_online: online,
    last_heartbeat_at: null,
    vehicle: null,
    rating: null,
    rating_count: 0,
    completed_trips: 0,
    wallet_available: 1000,
    submitted_at: null,
    approved_at: null,
    created_at: '2026-10-17T10:00:00Z',
  }
}

const order = {
  id: 'o1',
  number: 1015,
  status: 'searching',
  city,
  driver: null,
  commission_amount: 50,
  vehicle_class: 'minivan',
  vehicle_class_name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' },
  passenger_count: 7,
  cargo_size: null,
  cargo_volume_l: 0,
  ledger: [],
  stops: [{ id: 's1', seq: 0, kind: 'pickup', lat: 43.2389, lon: 76.8897, address: 'A', address_details: null, completed_at: null }],
} as unknown as AdminOrder

const location = (lat: number, lon: number) => ({
  lat,
  lon,
  accuracy_m: null,
  heading: null,
  speed_mps: null,
  source: 'miniapp',
  recorded_at: new Date().toISOString(),
  received_at: new Date().toISOString(),
  age_s: 5,
})

beforeEach(() => {
  i18n.global.locale.value = 'en'
  setActivePinia(createPinia())
  setVehicleClasses([])
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('AssignDriverDialog', () => {
  it('searches drivers in every city, nearest to pickup A first, and shows the API warnings after assigning', async () => {
    const list = vi.spyOn(driversApi, 'list').mockResolvedValue({ items: [driver('far', 'EMB001', true), driver('near', 'ALA001', true)], page: 1, page_size: 20, total: 2 })
    vi.spyOn(driversApi, 'locations').mockResolvedValue({
      items: [
        { driver: { id: 'far', code: 'EMB001', first_name: '', last_name: '', phone: null }, status: 'approved', is_online: true, active_order: null, location: location(48.8264, 58.1442) },
        { driver: { id: 'near', code: 'ALA001', first_name: '', last_name: '', phone: null }, status: 'approved', is_online: true, active_order: null, location: location(43.24, 76.89) },
      ],
    })
    const assign = vi.spyOn(ordersApi, 'assign').mockResolvedValue({ ...order, driver: { id: 'far', code: 'EMB001', first_name: '', last_name: '', phone: null }, warnings: ['offline', 'out_of_radius'] })

    const wrapper = mount(AssignDriverDialog, { props: { open: false, order }, global: { plugins: [i18n] }, attachTo: document.body })
    await wrapper.setProps({ open: true })
    await flushPromises()

    expect(list.mock.calls[0]![0]).not.toHaveProperty('city_id')
    expect(document.body.textContent).toContain('Minivan · seats: 7')
    const rows = [...document.querySelectorAll<HTMLButtonElement>('.assign__row')]
    expect(rows.map((r) => r.querySelector('.assign__code')?.textContent)).toEqual(['ALA001', 'EMB001'])

    rows[1]!.click()
    await flushPromises()
    const buttons = [...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')]
    buttons[buttons.length - 1]!.click()
    await flushPromises()

    expect(assign).toHaveBeenCalledWith('o1', 'far', false)
    expect(wrapper.emitted('assigned')).toHaveLength(1)
    expect(wrapper.emitted('update:open')).toBeUndefined()
    const body = document.body.textContent ?? ''
    expect(body).toContain('The driver was offline when assigned.')
    expect(body).toContain('The driver is farther from pickup A than the search radius.')
    wrapper.unmount()
  })

  it('closes at once when the assignment has no warnings', async () => {
    vi.spyOn(driversApi, 'list').mockResolvedValue({ items: [driver('d1', 'EMB002', true)], page: 1, page_size: 20, total: 1 })
    vi.spyOn(driversApi, 'locations').mockResolvedValue({ items: [] })
    vi.spyOn(ordersApi, 'assign').mockResolvedValue({ ...order, warnings: undefined })

    const wrapper = mount(AssignDriverDialog, { props: { open: false, order }, global: { plugins: [i18n] }, attachTo: document.body })
    await wrapper.setProps({ open: true })
    await flushPromises()
    document.querySelector<HTMLButtonElement>('.assign__row')!.click()
    await flushPromises()
    const buttons = [...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')]
    buttons[buttons.length - 1]!.click()
    await flushPromises()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
    wrapper.unmount()
  })
})

describe('distanceMeters', () => {
  it('measures great-circle distance', () => {
    expect(distanceMeters({ lat: 48.8264, lon: 58.1442 }, { lat: 48.8264, lon: 58.1442 })).toBe(0)
    const embiAlmaty = distanceMeters({ lat: 48.8264, lon: 58.1442 }, { lat: 43.2389, lon: 76.8897 })
    expect(embiAlmaty).toBeGreaterThan(1_500_000)
    expect(embiAlmaty).toBeLessThan(1_650_000)
  })
})
