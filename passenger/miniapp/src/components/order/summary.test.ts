import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { defineComponent, h, type PropType } from 'vue'
import { parseOrder } from '../../api/parse'
import type { OrderSummary } from '../../api/types'
import { usePromoText, type PromoBreakdownSource } from '../../composables/usePromoText'
import { createI18nInstance } from '../../i18n/core'
import { en } from '../../i18n/messages/en'
import { kk } from '../../i18n/messages/kk'
import { ru } from '../../i18n/messages/ru'
import OrderStopList from './OrderStopList.vue'
import OrderSummaryRow from './OrderSummaryRow.vue'

const global = { plugins: [createI18nInstance({ kk, ru, en })] }
/** Money uses no-break spaces. */
const plain = (s: string) => s.replace(/\u00a0/g, ' ')

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

  it('shows an address the passenger wrote exactly as written', () => {
    const typed = { seq: 0, kind: 'pickup' as const, lat: 48.82, lon: 58.14, address: '48.82, 58.14', address_typed: true }
    expect(mountRow({ pickup: typed }).findAll('.summary__address')[0]!.text()).toBe('48.82, 58.14')
    // The same text from the map is coordinates, never shown as an address.
    expect(mountRow({ pickup: { ...typed, address_typed: false } }).findAll('.summary__address')[0]!.text()).toBe('Картадағы нүкте')
  })
})

describe('OrderStopList', () => {
  it('marks the stops the passenger wrote, as drivers see them: a typed pickup A and a manual stop', () => {
    const order = parseOrder({
      id: 'o1',
      status: 'searching',
      stops: [
        { id: 'a', seq: 0, kind: 'pickup', lat: 48.82, lon: 58.14, address: 'Мешіттің жанында, көк қақпа', address_typed: true },
        { id: 'b', seq: 1, kind: 'intermediate', lat: 48.83, lon: 58.15, address: '48.830000, 58.150000' },
        { id: 'c', seq: 2, kind: 'destination', mode: 'manual', lat: null, lon: null, address: 'Ескі мәдениет үйі' },
      ],
    })
    const w = mount(OrderStopList, { props: { stops: order.stops }, global })
    const rows = w.findAll('.order-stops__row')
    expect(rows.map((r) => r.find('.order-stops__address').text())).toEqual(['Мешіттің жанында, көк қақпа', 'Картадағы нүкте', 'Ескі мәдениет үйі'])
    expect(rows.map((r) => r.text().includes('Жолаушы көрсеткен орын'))).toEqual([true, false, true])
  })
})

describe('an order with a coupon or bonus (§23.6)', () => {
  it('lists what the passenger pays with the fare crossed out under it', () => {
    const w = mountRow({ fare: 1500, promo_discount: 400, cash_due: 1100 })
    expect(plain(w.find('.summary__fare').text())).toBe('1 100 ₸')
    const gross = w.find('s.summary__gross')
    expect(plain(gross.text())).toBe('Сапар құны: 1 500 ₸')
    expect(gross.find('.jt-visually-hidden').text()).toBe('Сапар құны:')
  })

  it('lists the fare alone without a promotion', () => {
    const w = mountRow({ fare: 700, promo_discount: 0, cash_due: 700 })
    expect(plain(w.find('.summary__fare').text())).toBe('700 ₸')
    expect(w.find('.summary__gross').exists()).toBe(false)
  })

  it('lists a cancelled order at its plain fare: the discount is only a snapshot, nothing was paid', () => {
    const w = mountRow({ status: 'cancelled', fare: 1500, promo_discount: 400, cash_due: 1100, cancelled_at: '2026-10-19T10:05:00Z' })
    expect(plain(w.find('.summary__fare').text())).toBe('1 500 ₸')
    expect(w.find('.summary__gross').exists()).toBe(false)
  })

  it('writes the compact breakdown under the price: the fare and what the coupon or bonus took off', () => {
    const Line = defineComponent({
      props: { o: { type: Object as PropType<PromoBreakdownSource>, required: true } },
      setup(props) {
        const promo = usePromoText()
        return () => h('p', promo.breakdown(props.o))
      },
    })
    const line = (o: PromoBreakdownSource) => plain(mount(Line, { props: { o }, global }).text())
    expect(line({ fare: 1500, promo_discount: 400, cash_due: 1100, promotion: { kind: 'coupon' } })).toBe('1 500 ₸ · Купон −400 ₸')
    expect(line({ fare: 700, promo_discount: 300, cash_due: 400, promotion: { kind: 'bonus' } })).toBe('700 ₸ · Бонус −300 ₸')
    // A list row knows only the amounts.
    expect(line({ fare: 1500, promo_discount: 400, cash_due: 1100 })).toBe('1 500 ₸ · Жеңілдік −400 ₸')
    expect(line({ fare: 700, promo_discount: 0, cash_due: 700 })).toBe('')
    expect(line({ fare: 1500, promo_discount: 400, cash_due: 1100, status: 'cancelled', promotion: { kind: 'coupon' } })).toBe('')
  })
})
