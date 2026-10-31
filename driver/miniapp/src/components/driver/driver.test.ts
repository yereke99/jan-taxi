// Driver components: the registration's body type row and IIN field (§11,
// §21), passenger contact on the trip (§22.4/§22.5) and bonus rows in the
// wallet (§22.1/§22.7).

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
import IinField from './registration/IinField.vue'
import VehicleClassRow from './registration/VehicleClassRow.vue'
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

describe('VehicleClassRow', () => {
  function setup(vehicle: Record<string, unknown> = {}): ReturnType<typeof useRegistrationStore> {
    useSessionStore().extras.vehicleClasses = [cls('sedan', 4, 4, 400, 'Седан'), cls('minivan', 7, 8, 800, 'Минивэн')]
    const reg = useRegistrationStore()
    reg.registration = normalizeRegistration({ id: 'd1', status: 'draft', editable: true, vehicle })
    return reg
  }

  it('shows the chosen class with its capacity in one row', () => {
    const reg = setup({ vehicle_class: 'sedan', vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, seats: 4, cargo_volume_l: 400 })
    reg.form.vehicle_class = 'sedan'
    const w = mount(VehicleClassRow, { props: { showErrors: false }, ...mountOpts() })
    expect(w.findAll('[role="radio"]')).toHaveLength(0)
    const row = w.find('button[aria-expanded]')
    expect(row.text()).toContain('Седан')
    expect(row.text()).toContain('4 орын · 400 л')
    expect(row.find('svg').exists()).toBe(true)
  })

  it('lists the active classes on tap; picking one closes the list', async () => {
    const reg = setup()
    reg.form.vehicle_class = 'sedan'
    const w = mount(VehicleClassRow, { props: { showErrors: false }, ...mountOpts() })
    await w.find('button[aria-expanded]').trigger('click')
    const options = w.findAll('[role="radio"]')
    expect(options).toHaveLength(2)
    expect(options[0]!.attributes('aria-checked')).toBe('true')
    expect(options[1]!.text()).toContain('Toyota Alphard, Sienna')
    await options[1]!.trigger('click')
    expect(reg.form.vehicle_class).toBe('minivan')
    expect(w.findAll('[role="radio"]')).toHaveLength(0)
    expect(w.find('button[aria-expanded]').text()).toContain('7 орын · 800 л')
  })

  it('asks for a class when there is none', () => {
    setup()
    const w = mount(VehicleClassRow, { props: { showErrors: true }, ...mountOpts() })
    expect(w.find('[role="alert"]').text()).toBe('Көлік түрін таңдаңыз')
    expect(w.find('button[aria-expanded]').text()).toContain('Таңдаңыз')
  })

  it('is locked when the class was not flagged (corrections)', () => {
    const reg = useRegistrationStore()
    useSessionStore().extras.vehicleClasses = [cls('sedan', 4, 4, 400, 'Седан')]
    reg.registration = normalizeRegistration({
      id: 'd1',
      status: 'changes_requested',
      editable: true,
      iin: '900101300017',
      corrections: [{ field: 'vehicle_color', reason: 'other' }],
      vehicle: { vehicle_class: 'sedan', seats: 4, cargo_volume_l: 400 },
    })
    reg.form.vehicle_class = 'sedan'
    const w = mount(VehicleClassRow, { props: { showErrors: false }, ...mountOpts() })
    expect(w.find('button[aria-expanded]').exists()).toBe(false)
    expect(w.find('button').attributes('disabled')).toBeDefined()
  })

  it('offers a retry when the class list could not be loaded', async () => {
    const reg = useRegistrationStore()
    reg.registration = normalizeRegistration({ id: 'd1', status: 'draft', editable: true, vehicle: {} })
    const w = mount(VehicleClassRow, { props: { showErrors: false }, ...mountOpts() })
    await w.find('button[aria-expanded]').trigger('click')
    expect(w.text()).toContain('Көлік түрлерінің тізімі жүктелмеді')
  })
})

describe('IinField', () => {
  const mountField = (modelValue: string, showErrors = false, serverError = '') =>
    mount(IinField, { props: { modelValue, label: 'ЖСН', showErrors, serverError }, ...mountOpts() })

  it('keeps digits only, with the numeric keyboard and a 12-digit counter', async () => {
    const w = mountField('')
    const input = w.find('input')
    expect(input.attributes('inputmode')).toBe('numeric')
    await input.setValue('9001 0130-a')
    expect(w.emitted('update:modelValue')!.at(-1)).toEqual(['90010130'])
    expect((input.element as HTMLInputElement).value).toBe('90010130')
    expect(w.text()).toContain('0/12')
  })

  it('reports a wrong check digit at once, an unfinished number only after Next', () => {
    expect(mountField('900101300018').find('[role="alert"]').text()).toBe('ЖСН қате: цифрларды тексеріңіз')
    expect(mountField('9001').find('[role="alert"]').exists()).toBe(false)
    expect(mountField('9001', true).find('[role="alert"]').text()).toBe('ЖСН 12 цифрдан тұрады')
    expect(mountField('', true).find('[role="alert"]').text()).toBe('ЖСН-ді енгізіңіз')
    expect(mountField('900101300017').find('[role="alert"]').exists()).toBe(false)
  })

  it('shows the server answer first', () => {
    const w = mountField('900101300017', false, 'Бұл ЖСН басқа жүргізушіге тіркелген')
    expect(w.find('[role="alert"]').text()).toBe('Бұл ЖСН басқа жүргізушіге тіркелген')
  })
})

describe('TripContacts', () => {
  const order = (passenger: unknown) =>
    normalizeDetail({ id: 'o1', number: 5, status: 'accepted', is_mine: true, category: 'taxi', recipient_phone: null, passenger })

  it('calls the passenger or opens WhatsApp when the phone was shared', () => {
    const w = mount(TripContacts, {
      props: { order: order({ first_name: 'Айгерім', phone: '+77017654321', phone_verified: true, telegram_username: 'aigerim_k', telegram_url: 'https://t.me/aigerim_k' }) },
      ...mountOpts(),
    })
    expect(w.text()).toContain('Айгерім')
    expect(w.find('a[href^="tel:"]').attributes('href')).toBe('tel:+77017654321')
    expect(w.find('a[href="https://wa.me/77017654321"]').attributes('aria-label')).toBe('Жолаушыға WhatsApp-та жазу')
    expect(w.find('a[href="https://t.me/aigerim_k"]').exists()).toBe(false)
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
