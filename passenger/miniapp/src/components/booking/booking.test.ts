import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18nInstance } from '../../i18n/core'
import { en } from '../../i18n/messages/en'
import { kk } from '../../i18n/messages/kk'
import { ru } from '../../i18n/messages/ru'
import type { RouteStop } from '../../domain/route'
import FarePicker from './FarePicker.vue'
import RouteStopList from './RouteStopList.vue'

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
