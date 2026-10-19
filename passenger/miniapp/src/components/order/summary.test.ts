import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { OrderSummary } from '../../api/types'
import { createI18nInstance } from '../../i18n/core'
import { en } from '../../i18n/messages/en'
import { kk } from '../../i18n/messages/kk'
import { ru } from '../../i18n/messages/ru'
import OrderSummaryRow from './OrderSummaryRow.vue'

const global = { plugins: [createI18nInstance({ kk, ru, en })] }

function summary(over: Partial<OrderSummary> = {}): OrderSummary {
  return {
    id: 'o1034',
    number: 1034,
    status: 'searching',
    category: 'taxi',
    vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
    passenger_count: 1,
    cargo_size: null,
    cargo_volume_l: 0,
    locality: 'Алматы',
    tariff_region: { code: 'KAZ', name: { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } },
    fare: 700,
    payment_method: 'cash',
    pickup: { seq: 0, kind: 'pickup', lat: 43.2389, lon: 76.8897, address: 'Абай даңғылы, 10' },
    destination: { seq: 1, kind: 'destination', lat: 43.25, lon: 76.92, address: 'Достық даңғылы, 5' },
    stop_count: 2,
    distance_m: 3200,
    driver_first_name: null,
    rating: null,
    can_rate: false,
    created_at: '2026-10-19T10:00:00Z',
    completed_at: null,
    cancelled_at: null,
    ...over,
  } as OrderSummary
}

function mountRow(over: Partial<OrderSummary> = {}, pickupSubtitle: string | null = null) {
  return mount(OrderSummaryRow, { props: { order: summary(over), locale: 'kk' as const, pickupSubtitle }, global })
}

describe('OrderSummaryRow', () => {
  it('names the settlement of pickup A under its address, not the tariff region', () => {
    const w = mountRow()
    expect(w.find('.summary__place').text()).toBe('Алматы')
    expect(w.text()).not.toContain('Қазақстан')
    expect(w.text()).not.toContain('Ембі')
    expect(w.find('.summary__meta').text()).toContain('Седан')
  })

  it('falls back to the pickup locality line seen while booking', () => {
    expect(mountRow({ locality: null }, 'Алматы, Алматы облысы').find('.summary__place').text()).toBe('Алматы, Алматы облысы')
  })

  it('shows no place rather than coordinates', () => {
    const w = mountRow({ locality: null }, '43.238900, 76.889700')
    expect(w.find('.summary__place').exists()).toBe(false)
    expect(w.text()).not.toContain('43.2389')
    expect(w.text()).not.toContain('76.8897')
  })
})
