import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18nInstance } from '../../i18n/core'
import { en } from '../../i18n/messages/en'
import { kk } from '../../i18n/messages/kk'
import { ru } from '../../i18n/messages/ru'
import type { RouteQuoteOption } from '../../api/types'
import type { RouteStop } from '../../domain/route'
import { DEFAULT_CARGO_SIZES } from '../../services/config'
import CargoSizePicker from './CargoSizePicker.vue'
import FarePicker from './FarePicker.vue'
import PassengerStepper from './PassengerStepper.vue'
import RouteStopList from './RouteStopList.vue'
import VehicleOptions from './VehicleOptions.vue'

const global = { plugins: [createI18nInstance({ kk, ru, en })] }

function stop(key: string, role: RouteStop['role'], address: string | null): RouteStop {
  return {
    key,
    role,
    place: address ? { lat: 48.8, lon: 58.1, address, subtitle: null, source: 'search', inServiceArea: true } : null,
  }
}

const four: RouteStop[] = [
  stop('a', 'pickup', 'Абай 1'),
  stop('b', 'intermediate', 'Дүкен'),
  stop('c', 'intermediate', 'Мектеп'),
  stop('d', 'destination', 'Вокзал'),
]

describe('RouteStopList', () => {
  it('letters stops in order and shows placeholders for empty ones', () => {
    const w = mount(RouteStopList, {
      props: { stops: [stop('a', 'pickup', null), stop('b', 'destination', 'Вокзал')], errors: {}, canAdd: true, maxPoints: 5 },
      global,
    })
    const rows = w.findAll('.stops__row')
    expect(rows.map((r) => r.find('.stop-badge').text())).toEqual(['A', 'B'])
    expect(rows[0]!.text()).toContain('Қайдан кетесіз?')
    expect(rows[1]!.text()).toContain('Вокзал')
  })

  it('offers remove and reorder only for intermediate stops', async () => {
    const w = mount(RouteStopList, { props: { stops: four, errors: {}, canAdd: true, maxPoints: 5 }, global })
    const rows = w.findAll('.stops__row')
    expect(rows[0]!.find('.stops__remove').exists()).toBe(false)
    expect(rows[3]!.find('.stops__remove').exists()).toBe(false)
    expect(rows[1]!.find('.stops__handle').exists()).toBe(true)

    // B can move down to C's place but not up into the pickup.
    const [up, down] = rows[1]!.findAll('.stops__key')
    expect(up!.attributes('disabled')).toBeDefined()
    await down!.trigger('click')
    expect(w.emitted('move')).toEqual([[1, 2]])

    await rows[2]!.find('.stops__remove').trigger('click')
    expect(w.emitted('remove')).toEqual([[2]])
  })

  it('disables "add stop" at the maximum and says why', () => {
    const w = mount(RouteStopList, { props: { stops: four, errors: {}, canAdd: false, maxPoints: 4 }, global })
    const add = w.find('.stops__add')
    expect(add.attributes('disabled')).toBeDefined()
    expect(add.text()).toContain('Ең көбі 4 нүкте')
  })

  it('shows the inline error of a stop', () => {
    const w = mount(RouteStopList, { props: { stops: four, errors: { 3: 'Қазақстаннан тыс' }, canAdd: true, maxPoints: 5 }, global })
    expect(w.findAll('.stops__row')[3]!.find('[role="alert"]').text()).toBe('Қазақстаннан тыс')
  })
})

describe('FarePicker', () => {
  const fare = { recommended: 700, min: 700, max: 800, step: 50, options: [700, 750, 800] }

  it('cannot go below the minimum or above the maximum', async () => {
    const w = mount(FarePicker, { props: { fare, modelValue: 700, locale: 'kk' as const }, global })
    const [minus, plus] = w.findAll('.fare__step')
    expect(minus!.attributes('disabled')).toBeDefined()
    await plus!.trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([[750]])

    await w.setProps({ modelValue: 800 })
    expect(w.findAll('.fare__step')[1]!.attributes('disabled')).toBeDefined()
  })

  it('exposes the value as a spinbutton and supports arrow keys', async () => {
    const w = mount(FarePicker, { props: { fare, modelValue: 750, locale: 'kk' as const }, global })
    const spin = w.find('[role="spinbutton"]')
    expect(spin.attributes('aria-valuemin')).toBe('700')
    expect(spin.attributes('aria-valuemax')).toBe('800')
    await spin.trigger('keydown', { key: 'ArrowDown' })
    await spin.trigger('keydown', { key: 'End' })
    expect(w.emitted('update:modelValue')).toEqual([[700], [800]])
  })
})

