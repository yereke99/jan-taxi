// Driver components for the v2 rules: vehicle class cards (§21), passenger
// contact on the trip (§22.4/§22.5) and bonus rows in the wallet (§22.1/§22.7).

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { normalizeRegistration } from '../../api/normalize'
import { normalizeDetail } from '../../api/orders'
import type { LedgerEntry } from '../../api/types'
import type { VehicleClassOption } from '../../features/vehicle/classes'
import { i18n } from '../../i18n'
import { useRegistrationStore } from '../../stores/registration'
import { useSessionStore } from '../../stores/session'
import LedgerRow from './LedgerRow.vue'
import StepVehicleClass from './registration/StepVehicleClass.vue'
import TripContacts from './TripContacts.vue'

function cls(code: VehicleClassOption['icon'], seatsDefault: number, seatsMax: number, cargo: number, name: string): VehicleClassOption {
  return {
    code,
    name: { kk: name, ru: name, en: name },
    examples: code === 'minivan' ? 'Toyota Alphard, Sienna' : '',
    icon: code,
    seats_default: seatsDefault,
    seats_max: seatsMax,
    cargo_volume_l_default: cargo,
    passenger_capable: true,
    cargo_capable: true,
  }
}

let pinia: Pinia
const mountOpts = () => ({ global: { plugins: [pinia, i18n] } })

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  i18n.global.locale.value = 'kk'
})

describe('StepVehicleClass', () => {
  function setup(): ReturnType<typeof useRegistrationStore> {
    useSessionStore().extras.vehicleClasses = [cls('sedan', 4, 4, 400, 'Седан'), cls('minivan', 7, 8, 800, 'Минивэн')]
    const reg = useRegistrationStore()
    reg.registration = normalizeRegistration({ id: 'd1', status: 'draft', editable: true, vehicle: {} })
    return reg
  }

  it('shows a card per class with its capacity', () => {
    setup()
    const w = mount(StepVehicleClass, { props: { showErrors: false }, ...mountOpts() })
    const cards = w.findAll('[role="radio"]')
    expect(cards).toHaveLength(2)
    expect(cards[0]!.text()).toContain('Седан')
    expect(cards[0]!.text()).toContain('4 орын · 400 л')
    expect(cards[1]!.text()).toContain('Toyota Alphard, Sienna')
    expect(cards[1]!.find('svg').exists()).toBe(true)
    expect(w.text()).not.toContain('Жолаушы орны') // capacity appears after a choice
  })

  it('choosing a card prefills seats and cargo; the stepper stops at the class maximum', async () => {
    const reg = setup()
    const w = mount(StepVehicleClass, { props: { showErrors: false }, ...mountOpts() })
    await w.findAll('[role="radio"]')[1]!.trigger('click')
    expect(reg.form).toMatchObject({ vehicle_class: 'minivan', seats: 7, cargo_volume_l: '800' })
    expect(w.findAll('[role="radio"]')[1]!.attributes('aria-checked')).toBe('true')
    const plus = w.find('.stepper__btn:last-of-type')
    await plus.trigger('click')
    expect(reg.form.seats).toBe(8)
    expect(plus.attributes('disabled')).toBeDefined()
    expect(w.find('input').element.value).toBe('800')
  })

  it('asks for a class when Next is pressed without one', () => {
    setup()
    const w = mount(StepVehicleClass, { props: { showErrors: true }, ...mountOpts() })
    expect(w.find('[role="alert"]').text()).toBe('Көлік түрін таңдаңыз')
  })

  it('offers a retry when the class list could not be loaded', () => {
    const reg = useRegistrationStore()
    reg.registration = normalizeRegistration({ id: 'd1', status: 'draft', editable: true, vehicle: {} })
    const w = mount(StepVehicleClass, { props: { showErrors: false }, ...mountOpts() })
    expect(w.findAll('[role="radio"]')).toHaveLength(0)
    expect(w.text()).toContain('Көлік түрлерінің тізімі жүктелмеді')
  })
})

describe('TripContacts', () => {
  const order = (passenger: unknown) =>
    normalizeDetail({ id: 'o1', number: 5, status: 'accepted', is_mine: true, category: 'taxi', recipient_phone: null, passenger })

  it('calls the passenger when the phone was shared and links their Telegram chat', () => {
    const w = mount(TripContacts, {
      props: { order: order({ first_name: 'Айгерім', phone: '+77017654321', phone_verified: true, telegram_username: 'aigerim_k', telegram_url: 'https://t.me/aigerim_k' }) },
      ...mountOpts(),
    })
    expect(w.text()).toContain('Айгерім')
    expect(w.find('a[href^="tel:"]').attributes('href')).toBe('tel:+77017654321')
    expect(w.find('a[href="https://t.me/aigerim_k"]').exists()).toBe(true)
  })

  it('without a shared phone offers only the Telegram chat', () => {
    const w = mount(TripContacts, {
      props: { order: order({ first_name: 'Бауыржан', phone: null, phone_verified: false, telegram_username: null, telegram_url: 'tg://user?id=700000001' }) },
      ...mountOpts(),
    })
    expect(w.find('a[href^="tel:"]').exists()).toBe(false)
    expect(w.find('a[href="tg://user?id=700000001"]').exists()).toBe(true)
    expect(w.text()).toContain('Нөмірін бөліспеген')
  })
})

describe('LedgerRow bonuses', () => {
  const entry = (over: Partial<LedgerEntry>): LedgerEntry => ({
    id: 1,
    entry_type: 'bonus_credit',
    balance_delta: 500,
    reserved_delta: 0,
    balance_after: 500,
    reserved_after: 0,
    currency: 'KZT',
    order_id: null,
    order_number: null,
    topup_id: null,
    topup_reference: null,
    description: 'welcome bonus',
    created_at: '2026-10-19T08:00:00Z',
    ...over,
  })

  it('names the welcome bonus and the company', () => {
    const w = mount(LedgerRow, { props: { entry: entry({}) }, ...mountOpts() })
    expect(w.text()).toContain('Қош келдіңіз бонусы')
    expect(w.text()).toContain('Jan Taxi компаниясынан')
    expect(w.find('.lrow__amount').classes()).toContain('is-in')
  })

  it('shows the administrator comment of a manual bonus', () => {
    const w = mount(LedgerRow, { props: { entry: entry({ balance_delta: 1000, description: 'Үздік жүргізуші' }) }, ...mountOpts() })
    expect(w.text()).toContain('Jan Taxi бонусы')
    expect(w.text()).toContain('«Үздік жүргізуші»')
  })
})
