// The wallet never names the commission or its amount (customer request): one
// neutral line asks to keep the balance topped up, "orders paused" is only a
// title, and the journal names order charges neutrally while keeping their
// amounts, so the balance still adds up.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Me } from '../../api/me'
import type { LedgerEntry } from '../../api/types'
import * as api from '../../api/wallet'
import { i18n } from '../../i18n'
import type { Locale } from '../../i18n/core'
import { ROUTE } from '../../router/names'
import { useSessionStore } from '../../stores/session'
import WalletPage from './WalletPage.vue'

vi.mock('../../api/wallet', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../api/wallet')>()
  return { ...real, getWallet: vi.fn(), listTransactions: vi.fn(), listTopUps: vi.fn() }
})

const mocked = vi.mocked(api)

/** Money is formatted with no-break spaces. */
const plain = (s: string) => s.replace(/ /g, ' ')

const ME: Me = {
  id: 'u1',
  telegram_user_id: 1,
  first_name: 'Асан',
  last_name: 'Сериков',
  username: null,
  language: 'kk',
  phone_masked: null,
  phone_verified: true,
  app: 'driver',
  driver: { id: 'd1', code: 'EMB028', status: 'approved', is_online: true, city: null },
}

function entry(id: number, over: Partial<LedgerEntry>): LedgerEntry {
  return {
    id,
    entry_type: 'commission_charge',
    balance_delta: -50,
    reserved_delta: 0,
    balance_after: 20,
    reserved_after: 0,
    currency: 'KZT',
    order_id: 'o1',
    order_number: 1024,
    topup_id: null,
    topup_reference: null,
    description: 'order assignment 1',
    created_at: '2026-11-01T09:00:00Z',
    ...over,
  }
}

/** 20 ₸ available against a fixed 50 ₸ charge per order: orders are paused. */
async function mountWallet(locale: Locale) {
  i18n.global.locale.value = locale
  const session = useSessionStore()
  session.me = ME
  session.extras = { vehicleClasses: [], commission: { mode: 'fixed', fixed_amount: 50, by_area: false }, welcomeBonus: 500 }
  mocked.getWallet.mockResolvedValue({ balance: 20, reserved: 0, available: 20, currency: 'KZT', min_online_balance: 100, updated_at: '' })
  mocked.listTransactions.mockResolvedValue({
    items: [
      entry(3, { entry_type: 'commission_refund', balance_delta: 50, balance_after: 70 }),
      entry(2, {}),
      entry(1, { entry_type: 'commission_reserve', balance_delta: 0, reserved_delta: 50, balance_after: 70, reserved_after: 50 }),
    ],
    next_cursor: null,
  })
  mocked.listTopUps.mockResolvedValue({ items: [], next_cursor: null })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: Object.values(ROUTE).map((name) => ({ path: name === ROUTE.wallet ? '/' : `/${name}`, name, component: { template: '<div />' } })),
  })
  await router.push('/')
  await router.isReady()
  const w = mount(WalletPage, { global: { plugins: [i18n, router] } })
  await flushPromises()
  return w
}

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('WalletPage', () => {
  it('asks to keep the balance topped up and pauses orders without naming the commission or an amount', async () => {
    const w = await mountWallet('kk')
    expect(w.find('.wallet__explainer').text()).toBe('Тапсырыстар келуі үшін балансыңызды толтырып отырыңыз.')
    expect(w.find('.bal__paused').text()).toBe('Тапсырыстар тоқтатылды — балансты толтырыңыз')
    // Neither the 50 ₸ charge nor the 30 ₸ missing to it.
    expect(plain(w.find('.bal').text())).not.toMatch(/\b(50|30) ₸/)
    expect(w.text()).not.toMatch(/комисси|commission/i)
  })

  it.each([
    ['kk', 'Тапсырыстар келуі үшін балансыңызды толтырып отырыңыз.', ['Балансқа қайтарылды', 'Тапсырыс үшін есептен шығарылды', 'Тапсырысқа ұсталды']],
    ['ru', 'Чтобы получать заказы, держите баланс пополненным.', ['Возврат на баланс', 'Списание за заказ', 'Удержано для заказа']],
    ['en', 'Keep your balance topped up to receive orders.', ['Returned to balance', 'Charged for an order', 'Held for an order']],
  ] as const)('%s: names order entries neutrally and keeps their amounts', async (locale, explainer, titles) => {
    const w = await mountWallet(locale)
    expect(w.find('.wallet__explainer').text()).toBe(explainer)
    expect(w.findAll('.lrow__title').map((t) => t.text())).toEqual(titles)
    expect(w.findAll('.lrow__amount > span:first-child').map((a) => plain(a.text()))).toEqual(['+50 ₸', '−50 ₸', '50 ₸'])
    expect(w.text()).not.toMatch(/комисси|commission/i)
  })
})
