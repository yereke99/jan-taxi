import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ordersApi, type AdminOrder, type OrderLedgerRow } from '../../api/orders'
import { i18n } from '../../i18n'
import CancelOrderDialog from './CancelOrderDialog.vue'

function charge(delta: number): OrderLedgerRow {
  return {
    id: 1,
    driver_id: 'd1',
    entry_type: 'commission_charge',
    balance_delta: delta,
    reserved_delta: 0,
    balance_after: 0,
    reserved_after: 0,
    currency: 'KZT',
    order_id: 'o1',
    order_number: 7,
    topup_id: null,
    topup_reference: null,
    description: 'order assignment 1',
    created_at: '2026-10-19T08:00:00Z',
  }
}

function order(ledger: OrderLedgerRow[]): AdminOrder {
  return { id: 'o1', number: 7, status: 'accepted', driver: { id: 'd1', code: 'EMB001', first_name: '', last_name: '', phone: null }, ledger } as unknown as AdminOrder
}

async function open(o: AdminOrder) {
  const wrapper = mount(CancelOrderDialog, { props: { open: false, order: o }, global: { plugins: [i18n] }, attachTo: document.body })
  await wrapper.setProps({ open: true })
  const area = document.querySelector<HTMLTextAreaElement>('.modal textarea')!
  area.value = 'Passenger asked by phone'
  area.dispatchEvent(new Event('input'))
  await flushPromises()
  return wrapper
}

function submit(): void {
  const buttons = [...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')]
  buttons.at(-1)!.click()
}

beforeEach(() => {
  i18n.global.locale.value = 'en'
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('CancelOrderDialog', () => {
  it('keeps the commission unless the refund is ticked', async () => {
    const cancel = vi.spyOn(ordersApi, 'cancel').mockResolvedValue(order([]))
    const wrapper = await open(order([charge(-50)]))
    expect(document.body.textContent).toContain('Refund the commission to EMB001')
    submit()
    await flushPromises()
    expect(cancel).toHaveBeenCalledWith('o1', 'Passenger asked by phone', false)
    wrapper.unmount()
  })

  it('refunds the charged commission when ticked', async () => {
    const cancel = vi.spyOn(ordersApi, 'cancel').mockResolvedValue(order([]))
    const wrapper = await open(order([charge(-50)]))
    const toggle = document.querySelector<HTMLInputElement>('.modal input[role="switch"]')!
    toggle.checked = true
    toggle.dispatchEvent(new Event('change'))
    await flushPromises()
    submit()
    await flushPromises()
    expect(cancel).toHaveBeenCalledWith('o1', 'Passenger asked by phone', true)
    expect(wrapper.emitted('done')).toHaveLength(1)
    wrapper.unmount()
  })

  it('offers no refund when nothing was charged', async () => {
    vi.spyOn(ordersApi, 'cancel').mockResolvedValue(order([]))
    const wrapper = await open(order([]))
    expect(document.querySelector<HTMLInputElement>('.modal input[role="switch"]')!.disabled).toBe(true)
    expect(document.body.textContent).toContain('No commission was charged')
    wrapper.unmount()
  })
})
