// Campaign screens by role: the launch campaigns' on/off switch in the list
// (approvers flip it with a reason and confirmation, sending the version they
// saw; a marketing manager sees it disabled with who may), and the detail page
// hiding approver actions, archiving a launch campaign included, from a
// marketing manager.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { campaignsApi, type Campaign, type CampaignDetail } from '../../api/marketing'
import { toast } from '../../components'
import { i18n } from '../../i18n'
import { ROLE_PERMISSIONS, type Role } from '../../permissions'
import { ApiError } from '../../services/api'
import { useAuthStore } from '../../stores/auth'
import CampaignDetailPage from './CampaignDetailPage.vue'
import CampaignsPage from './CampaignsPage.vue'

function campaign(extra: Partial<Campaign>): Campaign {
  return {
    id: 'c1',
    key: 'first_ride_coupon_400',
    template: 'first_ride_coupon',
    status: 'draft',
    is_system: true,
    version: 1,
    version_id: 'v1',
    title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' },
    description: { kk: '', ru: '', en: '' },
    rules: { amount: 400, validity_hours: 72, min_order_fare: 400, categories: ['taxi'] },
    starts_at: null,
    ends_at: null,
    budget_limit: 100000,
    redemptions_frozen: false,
    priority: 10,
    exclusivity_group: 'launch_2026',
    scheduled_at: null,
    active_since: null,
    activated_at: null,
    paused_at: null,
    ended_at: null,
    archived_at: null,
    created_by: null,
    approved_by: null,
    created_at: '2026-11-01T08:00:00Z',
    updated_at: '2026-11-01T08:00:00Z',
    usage: { committed: 800, spent: 400, remaining: 98800, coupons: { issued: 2, redeemed: 1 }, grants: {} },
    ...extra,
  }
}

const coupon = campaign({})
const referral = campaign({
  id: 'c2',
  key: 'referral_300_once',
  template: 'referral_first_ride',
  status: 'active',
  title: { kk: 'Досыңды шақыр', ru: 'Пригласи друга', en: 'Invite a friend' },
  rules: { reward_amount: 300, categories: ['taxi'], attribution_window_minutes: 60, unlock: 'coupon_redeemed', min_ride_seconds: 60 },
})
const other = campaign({ id: 'c3', key: 'driver_week', template: 'driver_milestone_bonus', is_system: false, title: { kk: 'Апта', ru: 'Неделя', en: 'Week' } })

function signIn(role: Role): void {
  const auth = useAuthStore()
  auth.admin = { id: 'a1', email: 'ops@jantaxi.kz', full_name: 'Ops', role, permissions: [...ROLE_PERMISSIONS[role]] }
}

async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/campaigns', name: 'campaigns', component: CampaignsPage },
      { path: '/campaigns/:id', name: 'campaign-detail', component: CampaignDetailPage, props: true },
    ],
  })
  await router.push(path)
  await router.isReady()
  const w = mount({ template: '<RouterView />' }, { global: { plugins: [i18n, router] }, attachTo: document.body })
  await flushPromises()
  return w
}

function switches(): HTMLInputElement[] {
  return [...document.querySelectorAll<HTMLInputElement>('input[role="switch"]')]
}

function buttons(): string[] {
  return [...document.querySelectorAll<HTMLButtonElement>('.detail-head__actions button')].map((b) => b.textContent?.trim() ?? '')
}

function clickAction(label: string): void {
  ;[...document.querySelectorAll<HTMLButtonElement>('.detail-head__actions button')].find((b) => b.textContent?.trim() === label)!.click()
}

async function confirmWithReason(reason: string): Promise<void> {
  const area = document.querySelector<HTMLTextAreaElement>('.modal textarea')!
  area.value = reason
  area.dispatchEvent(new Event('input'))
  await flushPromises()
  ;[...document.querySelectorAll<HTMLButtonElement>('.modal__foot button')].at(-1)!.click()
  await flushPromises()
}

const NBSP = '\u00a0'
const VERSION_CONFLICT = new ApiError(409, 'version_conflict', 'the campaign changed since it was reviewed', {}, { current_version: 2 })

beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'en'
  vi.spyOn(campaignsApi, 'list').mockResolvedValue({ items: [coupon, referral, other], page: 1, page_size: 25, total: 3 })
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('CampaignsPage', () => {
  it('shows the launch switch disabled to a marketing manager, with who may flip it', async () => {
    signIn('marketing_manager')
    const w = await mountAt('/campaigns')
    expect(switches()).toHaveLength(2)
    expect(switches().every((s) => s.disabled)).toBe(true)
    expect(switches()[0]!.checked).toBe(false)
    expect(switches()[1]!.checked).toBe(true)
    expect(document.body.textContent).toContain('Only an approver (marketing.approve: super admin or finance manager) can switch the launch campaigns on and off or archive them.')
    expect(document.body.textContent).toContain('New campaign')
    w.unmount()
  })

  it('lets an approver switch a launch campaign on with a reason and confirmation', async () => {
    signIn('finance_manager')
    const transition = vi.spyOn(campaignsApi, 'transition').mockResolvedValue({ ...coupon, status: 'active', versions: [] } as CampaignDetail)
    const w = await mountAt('/campaigns')
    expect(document.body.textContent).not.toContain('New campaign')
    const first = switches()[0]!
    expect(first.disabled).toBe(false)
    first.checked = true
    first.dispatchEvent(new Event('change'))
    await flushPromises()

    expect(document.querySelector('.modal')?.textContent).toContain('Switch on «First ride»')
    expect(document.querySelector('.modal')?.textContent).toContain('passengers who finish their first ride start getting the coupon.')
    // The approver sees the rules being switched on, and the request carries their version.
    expect(document.querySelector('.modal')?.textContent).toContain(`Rules (version 1): 400${NBSP}₸ coupon, valid 72 h, minimum fare 400${NBSP}₸, taxi only.`)
    await confirmWithReason('Launch agreed with the customer')

    expect(transition).toHaveBeenCalledWith('c1', { action: 'activate', reason: 'Launch agreed with the customer', confirm: true, expected_version: 1 })
    expect(campaignsApi.list).toHaveBeenCalledTimes(2)
    w.unmount()
  })

  it('reloads instead of switching on a version saved after the list was opened', async () => {
    signIn('finance_manager')
    const transition = vi.spyOn(campaignsApi, 'transition').mockRejectedValue(VERSION_CONFLICT)
    const toastError = vi.spyOn(toast, 'error')
    const w = await mountAt('/campaigns')
    const first = switches()[0]!
    first.checked = true
    first.dispatchEvent(new Event('change'))
    await flushPromises()
    await confirmWithReason('Launch agreed with the customer')

    expect(transition).toHaveBeenCalledTimes(1)
    expect(toastError).toHaveBeenCalledWith('Meanwhile a new version of the campaign was saved. Review the new rules and try again.')
    expect(document.querySelector('.modal')).toBeNull()
    expect(campaignsApi.list).toHaveBeenCalledTimes(2)
    expect(switches()[0]!.checked).toBe(false)
    w.unmount()
  })

  it('pauses an active launch campaign when switched off', async () => {
    signIn('super_admin')
    const transition = vi.spyOn(campaignsApi, 'transition').mockResolvedValue({ ...referral, status: 'paused', versions: [] } as CampaignDetail)
    const w = await mountAt('/campaigns')
    const second = switches()[1]!
    second.checked = false
    second.dispatchEvent(new Event('change'))
    await flushPromises()
    await confirmWithReason('Fraud check')
    // Pausing stops the money, so it does not depend on the version.
    expect(transition).toHaveBeenCalledWith('c2', { action: 'pause', reason: 'Fraud check', confirm: true })
    w.unmount()
  })
})

describe('CampaignDetailPage', () => {
  beforeEach(() => {
    vi.spyOn(campaignsApi, 'get').mockResolvedValue({ ...coupon, versions: [] })
    vi.spyOn(campaignsApi, 'preview').mockResolvedValue({
      audience: 120,
      per_person: 400,
      max_liability: 48000,
      budget_limit: 100000,
      usage: coupon.usage!,
      warnings: ['fares_below_coupon_minimum'],
    })
  })

  it('hides approver actions from a marketing manager', async () => {
    signIn('marketing_manager')
    const w = await mountAt('/campaigns/c1')
    expect(document.body.textContent).toContain('400 ₸ coupon, valid 72 h, minimum fare 400 ₸, taxi only')
    expect(document.body.textContent).toContain('the coupon will not apply to cheap rides')
    // Archiving cannot be undone: a launch campaign is archived by an approver only.
    expect(buttons()).toEqual(['Edit rules', 'Duplicate'])
    expect(document.body.textContent).toContain('is for approvers only')
    w.unmount()
  })

  it('offers switching on, scheduling and the budget to an approver', async () => {
    signIn('finance_manager')
    const w = await mountAt('/campaigns/c1')
    expect(buttons()).toEqual(['Switch on', 'Schedule', 'Freeze coupons', 'Archive'])
    expect([...document.querySelectorAll('.card button')].some((b) => b.textContent?.trim() === 'Change budget')).toBe(true)
    w.unmount()
  })

  it('sends the version on screen with an approver action and reloads when it is outdated', async () => {
    signIn('finance_manager')
    const transition = vi.spyOn(campaignsApi, 'transition').mockRejectedValueOnce(VERSION_CONFLICT).mockResolvedValueOnce({ ...coupon, status: 'archived', versions: [] })
    const toastError = vi.spyOn(toast, 'error')
    const w = await mountAt('/campaigns/c1')
    expect(campaignsApi.get).toHaveBeenCalledTimes(1)

    clickAction('Switch on')
    await flushPromises()
    await confirmWithReason('Launch agreed with the customer')
    expect(transition).toHaveBeenLastCalledWith('c1', { action: 'activate', reason: 'Launch agreed with the customer', confirm: true, expected_version: 1 })
    expect(toastError).toHaveBeenCalledWith('Meanwhile a new version of the campaign was saved. Review the new rules and try again.')
    expect(document.querySelector('.modal')).toBeNull()
    expect(campaignsApi.get).toHaveBeenCalledTimes(2)

    // Archiving is not an approver action in the API, so it carries no version.
    clickAction('Archive')
    await flushPromises()
    await confirmWithReason('Replaced by a new campaign')
    expect(transition).toHaveBeenLastCalledWith('c1', { action: 'archive', reason: 'Replaced by a new campaign', confirm: true })
    w.unmount()
  })
})
