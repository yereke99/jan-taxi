// Wallet journal: every entry type has a label in each language (the filter
// lists them all), including the promotion compensation Jan Taxi credits to a
// driver and its reversal (§23.6).

import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { LEDGER_TYPES, type AdminLedgerEntry } from '../../api/finance'
import { i18n } from '../../i18n'
import { en, kk, ru } from '../../i18n/messages'
import LedgerTable from './LedgerTable.vue'

function entry(extra: Partial<AdminLedgerEntry>): AdminLedgerEntry {
  return {
    id: 1,
    entry_type: 'promo_compensation',
    balance_delta: 400,
    reserved_delta: 0,
    balance_after: 1400,
    reserved_after: 0,
    currency: 'KZT',
    order_id: 'o1',
    order_number: 1201,
    topup_id: null,
    topup_reference: null,
    description: 'promotion compensation',
    created_at: '2026-11-02T08:00:00Z',
    wallet_id: 'w1',
    driver: { id: 'd1', code: 'EMB001', first_name: 'Asel', last_name: 'Test', phone: null },
    admin: null,
    reservation_id: null,
    idempotency_key: 'promo:a1',
    ...extra,
  }
}

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('ledger entry types', () => {
  it('includes the promotion compensation and its reversal, labelled in every language', () => {
    expect(LEDGER_TYPES).toContain('promo_compensation')
    expect(LEDGER_TYPES).toContain('promo_compensation_reversal')
    for (const tree of [kk, ru, en]) {
      for (const type of LEDGER_TYPES) expect(tree.ledger_type[type], type).toBeTruthy()
    }
    expect(en.ledger_type.promo_compensation).toBe('Promotion compensation')
    expect(ru.ledger_type.promo_compensation_reversal).toBe('Сторно компенсации по акции')
  })

  it('shows the compensation row in the operator’s language', async () => {
    i18n.global.locale.value = 'en'
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }, { path: '/orders/:id', name: 'order-detail', component: { template: '<div />' } }, { path: '/drivers/:id', name: 'driver-detail', component: { template: '<div />' } }] })
    await router.push('/')
    const rows = [entry({}), entry({ id: 2, entry_type: 'promo_compensation_reversal', balance_delta: -400, balance_after: 1000, description: 'Ride disputed' })]
    const w = mount(LedgerTable, { props: { rows }, global: { plugins: [i18n, router] } })
    const text = w.text()
    expect(text).toContain('Promotion compensation')
    expect(text).toContain('Compensation for the passenger’s coupon or bonus')
    expect(text).toContain('Promotion compensation reversal')
    expect(text).toContain('Ride disputed')
    expect(text).toContain('#1201')
    w.unmount()
  })
})
