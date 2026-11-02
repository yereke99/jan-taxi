// Codes the server writes for finance decisions read as words in every
// language: why a promotion waits for review, why a referral was held or
// rejected, and why the system voided a coupon. An operator's own void reason
// is shown as typed.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { REFERRAL_REASONS, REVIEW_REASONS, SYSTEM_VOID_REASONS, type ApplicationRow, type Coupon } from '../../api/marketing'
import { i18n } from '../../i18n'
import { ROLE_PERMISSIONS } from '../../permissions'
import { useAuthStore } from '../../stores/auth'
import CouponsTable from './CouponsTable.vue'
import PromotionsTable from './PromotionsTable.vue'

function coupon(id: string, voidReason: string): Coupon {
  return {
    id,
    campaign_id: 'c1',
    campaign_key: 'first_ride_coupon_400',
    version: 1,
    user_id: '7b1c2d3e-0000-4000-8000-000000000001',
    amount: 400,
    min_order_fare: 400,
    categories: ['taxi'],
    status: 'voided',
    issued_at: '2026-11-01T08:00:00Z',
    expires_at: '2026-11-04T08:00:00Z',
    source_order_number: 1201,
    reserved_order_id: null,
    redeemed_order_number: null,
    redeemed_at: null,
    closed_at: '2026-11-02T08:00:00Z',
    void_reason: voidReason,
  }
}

const promotion: ApplicationRow = {
  id: 'a1',
  order_id: 'o2',
  order_number: 1305,
  order_status: 'completed',
  user_id: '7b1c2d3e-0000-4000-8000-000000000001',
  kind: 'coupon',
  campaign_key: 'first_ride_coupon_400',
  gross_fare: 1500,
  discount: 400,
  status: 'settled',
  driver_code: 'JT-0042',
  compensation: 400,
  review_reason: 'coupon_source_cancelled',
  reserved_at: '2026-11-01T09:00:00Z',
  settled_at: '2026-11-01T09:20:00Z',
  released_at: null,
  reversed_at: null,
}

/** The routes the tables link to (the passenger's page and the orders). */
async function links() {
  const stub = { template: '<div />' }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub },
      { path: '/marketing-users/:id', name: 'marketing-user', component: stub },
      { path: '/orders', name: 'orders', component: stub },
      { path: '/orders/:id', name: 'order-detail', component: stub },
    ],
  })
  await router.push('/')
  await router.isReady()
  return { plugins: [i18n, router] }
}

beforeEach(() => {
  setActivePinia(createPinia())
  useAuthStore().admin = { id: 'a1', email: 'audit@jantaxi.kz', full_name: 'Audit', role: 'auditor', permissions: [...ROLE_PERMISSIONS.auditor] }
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('reason codes', () => {
  it('have words in Kazakh, Russian and English', () => {
    const keys = [
      ...REVIEW_REASONS.map((c) => `marketing.review_reason_${c}`),
      ...REFERRAL_REASONS.map((c) => `marketing.referral_reason_${c}`),
      ...SYSTEM_VOID_REASONS.map((c) => `marketing.void_reason_${c}`),
    ]
    const missing = (['kk', 'ru', 'en'] as const).flatMap((locale) => keys.filter((k) => !i18n.global.te(k, locale)).map((k) => `${locale}: ${k}`))
    expect(missing).toEqual([])
  })

  it('translate the system’s void reason and keep the operator’s as typed', async () => {
    i18n.global.locale.value = 'ru'
    const w = mount(CouponsTable, { props: { rows: [coupon('k1', 'qualifying_ride_cancelled'), coupon('k2', 'Мошенничество с телефонами')] }, global: await links(), attachTo: document.body })
    await flushPromises()
    expect(document.body.textContent).toContain('Поездка отменена по спору')
    expect(document.body.textContent).toContain('Мошенничество с телефонами')
    expect(document.body.textContent).not.toContain('qualifying_ride_cancelled')
    w.unmount()
  })

  it('name a used coupon whose own ride was cancelled in a dispute', async () => {
    const w = mount(PromotionsTable, { props: { rows: [promotion] }, global: await links(), attachTo: document.body })
    await flushPromises()
    expect(document.body.textContent).toContain('Купон алынған сапар дау нәтижесінде бас тартылды')
    expect(document.body.textContent).not.toContain('coupon_source_cancelled')
    w.unmount()
  })
})
