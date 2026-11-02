// Campaign rules in plain words, the typed rules form (server ranges), the
// actions by status and permission (with the reviewed version on approver
// actions), the launch switch and the words for a refused activation.

import { afterEach, describe, expect, it } from 'vitest'
import type { Campaign } from '../../api/marketing'
import { formatMoney } from '../../utils/format'
import { i18n } from '../../i18n'
import { ROLE_PERMISSIONS, type Permission, type Role } from '../../permissions'
import { ApiError } from '../../services/api'
import {
  availableActions,
  canEditCampaign,
  emptyCampaignForm,
  emptyRulesForm,
  formToRules,
  isVersionConflict,
  launchSwitch,
  rulesSummary,
  transitionBody,
  transitionErrorText,
  validateCampaignForm,
} from './campaignModel'

function can(role: Role): (p: Permission) => boolean {
  return (p) => ROLE_PERMISSIONS[role].includes(p)
}

function words(locale: 'kk' | 'ru' | 'en') {
  i18n.global.locale.value = locale
  return { t: i18n.global.t, money: (n: number) => formatMoney(n, locale) }
}

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

const NBSP = ' '
const couponRules = { amount: 400, validity_hours: 72, min_order_fare: 400, categories: ['taxi' as const] }

describe('rulesSummary', () => {
  it('describes the first-ride coupon the way the customer wrote it', () => {
    expect(rulesSummary('first_ride_coupon', couponRules, words('kk'))).toBe(`400${NBSP}₸ купон, 72 сағат, ең аз баға 400${NBSP}₸, тек такси`)
    expect(rulesSummary('first_ride_coupon', couponRules, words('ru'))).toBe(`купон 400${NBSP}₸, действует 72 ч, минимальная цена 400${NBSP}₸, только такси`)
    expect(rulesSummary('first_ride_coupon', couponRules, words('en'))).toBe(`400${NBSP}₸ coupon, valid 72 h, minimum fare 400${NBSP}₸, taxi only`)
  })

  it('describes the referral reward, its unlock rule and the short-ride review', () => {
    const rules = { reward_amount: 300, categories: ['taxi' as const], attribution_window_minutes: 60, unlock: 'coupon_redeemed' as const, min_ride_seconds: 60 }
    expect(rulesSummary('referral_first_ride', rules, words('en'))).toBe(
      `300${NBSP}₸ to the inviter, once, taxi only, invites unlock once the coupon is used, rides under 60 s go to review`,
    )
    expect(rulesSummary('referral_first_ride', { ...rules, min_ride_seconds: 0 }, words('en'))).not.toContain('review')
  })
})

