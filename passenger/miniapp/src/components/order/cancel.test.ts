import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelOrder } from '../../api/orders'
import type { Order } from '../../api/types'
import { createI18nInstance } from '../../i18n/core'
import { en } from '../../i18n/messages/en'
import { kk } from '../../i18n/messages/kk'
import { ru } from '../../i18n/messages/ru'
import { ApiError } from '../../services/http'
import { useSessionStore } from '../../stores/session'
import { toast } from '../ui'
import CancelOrderSheet from './CancelOrderSheet.vue'

vi.mock('../../api/orders', () => ({ cancelOrder: vi.fn() }))

const cancel = vi.mocked(cancelOrder)

function order(over: Partial<Order> = {}): Order {
  return { id: 'o1', status: 'accepted', driver: { id: 'd1' }, can_cancel: true, ...over } as Order
}

function mountSheet(o = order()) {
  return mount(CancelOrderSheet, {
    props: { open: true, order: o },
    global: { plugins: [createI18nInstance({ kk, ru, en })], stubs: { teleport: true, transition: false } },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
  cancel.mockReset()
})

afterEach(() => {
  document.body.innerHTML = ''
})

describe('CancelOrderSheet', () => {
  it('says the cancellation is final and lists the five reasons', () => {
    const w = mountSheet()
    expect(w.text()).toContain('Бас тарту түпкілікті')
    // A driver is on the way: the passenger learns the reason stays with operators.
    expect(w.text()).toContain('Себебін тек Jan Taxi операторлары көреді')
    expect(w.findAll('[role="radio"]').map((r) => r.text())).toEqual(['Ұзақ күттім', 'Жүргізуші ұнамады', 'Көлік ұнамады', 'Жоспарым өзгерді', 'Басқа себеп'])
    // Nothing chosen yet: the destructive button waits for a reason.
    expect(w.find('.jt-button--destructive').attributes('disabled')).toBeDefined()
  })

  it('uses the reasons and labels from the public config', () => {
    useSessionStore().config.cancel_reasons = [
      { code: 'changed_plans', label: { kk: 'Жоспар өзгерді', ru: 'Планы', en: 'Plans' }, comment_required: false },
      { code: 'other', label: { kk: 'Басқа', ru: 'Другое', en: 'Other' }, comment_required: true },
    ]
    const w = mountSheet(order({ status: 'searching', driver: null }))
    expect(w.findAll('[role="radio"]').map((r) => r.text())).toEqual(['Жоспар өзгерді', 'Басқа'])
    expect(w.text()).not.toContain('операторлары')
  })

  it('requires a comment for "other", counts 120 code points and sends the reason', async () => {
    const done = order({ status: 'cancelled', can_cancel: false })
    cancel.mockResolvedValueOnce(done)
    const w = mountSheet()
    const radios = w.findAll('[role="radio"]')
    await radios[4]!.trigger('click')
    expect(radios[4]!.attributes('aria-checked')).toBe('true')
    expect(w.find('.jt-area__label').text()).toBe('Түсініктеме')
    expect(w.find('.jt-area__counter').text()).toBe('0/120')

    await w.find('.jt-button--destructive').trigger('click')
    expect(cancel).not.toHaveBeenCalled()
    expect(w.find('.jt-area__message').text()).toBe('Міндетті өріс')

    await w.find('textarea').setValue('  Басқа  көлік таптым ')
    expect(w.find('.jt-area__counter').text()).toBe('18/120')
    await w.find('.jt-button--destructive').trigger('click')
    await flushPromises()
    expect(cancel).toHaveBeenCalledWith('o1', { reason_code: 'other', comment: 'Басқа көлік таптым', expected_status: 'accepted' })
    expect(w.emitted('cancelled')).toEqual([[done]])
  })

  it('sends a reason without a comment and shows API field errors', async () => {
    cancel.mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'bad', { comment: 'too_long' }))
    const w = mountSheet()
    await w.findAll('[role="radio"]')[0]!.trigger('click')
    expect(w.find('.jt-area__label').text()).toBe('Түсініктеме (міндетті емес)')
    await w.find('.jt-button--destructive').trigger('click')
    await flushPromises()
    expect(cancel).toHaveBeenCalledWith('o1', { reason_code: 'long_wait', expected_status: 'accepted' })
    expect(w.find('.jt-area__message').text()).toBe('Тым ұзын')
    expect(w.emitted('cancelled')).toBeUndefined()
  })

  it('reports an order that can no longer be cancelled', async () => {
    cancel.mockRejectedValueOnce(new ApiError(409, 'invalid_transition', 'x', {}, { from: 'in_progress' }))
    const w = mountSheet()
    await w.findAll('[role="radio"]')[3]!.trigger('click')
    await w.find('.jt-button--destructive').trigger('click')
    await flushPromises()
    expect(w.emitted('failed')).toHaveLength(1)
  })

  it('does not cancel an order a driver took while the sheet was open: it shows why and re-syncs', async () => {
    cancel.mockRejectedValueOnce(new ApiError(409, 'conflict', 'x', {}, { reason: 'order_changed', status: 'accepted' }))
    const w = mountSheet(order({ status: 'searching', driver: null }))
    await w.findAll('[role="radio"]')[3]!.trigger('click')
    await w.find('.jt-button--destructive').trigger('click')
    await flushPromises()
    expect(cancel).toHaveBeenCalledWith('o1', { reason_code: 'changed_plans', expected_status: 'searching' })
    expect(w.emitted('cancelled')).toBeUndefined()
    expect(w.emitted('failed')).toHaveLength(1)
    expect(toast.items.map((t) => t.message).join(' ')).toContain('Осы уақытта тапсырыс өзгерді')
    toast.clear()
  })

  it('"Keep order" closes without cancelling', async () => {
    const w = mountSheet()
    await w.find('.jt-button--plain').trigger('click')
    expect(w.emitted('close')).toHaveLength(1)
    expect(cancel).not.toHaveBeenCalled()
  })
})