function option(vehicleClass: string, over: Partial<RouteQuoteOption> = {}): RouteQuoteOption {
  return {
    vehicle_class: vehicleClass,
    name: null,
    examples: '',
    icon: 'sedan',
    seats_max: 4,
    cargo_volume_l: 400,
    quote_id: `q-${vehicleClass}`,
    expires_at: '',
    fare: { recommended: 700, min: 700, max: 900, step: 50, options: [700, 750, 800, 850, 900] },
    commission_bps: 1000,
    eta_s: null,
    ...over,
  }
}

describe('VehicleOptions', () => {
  const options = [
    option('sedan', { name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, eta_s: 180 }),
    option('minivan', {
      icon: 'minivan',
      seats_max: 8,
      cargo_volume_l: 800,
      examples: 'Alphard, Sienna',
      fare: { recommended: 1200, min: 1200, max: 1500, step: 100, options: [1200, 1300, 1400, 1500] },
    }),
  ]

  it('lists each class with seats, ETA, price and a check on the chosen one', async () => {
    const w = mount(VehicleOptions, { props: { options, selected: 'sedan', delivery: false, fare: 800, locale: 'kk' as const }, global })
    const rows = w.findAll('[role="radio"]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.attributes('aria-checked')).toBe('true')
    expect(rows[0]!.text()).toContain('Седан')
    expect(rows[0]!.text()).toContain('4 орынға дейін')
    expect(rows[0]!.text()).toContain('~3 мин')
    // The chosen row shows the passenger's fare, the others their recommendation.
    expect(rows[0]!.find('.vclass__price').text()).toBe('800\u00a0₸')
    expect(rows[1]!.find('.vclass__price').text()).toBe('1\u00a0200\u00a0₸')
    // A class without an API name uses the app's own name.
    expect(rows[1]!.text()).toContain('Минивэн')
    expect(rows[1]!.attributes('title')).toBe('Alphard, Sienna')
    expect(rows[0]!.find('svg.lucide').exists()).toBe(true)
    expect(rows[1]!.find('.vclass__check svg').exists()).toBe(false)

    await rows[1]!.trigger('click')
    await rows[0]!.trigger('click')
    expect(w.emitted('select')).toEqual([['minivan']])
  })

  // Order #1034: a taxi for one passenger was placed as a minibus. Swiping the
  // half-open confirmation sheet up from the last class row ended with a click
  // on that row (iOS delivers the click of a short swipe), and the row was
  // selected without a real tap.
  describe('selects only on a genuine tap', () => {
    const four = [
      option('sedan'),
      option('universal'),
      option('minivan', { icon: 'minivan', seats_max: 8 }),
      option('microbus', { icon: 'microbus', seats_max: 20 }),
    ]

    function press(el: Element, type: string, x: number, y: number, over: PointerEventInit = {}): void {
      el.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 7, pointerType: 'touch', isPrimary: true, clientX: x, clientY: y, ...over }))
    }

    /** A click as the browser sends it after a touch or a mouse press (detail 1). */
    function touchClick(el: Element): void {
      el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 }))
    }

    function mountFour() {
      return mount(VehicleOptions, { props: { options: four, selected: 'sedan', delivery: false, fare: 700, locale: 'kk' as const }, global, attachTo: document.body })
    }

    it('ignores the click that ends a swipe over a row', async () => {
      const w = mountFour()
      const microbus = w.findAll('[role="radio"]')[3]!.element
      press(microbus, 'pointerdown', 120, 400)
      press(microbus, 'pointermove', 121, 390)
      press(microbus, 'pointerup', 121, 388)
      touchClick(microbus)
      await w.vm.$nextTick()
      expect(w.emitted('select')).toBeUndefined()
      w.unmount()
    })

    it('ignores a press during which the sheet scrolled or moved', async () => {
      const w = mountFour()
      const [, universal, minivan] = w.findAll('[role="radio"]').map((r) => r.element)
      // The content scrolled under a still finger.
      press(universal!, 'pointerdown', 120, 300)
      document.dispatchEvent(new Event('scroll'))
      press(universal!, 'pointerup', 120, 300)
      touchClick(universal!)
      // The sheet snapped while the finger rested: the row moved under it.
      let top = 300
      minivan!.getBoundingClientRect = () => ({ top, bottom: top + 60, left: 0, right: 375, width: 375, height: 60, x: 0, y: top, toJSON: () => ({}) })
      press(minivan!, 'pointerdown', 120, 330)
      top = 180
      press(minivan!, 'pointerup', 120, 330)
      touchClick(minivan!)
      await w.vm.$nextTick()
      expect(w.emitted('select')).toBeUndefined()
      w.unmount()
    })

    it('ignores a click without a press on that row and a cancelled press', async () => {
      const w = mountFour()
      const [sedan, universal, minivan] = w.findAll('[role="radio"]').map((r) => r.element)
      press(sedan!, 'pointerdown', 120, 240)
      press(sedan!, 'pointerup', 120, 240)
      touchClick(universal!)
      press(minivan!, 'pointerdown', 120, 330)
      press(minivan!, 'pointercancel', 120, 330)
      touchClick(minivan!)
      await w.vm.$nextTick()
      expect(w.emitted('select')).toBeUndefined()
      w.unmount()
    })

    it('selects on a tap that moved less than 8 px, and from the keyboard', async () => {
      const w = mountFour()
      const [, universal, minivan] = w.findAll('[role="radio"]').map((r) => r.element)
      press(minivan!, 'pointerdown', 120, 330)
      press(minivan!, 'pointermove', 123, 334)
      press(minivan!, 'pointerup', 124, 335)
      touchClick(minivan!)
      // Enter / Space on a focused row: a click with detail 0.
      universal!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 0 }))
      await w.vm.$nextTick()
      expect(w.emitted('select')).toEqual([['minivan'], ['universal']])
      w.unmount()
    })
  })

  it('shows cargo volume for deliveries and ignores taps while disabled', async () => {
    const w = mount(VehicleOptions, { props: { options, selected: 'sedan', delivery: true, fare: 700, locale: 'kk' as const, disabled: true }, global })
    expect(w.findAll('[role="radio"]')[1]!.text()).toContain('800 литрге дейін')
    await w.findAll('[role="radio"]')[1]!.trigger('click')
    expect(w.emitted('select')).toBeUndefined()
  })
})