describe('rules form', () => {
  it('starts with the launch campaigns’ values and sends only the template’s fields', () => {
    const r = formToRules('first_ride_coupon', emptyRulesForm())
    expect(r.errors).toEqual({})
    expect(r.rules).toEqual(couponRules)
    expect(formToRules('referral_first_ride', emptyRulesForm()).rules).toEqual({
      reward_amount: 300,
      categories: ['taxi'],
      attribution_window_minutes: 60,
      unlock: 'coupon_redeemed',
      min_ride_seconds: 60,
    })
    expect(formToRules('broadcast_only', emptyRulesForm()).rules).toEqual({})
  })

  it('applies the API ranges under the rules.<field> paths', () => {
    const f = { ...emptyRulesForm(), amount: '500', min_order_fare: '400', validity_hours: '800', categories: [] }
    expect(formToRules('first_ride_coupon', f).errors).toEqual({
      'rules.min_order_fare': 'below_amount',
      'rules.validity_hours': 'out_of_range',
      'rules.categories': 'one_required',
    })
    expect(formToRules('first_ride_coupon', { ...emptyRulesForm(), amount: '4x' }).errors).toEqual({ 'rules.amount': 'integer' })
    expect(formToRules('referral_first_ride', { ...emptyRulesForm(), attribution_window_minutes: '2' }).errors).toEqual({ 'rules.attribution_window_minutes': 'out_of_range' })
    expect(formToRules('passenger_credit', emptyRulesForm()).errors).toEqual({ 'rules.segment_id': 'required' })
  })

  it('needs a window for a ride-count bonus and leaves budgets to approvers', () => {
    const milestone = emptyCampaignForm('driver_milestone_bonus')
    milestone.title = { kk: 'Апта', ru: 'Неделя', en: 'Week' }
    expect(validateCampaignForm(milestone, 'create').errors).toEqual({ ends_at: 'window_required' })

    const coupon = emptyCampaignForm('first_ride_coupon')
    coupon.title = { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }
    coupon.budgetLimit = '100000'
    expect(validateCampaignForm(coupon, 'create', { canBudget: false }).payload).not.toHaveProperty('budget_limit')
    expect(validateCampaignForm(coupon, 'create', { canBudget: true }).payload).toMatchObject({ template: 'first_ride_coupon', budget_limit: 100000, priority: 100 })

    coupon.description = { kk: 'Сипаттама', ru: '', en: '' }
    expect(validateCampaignForm(coupon, 'create').errors).toEqual({ 'description.ru': 'required', 'description.en': 'required' })
  })

  it('does not let a new version silently keep a window date the operator emptied', () => {
    const f = emptyCampaignForm('first_ride_coupon')
    f.title = { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }
    expect(validateCampaignForm(f, 'version', { window: { starts: false, ends: true } }).errors).toEqual({ ends_at: 'required' })
    f.endsAt = '2026-12-01T10:00'
    expect(validateCampaignForm(f, 'version', { window: { starts: false, ends: true } }).payload).toMatchObject({ ends_at: new Date(2026, 11, 1, 10, 0).toISOString() })
    // A new campaign may leave both empty.
    expect(validateCampaignForm({ ...f, endsAt: '' }, 'create', { window: { starts: true, ends: true } }).errors).toEqual({})
  })

  it('sends copy, rules and the change note as a new version, never the budget or the key', () => {
    const f = emptyCampaignForm('first_ride_coupon')
    f.key = 'first_ride_coupon_400'
    f.title = { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }
    f.changeNote = '  Customer asked for 72 hours '
    const { payload } = validateCampaignForm(f, 'version')
    expect(payload).toEqual({
      title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' },
      description: { kk: '', ru: '', en: '' },
      rules: couponRules,
      change_note: 'Customer asked for 72 hours',
    })
  })
})

function campaign(status: Campaign['status']): Campaign {
  return { status, template: 'first_ride_coupon', redemptions_frozen: false, key: 'first_ride_coupon_400' } as Campaign
}

