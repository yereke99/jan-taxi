// Orders show the settlement of pickup A next to the route (a passenger in
// Almaty reads «Алматы», whatever region priced the order); coordinates and
// malformed values are never shown.

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { localityOrNull, normalizeOrderPlace } from '../../api/normalize'
import { normalizeCard, normalizeDetail, normalizeSummary } from '../../api/orders'
import { i18n } from '../../i18n'
import OrderCard from './OrderCard.vue'

const KAZ = { code: 'KAZ', name: { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } }

function card(extra: Record<string, unknown> = {}) {
  return normalizeCard({
    id: 'o1034',
    number: 1034,
    category: 'taxi',
    service_type: 'passenger',
    cargo_type: 'none',
    stops: [
      { seq: 0, kind: 'pickup', lat: 43.2389, lon: 76.8897, address: 'Абай даңғылы, 10' },
      { seq: 1, kind: 'destination', lat: 43.25, lon: 76.9, address: 'Достық даңғылы, 5' },
    ],
    stop_count: 2,
    comment: null,
    cargo_description: null,
    fare: 700,
    currency: 'KZT',
    commission_amount: 50,
    payment_method: 'cash',
    distance_m: 3000,
    duration_s: 420,
    distance_to_pickup_m: 900,
    created_at: new Date().toISOString(),
    search_expires_at: null,
    vehicle_class: 'sedan',
    passenger_count: 1,
    locality: 'Алматы',
    tariff_region: KAZ,
    ...extra,
  })
}

let pinia: Pinia

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  i18n.global.locale.value = 'kk'
})

describe('order place', () => {
  it('keeps the locality and the tariff region', () => {
    expect(normalizeOrderPlace({ locality: ' Алматы ', tariff_region: KAZ })).toEqual({ locality: 'Алматы', tariff_region: KAZ })
  })

  it('never keeps coordinates or malformed values', () => {
    expect(localityOrNull('43.238900, 76.889700')).toBeNull()
    expect(localityOrNull('')).toBeNull()
    expect(localityOrNull(42)).toBeNull()
    expect(localityOrNull('x'.repeat(121))).toBeNull()
    expect(localityOrNull('x'.repeat(120))).toBe('x'.repeat(120))
    expect(normalizeOrderPlace({ tariff_region: { code: 'KAZ' } })).toEqual({ locality: null, tariff_region: null })
    expect(normalizeOrderPlace(null)).toEqual({ locality: null, tariff_region: null })
  })

  it('is part of cards, details and history rows', () => {
    expect(card().locality).toBe('Алматы')
    expect(normalizeDetail({ id: 'o1', locality: 'Ембі', tariff_region: KAZ })).toMatchObject({ locality: 'Ембі', tariff_region: KAZ })
    expect(normalizeSummary({ id: 'o1' })).toMatchObject({ locality: null, tariff_region: null })
  })
})

describe('OrderCard', () => {
  it('shows the locality next to the route', () => {
    const w = mount(OrderCard, { props: { order: card(), now: Date.now() }, global: { plugins: [pinia, i18n] } })
    const place = w.find('.ocard__locality')
    expect(place.exists()).toBe(true)
    expect(place.text()).toContain('Алматы')
    expect(place.find('svg').exists()).toBe(true)
    expect(w.find('.ocard__body').text()).toMatch(/Алматы.*Абай даңғылы, 10/)
  })

  it('shows nothing extra when the locality is unknown', () => {
    const w = mount(OrderCard, { props: { order: card({ locality: '43.2389, 76.8897' }), now: Date.now() }, global: { plugins: [pinia, i18n] } })
    expect(w.find('.ocard__locality').exists()).toBe(false)
    expect(w.text()).not.toContain('43.2389')
  })
})
