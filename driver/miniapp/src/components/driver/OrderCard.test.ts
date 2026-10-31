// The available-order card takes its order in one tap: «Тапсырысты алу» is a
// button of its own next to (never inside) the button that opens the order,
// so taking an order never opens it and opening never takes it. It shows the
// fare, never the commission, and drops unknown route metrics.

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { DriverOrderCard } from '../../api/types'
import { i18n } from '../../i18n'
import OrderCard from './OrderCard.vue'

const plain = (s: string) => s.replace(/ /g, ' ')

function card(over: Partial<DriverOrderCard> = {}): DriverOrderCard {
  return {
    id: '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34',
    number: 1024,
    category: 'taxi',
    status: 'searching',
    fare: 400,
    currency: 'KZT',
    commission_amount: 50,
    distance_m: 3200,
    duration_s: 480,
    distance_to_pickup_m: 20,
    stop_count: 2,
    stops: [
      { id: 's1', seq: 0, kind: 'pickup', lat: 45.5, lon: 64.1, address: 'Әйтеке би көшесі, 14', address_details: null, completed_at: null },
      { id: 's2', seq: 1, kind: 'destination', lat: null, lon: null, address: 'үйге', address_details: null, completed_at: null },
    ],
    vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
    passenger_count: 1,
    cargo_size: null,
    cargo_volume_l: 0,
    cargo_description: null,
    comment: null,
    created_at: new Date().toISOString(),
    locality: 'Жалағаш',
    ...over,
  } as DriverOrderCard
}

function mountCard(props: Record<string, unknown> = {}) {
  return mount(OrderCard, { props: { order: card(), now: Date.now(), ...props }, global: { plugins: [i18n] } })
}

describe('OrderCard', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    i18n.global.locale.value = 'kk'
  })

  it('takes the order with its own button and never opens it', async () => {
    const w = mountCard()
    const accept = w.find('.ocard__accept')
    expect(accept.text()).toBe('Тапсырысты алу')
    // Two sibling buttons: no button nested in another.
    expect(w.findAll('button button')).toHaveLength(0)
    await accept.trigger('click')
    expect(w.emitted('accept')).toEqual([[card().id]])
    expect(w.emitted('open')).toBeUndefined()
  })

  it('the rest of the card opens the order', async () => {
    const w = mountCard()
    await w.find('.ocard__open').trigger('click')
    expect(w.emitted('open')).toEqual([[card().id]])
    expect(w.emitted('accept')).toBeUndefined()
  })

  it('shows a spinner while accepting and ignores more taps', async () => {
    const w = mountCard({ accepting: true })
    const accept = w.find('.ocard__accept')
    expect(accept.attributes('aria-busy')).toBe('true')
    expect(accept.text()).toBe('')
    await accept.trigger('click')
    expect(w.emitted('accept')).toBeUndefined()
  })

  it('waits while another order is being accepted', async () => {
    const w = mountCard({ disabled: true })
    const accept = w.find('.ocard__accept')
    expect(accept.attributes('disabled')).toBeDefined()
    await accept.trigger('click')
    expect(w.emitted('accept')).toBeUndefined()
  })

  it('shows the fare and the request, never the commission', () => {
    const text = plain(mountCard().text())
    expect(text).toContain('400 ₸')
    expect(text).toContain('Әйтеке би көшесі, 14')
    expect(text).toContain('үйге')
    expect(text).toContain('Седан')
    expect(text).not.toMatch(/омисси|50 ₸/)
  })

  it('leaves out unknown route metrics instead of dashes', () => {
    const known = mountCard()
    expect(known.find('.ocard__summary').exists()).toBe(true)
    const manual = mount(OrderCard, {
      props: { order: card({ distance_m: null, duration_s: null }), now: Date.now() },
      global: { plugins: [i18n] },
    })
    expect(manual.find('.ocard__summary').exists()).toBe(false)
    expect(manual.text()).not.toContain('—')
  })
})
