// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
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
})
