// The fare rows of an order. With a passenger's promotion (§23.6) the fare
// stays and two rows follow: the cash to collect (emphasised) and Jan Taxi's
// part with where it is — credited after the ride, credited, or taken back. A
// cancelled order's promotion (released) credits nothing and is not shown.

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import type { OrderPromotion, PromotionStatus } from '../../api/types'
import { i18n } from '../../i18n'
import FareBreakdown from './FareBreakdown.vue'

/** Money is formatted with no-break spaces. */
const plain = (s: string) => s.replace(/\u00a0/g, ' ')

function promotion(status: PromotionStatus): OrderPromotion {
  const settled = status === 'settled' || status === 'reversed'
  return {
    kind: '',
    title: null,
    discount: 400,
    gross_fare: 1500,
    cash_due: 1100,
    status,
    compensation: settled ? 400 : null,
    settled_at: settled ? '2026-11-02T10:20:00Z' : null,
  }
}

function mountRows(props: Record<string, unknown>) {
  return mount(FareBreakdown, { props: { fare: 1500, paymentMethod: 'cash', ...props }, global: { plugins: [i18n] } })
}

/** Each row as "title | subtitle | value". */
function rows(w: ReturnType<typeof mountRows>): string[] {
  return w.findAll('.jt-row').map((r) =>
    [r.find('.jt-row__title'), r.find('.jt-row__subtitle'), r.find('.jt-row__trailing')]
      .filter((p) => p.exists())
      .map((p) => plain(p.text()))
      .join(' | '),
  )
}

beforeEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('FareBreakdown', () => {
  it('without a promotion shows the fare and the payment method only', () => {
    const w = mountRows({})
    expect(rows(w)).toEqual(['Сапар құны | 1 500 ₸', 'Төлем тәсілі | Қолма-қол'])
    expect(w.find('.fare__value').classes()).not.toContain('is-plain')
  })

  it('reserved: the cash to collect stands out, Jan Taxi adds its part after the ride', () => {
    const w = mountRows({ promotion: promotion('reserved') })
    expect(rows(w)).toEqual([
      'Сапар құны | 1 500 ₸',
      'Жолаушыдан алу | 1 100 ₸',
      'Jan Taxi акциясы | Сапардан кейін балансқа түседі | +400 ₸',
      'Төлем тәсілі | Қолма-қол',
    ])
    const values = w.findAll('.fare__value')
    expect(values[0]!.classes()).toContain('is-plain')
    expect(values[1]!.classes()).not.toContain('is-plain')
    expect(w.find('.fare__promo').classes()).toContain('is-reserved')
  })

  it('settled: the part is on the balance', () => {
    const w = mountRows({ promotion: promotion('settled') })
    expect(rows(w)[2]).toBe('Jan Taxi акциясы | Балансқа түсті | +400 ₸')
  })

  it('reversed: an operator took the part back', () => {
    const w = mountRows({ promotion: promotion('reversed') })
    expect(rows(w)[2]).toBe('Jan Taxi акциясы | Қайтарып алынды | +400 ₸')
    expect(w.find('.fare__promo').classes()).toContain('is-reversed')
  })

  it('released (the order was cancelled): no promotion rows', () => {
    const w = mountRows({ promotion: promotion('released') })
    expect(rows(w)).toEqual(['Сапар құны | 1 500 ₸', 'Төлем тәсілі | Қолма-қол'])
    expect(w.find('.fare__promo').exists()).toBe(false)
  })

  it.each([
    ['ru', ['Стоимость поездки | 1 500 ₸', 'Получить с пассажира | 1 100 ₸', 'Акция Jan Taxi | Поступит на баланс после поездки | +400 ₸', 'Способ оплаты | Наличные']],
    ['en', ['Fare | 1,500 ₸', 'Collect from the passenger | 1,100 ₸', 'Jan Taxi promotion | Goes to your balance after the ride | +400 ₸', 'Payment method | Cash']],
  ] as const)('%s: names the rows in the chosen language', (locale, expected) => {
    i18n.global.locale.value = locale
    expect(rows(mountRows({ promotion: promotion('reserved') }))).toEqual(expected)
  })

  it('names neither the commission nor its amount', () => {
    for (const status of ['reserved', 'settled', 'reversed'] as const) {
      const text = plain(mountRows({ promotion: promotion(status) }).text())
      expect(text).not.toMatch(/омисси|ommission|\b50 ₸/)
    }
  })
})