describe('PassengerStepper', () => {
  it('steps within 1..max', async () => {
    const w = mount(PassengerStepper, { props: { modelValue: 1, max: 3 }, global })
    const [minus, plus] = w.findAll('.pax__key')
    expect(minus!.attributes('disabled')).toBeDefined()
    await plus!.trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([[2]])
    await w.setProps({ modelValue: 3 })
    expect(w.findAll('.pax__key')[1]!.attributes('disabled')).toBeDefined()
    await w.findAll('.pax__key')[0]!.trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([[2], [2]])
    expect(w.find('output').text()).toBe('3')
  })
})

describe('CargoSizePicker', () => {
  it('offers S, M, L, XL with what fits and the volume', async () => {
    const w = mount(CargoSizePicker, { props: { modelValue: 'm' as const, sizes: [...DEFAULT_CARGO_SIZES], locale: 'kk' as const }, global })
    expect(w.findAll('[role="radio"]').map((b) => b.text())).toEqual(['S', 'M', 'L', 'XL'])
    expect(w.find('.cargo-size__hint').text()).toBe('Сөмке немесе орташа қорап · ≈ 60 л')
    await w.findAll('[role="radio"]')[3]!.trigger('click')
    expect(w.emitted('update:modelValue')).toEqual([['xl']])
    await w.setProps({ modelValue: 'xl' as const })
    expect(w.find('.cargo-size__hint').text()).toContain('1\u00a0500')
  })
})
