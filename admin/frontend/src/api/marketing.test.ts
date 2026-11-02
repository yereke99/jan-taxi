// Request shapes of the marketing API: approver actions carry reason,
// confirm: true and the reviewed version, money corrections their idempotency
// key, and empty filters never reach the query string.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setCsrfToken } from '../services/csrf'
import { bonusApi, broadcastsApi, campaignsApi, couponsApi, marketingApi, promotionsApi, referralsApi, segmentsApi } from './marketing'

type FetchArgs = [input: RequestInfo | URL, init?: RequestInit]

let fetchMock: ReturnType<typeof vi.fn<(...args: FetchArgs) => Promise<Response>>>

function last(): { method: string; url: string; body: unknown } {
  const call = fetchMock.mock.calls.at(-1)!
  const init = call[1] ?? {}
  return { method: String(init.method), url: String(call[0]), body: init.body ? JSON.parse(String(init.body)) : undefined }
}

beforeEach(() => {
  setCsrfToken('csrf-1')
  fetchMock = vi.fn<(...args: FetchArgs) => Promise<Response>>(async () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('marketing API', () => {
  it('sends a campaign transition with the reason and confirm: true', async () => {
    await campaignsApi.transition('c 1', { action: 'activate', reason: 'Launch agreed', confirm: true })
    expect(last()).toEqual({
      method: 'POST',
      url: '/api/v1/admin/marketing/campaigns/c%201/transition',
      body: { action: 'activate', reason: 'Launch agreed', confirm: true },
    })
    const headers = fetchMock.mock.calls.at(-1)![1]!.headers as Record<string, string>
    expect(headers['X-CSRF-Token']).toBe('csrf-1')

    await campaignsApi.transition('c1', { action: 'schedule', scheduled_at: '2026-11-03T05:00:00.000Z', reason: 'Monday', confirm: true })
    expect(last().body).toEqual({ action: 'schedule', scheduled_at: '2026-11-03T05:00:00.000Z', reason: 'Monday', confirm: true })

    await campaignsApi.transition('c1', { action: 'resume', reason: 'Fraud checked', confirm: true, expected_version: 2 })
    expect(last().body).toEqual({ action: 'resume', reason: 'Fraud checked', confirm: true, expected_version: 2 })
  })

  it('sets a budget limit or removes it, always with a reason and confirmation', async () => {
    await campaignsApi.budget('c1', { budget_limit: 150000, reason: 'More coupons', confirm: true })
    expect(last()).toEqual({ method: 'PATCH', url: '/api/v1/admin/marketing/campaigns/c1/budget', body: { budget_limit: 150000, reason: 'More coupons', confirm: true } })
    await campaignsApi.budget('c1', { unlimited: true, reason: 'No cap', confirm: true })
    expect(last().body).toEqual({ unlimited: true, reason: 'No cap', confirm: true })
  })

  it('corrects a bonus balance with a signed amount and the idempotency key', async () => {
    await bonusApi.adjust('u1', { amount: -200, reason: 'Duplicate credit', idempotency_key: '5f0c6a8e-2b1d-4c3e-9f7a-1b2c3d4e5f60', confirm: true })
    expect(last()).toEqual({
      method: 'POST',
      url: '/api/v1/admin/marketing/users/u1/bonus-adjustments',
      body: { amount: -200, reason: 'Duplicate credit', idempotency_key: '5f0c6a8e-2b1d-4c3e-9f7a-1b2c3d4e5f60', confirm: true },
    })
  })

  it('starts and stops broadcasts through the transition endpoint', async () => {
    await broadcastsApi.transition('b1', { action: 'send_now', confirm: true })
    expect(last()).toEqual({ method: 'POST', url: '/api/v1/admin/marketing/broadcasts/b1/transition', body: { action: 'send_now', confirm: true } })
    await broadcastsApi.transition('b1', { action: 'schedule', scheduled_at: '2026-11-03T05:00:00.000Z', reason: 'Morning', confirm: true })
    expect(last().body).toEqual({ action: 'schedule', scheduled_at: '2026-11-03T05:00:00.000Z', reason: 'Morning', confirm: true })
    await broadcastsApi.transition('b1', { action: 'cancel' })
    expect(last().body).toEqual({ action: 'cancel' })
    await broadcastsApi.test('b1', 123456789)
    expect(last()).toEqual({ method: 'POST', url: '/api/v1/admin/marketing/broadcasts/b1/test', body: { telegram_user_id: 123456789 } })
  })

  it('saves the emergency switches together with the reason', async () => {
    await marketingApi.updateSettings({ 'marketing.awards_enabled': false, 'marketing.broadcast_rate_per_s': 2 }, 'Fraud wave')
    expect(last()).toEqual({
      method: 'PATCH',
      url: '/api/v1/admin/marketing/settings',
      body: { 'marketing.awards_enabled': false, 'marketing.broadcast_rate_per_s': 2, reason: 'Fraud wave' },
    })
    await marketingApi.updateSettings({ 'marketing.test_recipients': [123456789] }, 'Staff for tests')
    expect(last().body).toEqual({ 'marketing.test_recipients': [123456789], reason: 'Staff for tests' })
  })

  it('confirms reversals and sends reasons for voids and referral decisions', async () => {
    await promotionsApi.reverse('a1', 'Ride disputed')
    expect(last()).toEqual({ method: 'POST', url: '/api/v1/admin/marketing/applications/a1/reverse', body: { reason: 'Ride disputed', confirm: true } })
    await couponsApi.void('k1', 'Fraud')
    expect(last()).toEqual({ method: 'POST', url: '/api/v1/admin/marketing/coupons/k1/void', body: { reason: 'Fraud' } })
    await referralsApi.review('r1', 'approve', 'Checked the ride')
    expect(last()).toEqual({ method: 'POST', url: '/api/v1/admin/marketing/referrals/r1/review', body: { decision: 'approve', reason: 'Checked the ride' } })
  })

  it('leaves empty filters out of list queries', async () => {
    await campaignsApi.list({ status: '', template: 'first_ride_coupon', q: '  launch ' }, 2, 25)
    expect(last().url).toBe('/api/v1/admin/marketing/campaigns?template=first_ride_coupon&q=launch&page=2&page_size=25')
    await promotionsApi.list({ status: 'settled', review: '' }, 1, 25)
    expect(last().url).toBe('/api/v1/admin/marketing/applications?status=settled&page=1&page_size=25')
    await promotionsApi.list({ review: 'true' }, 1, 25)
    expect(last().url).toBe('/api/v1/admin/marketing/applications?review=true&page=1&page_size=25')
    await bonusApi.ledger({ type: 'spend' }, 1, 25)
    expect(last().url).toBe('/api/v1/admin/marketing/bonus-ledger?type=spend&page=1&page_size=25')
    await marketingApi.report({ from: '2026-10-01', to: '' })
    expect(last().url).toBe('/api/v1/admin/marketing/reports?from=2026-10-01')
  })

  it('previews a segment with typed filters', async () => {
    await segmentsApi.preview('drivers', { driver_statuses: ['approved'], online_within_days: 7 })
    expect(last()).toEqual({
      method: 'POST',
      url: '/api/v1/admin/marketing/segments/preview',
      body: { audience: 'drivers', filters: { driver_statuses: ['approved'], online_within_days: 7 } },
    })
  })
})