describe('actions by status and permission', () => {
  it('offers approver actions to approvers only', () => {
    expect(availableActions(campaign('draft'), can('finance_manager'))).toEqual(['activate', 'schedule', 'freeze_redemptions', 'archive'])
    // Archiving cannot be undone, so a launch campaign is archived by approvers only (the owner's rule).
    expect(availableActions(campaign('draft'), can('marketing_manager'))).toEqual([])
    expect(availableActions({ ...campaign('draft'), key: 'driver_week_10' }, can('marketing_manager'))).toEqual(['archive'])
    expect(availableActions(campaign('ended'), can('marketing_manager'))).toEqual([])
    expect(availableActions(campaign('ended'), can('finance_manager'))).toEqual(['freeze_redemptions', 'archive'])
    // Any campaign may be paused with marketing.edit; the launch ones only by approvers (the owner's rule).
    expect(availableActions({ ...campaign('active'), key: 'driver_week_10' }, can('marketing_manager'))).toEqual(['pause'])
    expect(availableActions(campaign('active'), can('marketing_manager'))).toEqual([])
    expect(availableActions(campaign('scheduled'), can('marketing_manager'))).toEqual([])
    expect(availableActions({ ...campaign('scheduled'), key: 'driver_week_10' }, can('marketing_manager'))).toEqual(['unschedule'])
    expect(availableActions(campaign('active'), can('super_admin'))).toEqual(['pause', 'freeze_redemptions', 'end'])
    expect(availableActions(campaign('paused'), can('finance_manager'))).toEqual(['resume', 'freeze_redemptions', 'end'])
    expect(availableActions(campaign('active'), can('auditor'))).toEqual([])
    expect(availableActions({ ...campaign('active'), redemptions_frozen: true }, can('finance_manager'))).toEqual(['pause', 'unfreeze_redemptions', 'end'])
    expect(availableActions({ ...campaign('draft'), template: 'manual_grant' }, can('super_admin'))).toEqual([])
  })

  it('sends the reviewed version with approver actions only', () => {
    const c = { version: 3 }
    expect(transitionBody(c, { action: 'activate', reason: 'Launch', confirm: true })).toEqual({ action: 'activate', reason: 'Launch', confirm: true, expected_version: 3 })
    expect(transitionBody(c, { action: 'schedule', scheduled_at: '2026-11-03T05:00:00.000Z', reason: 'Monday', confirm: true })).toMatchObject({ expected_version: 3 })
    for (const action of ['resume', 'end', 'freeze_redemptions', 'unfreeze_redemptions'] as const) {
      expect(transitionBody(c, { action, reason: 'Checked', confirm: true }).expected_version).toBe(3)
    }
    for (const action of ['pause', 'unschedule', 'archive'] as const) {
      expect(transitionBody(c, { action, reason: 'Checked', confirm: true })).not.toHaveProperty('expected_version')
    }
    expect(isVersionConflict(new ApiError(409, 'version_conflict', 'changed', {}, { current_version: 4 }))).toBe(true)
    expect(isVersionConflict(new ApiError(409, 'invalid_transition', 'no'))).toBe(false)
  })

  it('lets marketing.edit change drafts and only approvers change live campaigns', () => {
    expect(canEditCampaign(campaign('draft'), can('marketing_manager'))).toBe(true)
    expect(canEditCampaign(campaign('active'), can('marketing_manager'))).toBe(false)
    expect(canEditCampaign(campaign('active'), can('finance_manager'))).toBe(true)
    expect(canEditCampaign(campaign('archived'), can('super_admin'))).toBe(false)
  })
})

describe('launch switch', () => {
  it('switches on by activating or resuming and off by pausing, for approvers only', () => {
    expect(launchSwitch(campaign('draft'), can('finance_manager'))).toEqual({ on: false, action: 'activate', allowed: true })
    expect(launchSwitch(campaign('scheduled'), can('super_admin'))).toEqual({ on: false, action: 'activate', allowed: true })
    expect(launchSwitch(campaign('paused'), can('super_admin'))).toEqual({ on: false, action: 'resume', allowed: true })
    expect(launchSwitch(campaign('active'), can('super_admin'))).toEqual({ on: true, action: 'pause', allowed: true })
    expect(launchSwitch(campaign('active'), can('marketing_manager'))).toEqual({ on: true, action: 'pause', allowed: false })
    expect(launchSwitch(campaign('ended'), can('super_admin'))).toEqual({ on: false, action: null, allowed: false })
  })
})

describe('refused activation', () => {
  it('names the window or the segment instead of the generic validation text', () => {
    const t = words('en').t
    expect(transitionErrorText(new ApiError(422, 'validation_failed', 'invalid', { ends_at: 'in_past' }), t)).toBe(
      'The campaign window is over. Move its end date later with “Edit rules” first.',
    )
    expect(transitionErrorText(new ApiError(422, 'validation_failed', 'invalid', { 'rules.segment_id': 'not_found' }), t)).toContain('segment')
    expect(transitionErrorText(new ApiError(422, 'validation_failed', 'invalid', { reason: 'too_short' }), t)).toBeNull()
    expect(transitionErrorText(new ApiError(409, 'campaign_conflict', 'live'), t)).toBeNull()
  })
})
