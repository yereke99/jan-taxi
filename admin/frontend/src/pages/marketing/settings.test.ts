// Marketing safety settings: an approver's save always carries a reason; other
// roles see the switches read-only. The Telegram ids that may get test
// broadcasts are edited here too, as a comma-separated list.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { SettingItem } from '../../api/config'
import { marketingApi } from '../../api/marketing'
import { i18n } from '../../i18n'
import { ROLE_PERMISSIONS, type Role } from '../../permissions'
import { useAuthStore } from '../../stores/auth'
import MarketingSettingsPage from './MarketingSettingsPage.vue'

function item(key: string, type: SettingItem['type'], value: unknown, constraints: SettingItem['constraints'] = {}): SettingItem {
  return {
    key,
    type,
    value,
    default: value,
    source: 'default',
    has_env_default: false,
    overrides_env: false,
    description: { kk: '', ru: '', en: '' },
    constraints,
    updated_at: null,
    updated_by: null,
  }
}

const ITEMS: SettingItem[] = [
  item('marketing.awards_enabled', 'bool', true),
  item('marketing.redemptions_enabled', 'bool', true),
  item('marketing.broadcasts_enabled', 'bool', true),
  item('marketing.broadcast_rate_per_s', 'int', 4, { min: 1, max: 20 }),
  item('marketing.broadcast_approval_threshold', 'int', 100, { min: 1, max: 10_000_000 }),
  item('marketing.test_recipients', 'int_list', [], { min: 1, max: 9_999_999_999_999, max_items: 20 }),
]

function signIn(role: Role): void {
  useAuthStore().admin = { id: 'a1', email: 'ops@jantaxi.kz', full_name: 'Ops', role, permissions: [...ROLE_PERMISSIONS[role]] }
}

async function mountPage() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/marketing-settings', name: 'marketing-settings', component: MarketingSettingsPage }] })
  await router.push('/marketing-settings')
  await router.isReady()
  const w = mount({ template: '<RouterView />' }, { global: { plugins: [i18n, router] }, attachTo: document.body })
  await flushPromises()
  return w
}

function input(el: HTMLInputElement | HTMLTextAreaElement, value: string): void {
  el.value = value
  el.dispatchEvent(new Event('input'))
}

beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'en'
  vi.spyOn(marketingApi, 'settings').mockResolvedValue({ items: ITEMS })
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('MarketingSettingsPage', () => {
  it('saves the changed switches and numbers with the reason', async () => {
    signIn('finance_manager')
    const update = vi.spyOn(marketingApi, 'updateSettings').mockResolvedValue({ items: ITEMS })
    const w = await mountPage()

    const awards = document.querySelector<HTMLInputElement>('input[id="marketing.awards_enabled"]')!
    expect(awards.disabled).toBe(false)
    awards.checked = false
    awards.dispatchEvent(new Event('change'))
    input(document.querySelector<HTMLInputElement>('input[id="marketing.broadcast_rate_per_s"]')!, '2')
    await flushPromises()
    expect(document.body.textContent).toContain('Unsaved changes: 2')

    ;[...document.querySelectorAll<HTMLButtonElement>('.savebar button')].at(-1)!.click()
    await flushPromises()
    expect(document.querySelector('.modal')?.textContent).toContain('Issue coupons and bonuses: Off')
    // The reason is required before anything is sent.
    ;[...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')].at(-1)!.click()
    await flushPromises()
    expect(update).not.toHaveBeenCalled()

    input(document.querySelector<HTMLTextAreaElement>('.modal textarea')!, 'Fraud wave in Embi')
    await flushPromises()
    ;[...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')].at(-1)!.click()
    await flushPromises()
    expect(update).toHaveBeenCalledWith({ 'marketing.awards_enabled': false, 'marketing.broadcast_rate_per_s': 2 }, 'Fraud wave in Embi')
    w.unmount()
  })

  it('lists every safety key, the test recipients included', async () => {
    signIn('finance_manager')
    const w = await mountPage()
    for (const i of ITEMS) expect(document.querySelector(`[id="${i.key}"]`)).not.toBeNull()
    const label = document.querySelector('label[for="marketing.test_recipients"]')
    expect(label?.textContent).toBe('Test recipients, Telegram IDs')
    expect(document.body.textContent).toContain('Comma-separated, up to 20 IDs.')
    // The id range is not shown as an amount range.
    expect(document.body.textContent).not.toContain('9999999999999')
    w.unmount()
  })

  it('saves the Telegram ids of the test recipients with the reason', async () => {
    signIn('finance_manager')
    const update = vi.spyOn(marketingApi, 'updateSettings').mockResolvedValue({ items: ITEMS })
    const w = await mountPage()
    const ids = document.querySelector<HTMLInputElement>('input[id="marketing.test_recipients"]')!
    expect(ids.disabled).toBe(false)

    // A repeated id is refused before the reason is asked for.
    input(ids, '123456789, 123456789')
    await flushPromises()
    ;[...document.querySelectorAll<HTMLButtonElement>('.savebar button')].at(-1)!.click()
    await flushPromises()
    expect(document.querySelector('.modal')).toBeNull()
    expect(document.body.textContent).toContain('Duplicate.')

    input(ids, '987654321, 123456789')
    await flushPromises()
    ;[...document.querySelectorAll<HTMLButtonElement>('.savebar button')].at(-1)!.click()
    await flushPromises()
    expect(document.querySelector('.modal')?.textContent).toContain('Test recipients, Telegram IDs: 987654321, 123456789')
    input(document.querySelector<HTMLTextAreaElement>('.modal textarea')!, 'Staff phones for tests')
    await flushPromises()
    ;[...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')].at(-1)!.click()
    await flushPromises()
    expect(update).toHaveBeenCalledWith({ 'marketing.test_recipients': [987654321, 123456789] }, 'Staff phones for tests')
    w.unmount()
  })

  it('refuses values outside the range before asking for a reason', async () => {
    signIn('super_admin')
    const update = vi.spyOn(marketingApi, 'updateSettings')
    const w = await mountPage()
    input(document.querySelector<HTMLInputElement>('input[id="marketing.broadcast_rate_per_s"]')!, '50')
    await flushPromises()
    ;[...document.querySelectorAll<HTMLButtonElement>('.savebar button')].at(-1)!.click()
    await flushPromises()
    expect(document.querySelector('.modal')).toBeNull()
    expect(document.body.textContent).toContain('Out of the allowed range.')
    expect(update).not.toHaveBeenCalled()
    w.unmount()
  })

  it('shows the switches read-only to a marketing manager', async () => {
    signIn('marketing_manager')
    const w = await mountPage()
    const toggles = [...document.querySelectorAll<HTMLInputElement>('input[role="switch"]')]
    expect(toggles).toHaveLength(3)
    expect(toggles.every((x) => x.disabled && x.checked)).toBe(true)
    expect(document.querySelector<HTMLInputElement>('input[id="marketing.test_recipients"]')!.disabled).toBe(true)
    expect(document.body.textContent).toContain('Only an approver (super admin or finance manager) can change these settings.')
    w.unmount()
  })
})
