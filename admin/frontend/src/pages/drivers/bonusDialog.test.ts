import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { driversApi, type AdminDriverListItem, type BonusResult } from '../../api/drivers'
import { i18n } from '../../i18n'
import BonusDialog from './BonusDialog.vue'

const city = { id: 'c1', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Embi' } }

const row: AdminDriverListItem = {
  id: 'd1',
  code: 'EMB001',
  status: 'approved',
  first_name: 'Asel',
  last_name: 'Test',
  phone: '+77010000001',
  phone_verified: true,
  city,
  is_online: false,
  last_heartbeat_at: null,
  vehicle: null,
  rating: null,
  rating_count: 0,
  completed_trips: 0,
  wallet_available: 0,
  submitted_at: null,
  approved_at: null,
  created_at: '2026-10-19T08:00:00Z',
}

const result = {
  wallet: { balance: 1000, reserved: 0, available: 1000, currency: 'KZT', min_online_balance: 50, updated_at: '2026-10-19T08:00:00Z' },
  entry: { balance_delta: 1000 },
} as unknown as BonusResult

function input(el: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  el.value = value
  el.dispatchEvent(new Event('input'))
}

function footerButtons(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')]
}

beforeEach(() => {
  i18n.global.locale.value = 'en'
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('BonusDialog', () => {
  it('finds a driver by phone in any format, then credits the bonus with one idempotency key', async () => {
    const list = vi.spyOn(driversApi, 'list').mockResolvedValue({ items: [row], page: 1, page_size: 8, total: 1 })
    const grant = vi.spyOn(driversApi, 'grantBonus').mockResolvedValue(result)

    const wrapper = mount(BonusDialog, { props: { open: false }, global: { plugins: [i18n] }, attachTo: document.body })
    await wrapper.setProps({ open: true })
    input(document.querySelector<HTMLInputElement>('.modal input[type="search"]')!, '8 (701) 000-00-01')
    await vi.waitFor(() => expect(document.querySelector('.bonus__row')).not.toBeNull(), { timeout: 3000 })
    expect(list).toHaveBeenCalledWith({ q: '8 (701) 000-00-01' }, 1, 8, expect.any(AbortSignal))

    document.querySelector<HTMLButtonElement>('.bonus__row')!.click()
    await flushPromises()
    input(document.querySelector<HTMLInputElement>('.modal input[inputmode="numeric"]')!, '1 000')
    input(document.querySelector<HTMLTextAreaElement>('.modal textarea')!, '  Good week ')
    footerButtons().at(-1)!.click()
    await flushPromises()
    expect(document.body.textContent).toContain('Jan Taxi has credited you a bonus of')

    footerButtons().at(-1)!.click()
    await flushPromises()
    expect(grant).toHaveBeenCalledTimes(1)
    const [id, body] = grant.mock.calls[0]!
    expect(id).toBe('d1')
    expect(body).toMatchObject({ amount: 1000, comment: 'Good week' })
    expect(body.idempotency_key).toMatch(/^[0-9a-f-]{36}$/)
    expect(wrapper.emitted('done')).toHaveLength(1)
    expect(wrapper.emitted('update:open')).toEqual([[false]])
    wrapper.unmount()
  })

  it('starts at the amount for a preset driver and omits an empty comment', async () => {
    const grant = vi.spyOn(driversApi, 'grantBonus').mockResolvedValue(result)
    const driver = { id: 'd1', code: 'EMB001', first_name: 'Asel', last_name: 'Test', phone: null, balance: 200 }
    const wrapper = mount(BonusDialog, { props: { open: false, driver }, global: { plugins: [i18n] }, attachTo: document.body })
    await wrapper.setProps({ open: true })
    expect(document.querySelector('.modal input[type="search"]')).toBeNull()

    footerButtons().at(-1)!.click()
    await flushPromises()
    expect(document.body.textContent).toContain('Required.')

    input(document.querySelector<HTMLInputElement>('.modal input[inputmode="numeric"]')!, '500')
    footerButtons().at(-1)!.click()
    await flushPromises()
    expect(document.body.textContent).toContain('700')
    footerButtons().at(-1)!.click()
    await flushPromises()
    expect(grant.mock.calls[0]![1]).not.toHaveProperty('comment')
    wrapper.unmount()
  })
})
