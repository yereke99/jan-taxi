// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { createI18nInstance } from '../../i18n/core'
import JSegmented from './JSegmented.vue'
import JSheet from './JSheet.vue'
import JSwitch from './JSwitch.vue'
import JTextField from './JTextField.vue'

const global = { plugins: [createI18nInstance()] }

describe('JTextField', () => {
  it('counts code points like the backend and flags text over the limit', async () => {
    const wrapper = mount(JTextField, { props: { modelValue: 'Сәлем', max: 120, label: 'Пікір' }, global })
    expect(wrapper.find('.jt-field__counter').text()).toBe('5/120')
    expect(wrapper.find('input').attributes('aria-invalid')).toBeUndefined()

    await wrapper.setProps({ modelValue: 'қ'.repeat(121) })
    expect(wrapper.find('.jt-field__counter').text()).toBe('121/120')
    expect(wrapper.find('.jt-field__counter').classes()).toContain('is-over')
    expect(wrapper.find('input').attributes('aria-invalid')).toBe('true')
  })

  it('emits input and associates the label', async () => {
    const wrapper = mount(JTextField, { props: { modelValue: '', label: 'Аты' }, global })
    const input = wrapper.find('input')
    expect(wrapper.find('label').attributes('for')).toBe(input.attributes('id'))
    await input.setValue('Әлия')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['Әлия'])
  })
})

describe('JSegmented', () => {
  const options = [
    { value: 'taxi', label: 'Такси' },
    { value: 'parcel', label: 'Сәлемдеме' },
    { value: 'other', label: 'Басқа' },
  ]

  it('marks the selection and emits changes', async () => {
    const wrapper = mount(JSegmented, { props: { modelValue: 'taxi', options }, global })
    const radios = wrapper.findAll('[role="radio"]')
    expect(radios[0]!.attributes('aria-checked')).toBe('true')
    await radios[1]!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['parcel'])
    await radios[0]!.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
  })
})

describe('JSwitch', () => {
  it('toggles and exposes switch semantics', async () => {
    const wrapper = mount(JSwitch, { props: { modelValue: false, label: 'Желіде' }, global })
    const btn = wrapper.find('button')
    expect(btn.attributes('role')).toBe('switch')
    expect(btn.attributes('aria-checked')).toBe('false')
    await btn.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([true])
    await wrapper.setProps({ busy: true })
    await btn.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1)
  })
})

describe('JSheet', () => {
  it('renders a footer slot that appears after mount and removes it again', async () => {
    const ready = ref(false)
    const Host = defineComponent({
      setup: () => () =>
        h(JSheet, { snap: 'content', snapPoints: ['content'] }, {
          default: () => h('p', 'body'),
          ...(ready.value ? { footer: () => h('button', { class: 'accept' }, 'OK') } : {}),
        }),
    })
    const wrapper = mount(Host, { global })
    expect(wrapper.find('.jt-sheet__footer').exists()).toBe(false)
    expect(wrapper.find('.jt-sheet__content').classes()).not.toContain('has-footer')

    ready.value = true
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.jt-sheet__footer .accept').exists()).toBe(true)
    expect(wrapper.find('.jt-sheet__content').classes()).toContain('has-footer')

    ready.value = false
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.jt-sheet__footer').exists()).toBe(false)
  })

  // iOS sends the click of a short swipe after a round trip to the UI
  // process, later than a 0 ms timer; it must still not reach the control
  // the swipe started on.
  it('swallows the click that ends a drag, even when it arrives late', async () => {
    vi.useFakeTimers()
    try {
      const tapped = vi.fn()
      const wrapper = mount(JSheet, {
        props: { snap: 'half', snapPoints: ['half', 'full'] },
        slots: { default: () => h('button', { class: 'row', onClick: tapped }, 'row') },
        global,
        attachTo: document.body,
      })
      const row = wrapper.find('.row').element
      const pointer = (target: EventTarget, type: string, y: number): void => {
        target.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 3, pointerType: 'touch', isPrimary: true, clientX: 100, clientY: y }))
      }
      pointer(row, 'pointerdown', 500)
      pointer(window, 'pointermove', 492)
      pointer(window, 'pointermove', 470)
      pointer(window, 'pointerup', 470)
      await vi.advanceTimersByTimeAsync(40)
      row.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 }))
      expect(tapped).not.toHaveBeenCalled()

      // The next touch is a new gesture: its click goes through.
      pointer(row, 'pointerdown', 470)
      pointer(window, 'pointerup', 470)
      row.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 }))
      expect(tapped).toHaveBeenCalledTimes(1)
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  // A call button in the header (passenger trip card) or the content (driver
  // contacts) must open on a tap whose finger moves a few pixels.
  it('never starts a drag from a control marked data-sheet-no-drag, in the header or the content', () => {
    const tapped = vi.fn()
    const wrapper = mount(JSheet, {
      props: { snap: 'half', snapPoints: ['half', 'full'] },
      slots: {
        header: () => [
          h('p', { class: 'title' }, 'title'),
          h('a', { class: 'call', 'data-sheet-no-drag': '', onClick: tapped }, 'call'),
        ],
        default: () => h('button', { class: 'contact', 'data-sheet-no-drag': '', onClick: tapped }, 'contact'),
      },
      global,
      attachTo: document.body,
    })
    const pointer = (target: EventTarget, type: string, y: number): void => {
      target.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 4, pointerType: 'touch', isPrimary: true, clientX: 100, clientY: y }))
    }
    for (const selector of ['.call', '.contact']) {
      const el = wrapper.find(selector).element
      pointer(el, 'pointerdown', 500)
      pointer(window, 'pointermove', 490)
      pointer(window, 'pointerup', 490)
      const click = new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 })
      el.dispatchEvent(click)
      expect(click.defaultPrevented).toBe(false)
    }
    expect(tapped).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('update:snap')).toBeUndefined()

    // The rest of the header still drags the sheet.
    const title = wrapper.find('.title').element
    pointer(title, 'pointerdown', 500)
    pointer(window, 'pointermove', 490)
    pointer(window, 'pointermove', 190)
    pointer(window, 'pointerup', 190)
    expect(wrapper.emitted('update:snap')?.[0]).toEqual(['full'])
    wrapper.unmount()
  })
})
