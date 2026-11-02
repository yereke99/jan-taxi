// Broadcast page: the test card only while a draft or scheduled, the test
// send's refusals in words (an id off the test recipients list, an
// unsubscribed person, the broadcasts switch off), and why a scheduled
// broadcast the worker refused for approval was not sent.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { broadcastsApi, type BroadcastDetail } from '../../api/marketing'
import { i18n } from '../../i18n'
import { ROLE_PERMISSIONS, type Role } from '../../permissions'
import { ApiError } from '../../services/api'
import { useAuthStore } from '../../stores/auth'
import BroadcastDetailPage from './BroadcastDetailPage.vue'

function broadcast(extra: Partial<BroadcastDetail> = {}): BroadcastDetail {
  return {
    id: 'b1',
    campaign_id: null,
    title: 'Launch news',
    audience: 'passengers',
    segment_id: null,
    filters: {},
    content: { kk: 'Жаңа акция', ru: 'Новая акция', en: 'New promotion' },
    button: null,
    status: 'draft',
    scheduled_at: null,
    started_at: null,
    completed_at: null,
    cancelled_at: null,
    recipient_count: null,
    created_by: null,
    approved_by: null,
    approval_note: null,
    version: 1,
    created_at: '2026-11-01T08:00:00Z',
    updated_at: '2026-11-01T08:00:00Z',
    stats: {},
    estimate: { matching: 40, reachable: 38, suppressed: 2 },
    approval_threshold: 100,
    ...extra,
  }
}

function signIn(role: Role): void {
  useAuthStore().admin = { id: 'a1', email: 'ops@jantaxi.kz', full_name: 'Ops', role, permissions: [...ROLE_PERMISSIONS[role]] }
}

async function mountPage(b: BroadcastDetail) {
  vi.spyOn(broadcastsApi, 'get').mockResolvedValue(b)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/broadcasts', name: 'broadcasts', component: { template: '<div />' } },
      { path: '/broadcasts/:id', name: 'broadcast-detail', component: BroadcastDetailPage, props: true },
    ],
  })
  await router.push('/broadcasts/b1')
  await router.isReady()
  const w = mount({ template: '<RouterView />' }, { global: { plugins: [i18n, router] }, attachTo: document.body })
  await flushPromises()
  return w
}

async function sendTest(id: string): Promise<void> {
  const field = document.querySelector<HTMLInputElement>('form.test input')!
  field.value = id
  field.dispatchEvent(new Event('input'))
  document.querySelector('form.test')!.dispatchEvent(new Event('submit'))
  await flushPromises()
}

beforeEach(() => {
  setActivePinia(createPinia())
  i18n.global.locale.value = 'en'
  signIn('marketing_manager')
  vi.spyOn(broadcastsApi, 'recipients').mockResolvedValue({ items: [], page: 1, page_size: 25, total: 0 })
})

afterEach(() => {
  i18n.global.locale.value = 'kk'
  document.body.innerHTML = ''
})

describe('BroadcastDetailPage', () => {
  it('offers a test send only while the broadcast is a draft or scheduled', async () => {
    for (const [status, shown] of [
      ['draft', true],
      ['scheduled', true],
      ['sending', false],
      ['completed', false],
      ['cancelled', false],
    ] as const) {
      const w = await mountPage(broadcast({ status }))
      expect(document.querySelector('form.test') !== null).toBe(shown)
      w.unmount()
      document.body.innerHTML = ''
    }
  })

  it('points to the test recipients list and explains every refusal of a test send', async () => {
    const test = vi
      .spyOn(broadcastsApi, 'test')
      .mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'invalid', { telegram_user_id: 'not_test_recipient' }))
      .mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'invalid', { telegram_user_id: 'suppressed' }))
      .mockRejectedValueOnce(new ApiError(409, 'broadcasts_disabled', 'marketing broadcasts are switched off'))
    const w = await mountPage(broadcast())
    expect(document.body.textContent).toContain('The ID must be on the test recipients list on the Marketing → Safety page')

    await sendTest('123456789')
    expect(test).toHaveBeenLastCalledWith('b1', 123456789)
    expect(document.querySelector('form.test')?.textContent).toContain('This ID is not on the test recipients list (Marketing → Safety).')

    await sendTest('123456789')
    expect(document.querySelector('form.test')?.textContent).toContain('This person opted out of marketing, blocked the bot or is blocked.')

    await sendTest('123456789')
    expect(document.querySelector('form.test')?.textContent).toContain('Marketing broadcasts are switched off by the emergency switch (Marketing → Safety).')
    expect(document.body.textContent).not.toContain('Something went wrong.')
    expect(document.body.textContent).not.toContain('Invalid value.')
    w.unmount()
  })

  it('says why a scheduled broadcast refused for approval was not sent', async () => {
    const w = await mountPage(
      broadcast({ status: 'failed', scheduled_at: '2026-11-02T05:00:00Z', completed_at: '2026-11-02T05:00:03Z', approval_note: 'approval_required', estimate: null }),
    )
    expect(document.body.textContent).toContain('The broadcast was not sent: by the send time its audience had grown above the approval threshold')
    expect(document.body.textContent).not.toContain('approval_required')
    w.unmount()
  })

  it('keeps the note an operator wrote when starting a broadcast', async () => {
    const w = await mountPage(broadcast({ status: 'completed', recipient_count: 38, approval_note: 'Agreed with the owner', estimate: null }))
    expect(document.body.textContent).toContain('Approval note')
    expect(document.body.textContent).toContain('Agreed with the owner')
    expect(document.body.textContent).not.toContain('The broadcast was not sent')
    w.unmount()
  })
})
