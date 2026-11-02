// Campaign screens without Vue: the rules form per template (same ranges as
// marketing/rules.go, errors under the API's "rules.<field>" paths), the
// plain-language summary of a version's rules, the actions a campaign offers
// by status and permission (approver actions carry the reviewed version), and
// the on/off switch of the launch campaigns.

import type { LocalizedText } from '../../api/common'
import type {
  Campaign,
  CampaignAction,
  CampaignInput,
  CampaignRules,
  CampaignTemplate,
  CampaignTransitionBody,
  CouponRules,
  CreditRules,
  ManualRules,
  MilestoneRules,
  ReferralRules,
  UnlockPolicy,
} from '../../api/marketing'
import { parseInteger } from '../../forms/numbers'
import { parseLocalDateTime, toLocalDateTimeInput, type FieldErrors } from '../../forms/validation'
import type { Permission } from '../../permissions'
import { isApiError } from '../../services/api'
import { CATEGORIES, type CampaignStatus, type Category } from '../../utils/status'
import { codePoints } from '../../utils/text'

/** The customer's two launch promotions (seeded drafts): «Алғашқы сапар» and «Досыңды шақыр». */
export const LAUNCH_CAMPAIGN_KEYS = ['first_ride_coupon_400', 'referral_300_once'] as const

export function isLaunchCampaign(c: Pick<Campaign, 'key'>): boolean {
  return (LAUNCH_CAMPAIGN_KEYS as readonly string[]).includes(c.key)
}

/** Templates whose campaigns move money (and so have a budget). */
export function hasMoney(template: CampaignTemplate): boolean {
  return template !== 'broadcast_only'
}

/** Scheduled, active and paused campaigns are live: changing them is an approver's call. */
export function isLive(status: CampaignStatus): boolean {
  return status === 'scheduled' || status === 'active' || status === 'paused'
}

// --- actions -------------------------------------------------------------------

interface ActionRule {
  from: readonly CampaignStatus[]
  /** Needs marketing.approve (money starts or resumes flowing); others need marketing.edit or approve. */
  approve: boolean
}

const ALL_BUT_ARCHIVED: readonly CampaignStatus[] = ['draft', 'scheduled', 'active', 'paused', 'ended']

/** Mirror of marketing.transitions (campaigns.go). */
export const ACTION_RULES: Record<CampaignAction, ActionRule> = {
  schedule: { from: ['draft'], approve: true },
  unschedule: { from: ['scheduled'], approve: false },
  activate: { from: ['draft', 'scheduled'], approve: true },
  pause: { from: ['active'], approve: false },
  resume: { from: ['paused'], approve: true },
  end: { from: ['active', 'paused', 'scheduled'], approve: true },
  archive: { from: ['draft', 'ended'], approve: false },
  freeze_redemptions: { from: ALL_BUT_ARCHIVED, approve: true },
  unfreeze_redemptions: { from: ALL_BUT_ARCHIVED, approve: true },
}

const ACTION_ORDER: readonly CampaignAction[] = [
  'activate',
  'resume',
  'schedule',
  'pause',
  'unschedule',
  'freeze_redemptions',
  'unfreeze_redemptions',
  'end',
  'archive',
]

export function mayRunAction(action: CampaignAction, can: (p: Permission) => boolean): boolean {
  return ACTION_RULES[action].approve ? can('marketing.approve') : can('marketing.edit') || can('marketing.approve')
}

/**
 * Actions offered for c, in display order: allowed from its status and by the
 * admin's permissions. The manual bonus bucket has no lifecycle; freezing
 * concerns coupons only. The launch campaigns are switched on and off by
 * approvers only (the owner's rule), so pausing, unscheduling or archiving
 * them (an archived campaign never comes back, and a copy gets another key)
 * is not offered to marketing.edit either, although the API would accept it.
 */
export function availableActions(
  c: Pick<Campaign, 'status' | 'template' | 'redemptions_frozen' | 'key'>,
  can: (p: Permission) => boolean,
): CampaignAction[] {
  if (c.template === 'manual_grant') return []
  const launch = isLaunchCampaign(c)
  return ACTION_ORDER.filter((a) => {
    if (!ACTION_RULES[a].from.includes(c.status)) return false
    if (a === 'freeze_redemptions' && (c.redemptions_frozen || c.template !== 'first_ride_coupon')) return false
    if (a === 'unfreeze_redemptions' && !c.redemptions_frozen) return false
    if (launch && (a === 'pause' || a === 'unschedule' || a === 'archive')) return can('marketing.approve')
    return mayRunAction(a, can)
  })
}

/**
 * Body of an action on c. An approver action carries the version on the
 * operator's screen, so the server refuses it (409 version_conflict) when a
 * new version was saved meanwhile instead of switching on rules nobody
 * reviewed.
 */
export function transitionBody(c: Pick<Campaign, 'version'>, body: CampaignTransitionBody): CampaignTransitionBody {
  return ACTION_RULES[body.action].approve ? { ...body, expected_version: c.version } : body
}

/** The campaign changed after the operator opened it (a newer version exists). */
export function isVersionConflict(err: unknown): boolean {
  return isApiError(err) && err.code === 'version_conflict'
}

/** A draft or an ended campaign is edited with marketing.edit; a live one needs marketing.approve; archived never. */
export function canEditCampaign(c: Pick<Campaign, 'status'>, can: (p: Permission) => boolean): boolean {
  if (c.status === 'archived') return false
  return isLive(c.status) ? can('marketing.approve') : can('marketing.edit')
}

export function canDuplicate(c: Pick<Campaign, 'template'>, can: (p: Permission) => boolean): boolean {
  return c.template !== 'manual_grant' && can('marketing.edit')
}

// --- launch switch ----------------------------------------------------------------

export type SwitchAction = 'activate' | 'resume' | 'pause'

export interface SwitchState {
  /** True while the campaign is active. */
  on: boolean
  /** What flipping the switch does; null when it cannot move (ended, archived). */
  action: SwitchAction | null
  /** The admin may flip it (only approvers operate the launch switches). */
  allowed: boolean
}

/** ON = activate a draft or scheduled campaign, or resume a paused one; OFF = pause. */
export function launchSwitch(c: Pick<Campaign, 'status'>, can: (p: Permission) => boolean): SwitchState {
  const action: SwitchAction | null =
    c.status === 'active' ? 'pause' : c.status === 'paused' ? 'resume' : c.status === 'draft' || c.status === 'scheduled' ? 'activate' : null
  return { on: c.status === 'active', action, allowed: action !== null && can('marketing.approve') }
}

export type Translate = (key: string, params?: Record<string, unknown>) => string

/**
 * Why the server refused to switch a campaign on, in words the operator can
 * act on (the version's window is over, or the passenger credit's segment is
 * gone); null keeps the text of the error code.
 */
export function transitionErrorText(err: unknown, t: Translate): string | null {
  if (!isApiError(err) || err.code !== 'validation_failed') return null
  if (err.fields.ends_at) return t('marketing.err_window_over')
  if (err.fields['rules.segment_id']) return t('marketing.err_segment_gone')
  return null
}

// --- plain-language rules -----------------------------------------------------------

export interface SummaryWords {
  t: Translate
  money: (n: number) => string
}

function categoriesText(list: readonly string[] | undefined, w: SummaryWords): string {
  const names = (list ?? []).map((c) => w.t(`category.${c}`).toLocaleLowerCase())
  if (names.length === 0) return ''
  if (names.length === 1) return w.t('marketing.rules_only', { category: names[0] })
  return names.join(', ')
}

/** One line a non-technical operator understands, e.g. «400 ₸ купон, 72 сағат, ең аз баға 400 ₸, тек такси». */
export function rulesSummary(template: CampaignTemplate, rules: CampaignRules | null | undefined, w: SummaryWords): string {
  const r = (rules ?? {}) as Partial<CouponRules & ReferralRules & CreditRules & MilestoneRules & ManualRules>
  const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
  const parts: string[] = []
  switch (template) {
    case 'first_ride_coupon':
      parts.push(
        w.t('marketing.rules_coupon', { amount: w.money(num(r.amount)) }),
        w.t('marketing.rules_hours', { n: num(r.validity_hours) }),
        w.t('marketing.rules_min_fare', { amount: w.money(num(r.min_order_fare)) }),
        categoriesText(r.categories, w),
      )
      break
    case 'referral_first_ride':
      parts.push(
        w.t('marketing.rules_referral', { amount: w.money(num(r.reward_amount)) }),
        categoriesText(r.categories, w),
        w.t(`marketing.rules_unlock_${r.unlock ?? 'coupon_redeemed'}`),
      )
      if (num(r.min_ride_seconds) > 0) parts.push(w.t('marketing.rules_short_ride', { n: num(r.min_ride_seconds) }))
      break
    case 'passenger_credit':
      parts.push(w.t('marketing.rules_credit', { amount: w.money(num(r.amount)) }))
      break
    case 'driver_milestone_bonus':
      parts.push(w.t('marketing.rules_milestone', { amount: w.money(num(r.amount)), n: num(r.rides) }), categoriesText(r.categories, w))
      break
    case 'manual_grant':
      parts.push(w.t('marketing.rules_manual', { amount: w.money(num(r.max_amount)) }))
      break
    case 'broadcast_only':
      parts.push(w.t('marketing.rules_broadcast_only'))
      break
  }
  return parts.filter(Boolean).join(', ')
}

// --- rules form ---------------------------------------------------------------------

/** Every template's rule fields as typed text; only the template's own fields are sent. */
export interface RulesForm {
  amount: string
  validity_hours: string
  min_order_fare: string
  categories: Category[]
  reward_amount: string
  attribution_window_minutes: string
  unlock: UnlockPolicy
  min_ride_seconds: string
  segment_id: string
  rides: string
  max_amount: string
}

/** Server ranges (rules.go). */
export const RULE_LIMITS = {
  amount: [1, 100_000],
  validityHours: [1, 720],
  minOrderFare: 10_000_000,
  attributionWindow: [5, 7 * 24 * 60],
  minRideSeconds: [0, 3600],
  rides: [1, 1000],
  maxAmount: [1, 1_000_000],
} as const

/** Defaults of a new campaign: the launch campaigns' values. */
export function emptyRulesForm(): RulesForm {
  return {
    amount: '400',
    validity_hours: '72',
    min_order_fare: '400',
    categories: ['taxi'],
    reward_amount: '300',
    attribution_window_minutes: '60',
    unlock: 'coupon_redeemed',
    min_ride_seconds: '60',
    segment_id: '',
    rides: '10',
    max_amount: '50000',
  }
}

/** Form values of stored rules (missing fields keep the defaults). */
export function rulesToForm(template: CampaignTemplate, rules: CampaignRules | null | undefined): RulesForm {
  const f = emptyRulesForm()
  const r = (rules ?? {}) as Record<string, unknown>
  const str = (v: unknown, d: string): string => (typeof v === 'number' && Number.isFinite(v) ? String(v) : d)
  const cats = (v: unknown): Category[] =>
    Array.isArray(v) ? v.filter((c): c is Category => (CATEGORIES as readonly string[]).includes(String(c))) : f.categories
  switch (template) {
    case 'first_ride_coupon':
      return { ...f, amount: str(r.amount, f.amount), validity_hours: str(r.validity_hours, f.validity_hours), min_order_fare: str(r.min_order_fare, f.min_order_fare), categories: cats(r.categories) }
    case 'referral_first_ride':
      return {
        ...f,
        reward_amount: str(r.reward_amount, f.reward_amount),
        categories: cats(r.categories),
        attribution_window_minutes: str(r.attribution_window_minutes, f.attribution_window_minutes),
        unlock: (['coupon_redeemed', 'coupon_redeemed_or_expired', 'first_ride_completed'] as const).find((u) => u === r.unlock) ?? f.unlock,
        min_ride_seconds: str(r.min_ride_seconds, f.min_ride_seconds),
      }
    case 'passenger_credit':
      return { ...f, amount: str(r.amount, f.amount), segment_id: typeof r.segment_id === 'string' ? r.segment_id : '' }
    case 'driver_milestone_bonus':
      return { ...f, amount: str(r.amount, f.amount), rides: str(r.rides, f.rides), categories: cats(r.categories) }
    case 'manual_grant':
      return { ...f, max_amount: str(r.max_amount, f.max_amount) }
    case 'broadcast_only':
      return f
  }
}

function intField(errors: FieldErrors, name: string, raw: string, lo: number, hi: number): number {
  const path = `rules.${name}`
  if (raw.trim() === '') {
    errors[path] = 'required'
    return 0
  }
  const n = parseInteger(raw)
  if (n === null) errors[path] = 'integer'
  else if (n < lo || n > hi) errors[path] = 'out_of_range'
  return n ?? 0
}

function categoriesField(errors: FieldErrors, list: Category[]): Category[] {
  const unique = CATEGORIES.filter((c) => list.includes(c))
  if (unique.length === 0) errors['rules.categories'] = 'one_required'
  return unique
}

/** Typed rules for the API, or field errors ("rules.amount": "out_of_range"). */
export function formToRules(template: CampaignTemplate, f: RulesForm): { errors: FieldErrors; rules: CampaignRules | null } {
  const errors: FieldErrors = {}
  const L = RULE_LIMITS
  let rules: CampaignRules
  switch (template) {
    case 'first_ride_coupon': {
      const amount = intField(errors, 'amount', f.amount, L.amount[0], L.amount[1])
      const validity = intField(errors, 'validity_hours', f.validity_hours, L.validityHours[0], L.validityHours[1])
      const minFare = intField(errors, 'min_order_fare', f.min_order_fare, 0, L.minOrderFare)
      // No partial redemption: the cash a passenger pays never goes below zero.
      if (!errors['rules.min_order_fare'] && !errors['rules.amount'] && minFare < amount) errors['rules.min_order_fare'] = 'below_amount'
      rules = { amount, validity_hours: validity, min_order_fare: minFare, categories: categoriesField(errors, f.categories) }
      break
    }
    case 'referral_first_ride':
      rules = {
        reward_amount: intField(errors, 'reward_amount', f.reward_amount, L.amount[0], L.amount[1]),
        categories: categoriesField(errors, f.categories),
        attribution_window_minutes: intField(errors, 'attribution_window_minutes', f.attribution_window_minutes, L.attributionWindow[0], L.attributionWindow[1]),
        unlock: f.unlock,
        min_ride_seconds: intField(errors, 'min_ride_seconds', f.min_ride_seconds, L.minRideSeconds[0], L.minRideSeconds[1]),
      }
      break
    case 'passenger_credit':
      if (!f.segment_id) errors['rules.segment_id'] = 'required'
      rules = { amount: intField(errors, 'amount', f.amount, L.amount[0], L.amount[1]), segment_id: f.segment_id }
      break
    case 'driver_milestone_bonus':
      rules = {
        amount: intField(errors, 'amount', f.amount, L.amount[0], L.amount[1]),
        rides: intField(errors, 'rides', f.rides, L.rides[0], L.rides[1]),
        categories: categoriesField(errors, f.categories),
      }
      break
    case 'manual_grant':
      rules = { max_amount: intField(errors, 'max_amount', f.max_amount, L.maxAmount[0], L.maxAmount[1]) }
      break
    case 'broadcast_only':
      rules = {}
      break
  }
  return { errors, rules: Object.keys(errors).length ? null : rules }
}

// --- campaign form --------------------------------------------------------------------

export const CAMPAIGN_LIMITS = { title: 80, description: 600, changeNote: 300, priority: 1000, budget: 1_000_000_000 } as const

const KEY_RE = /^[a-z0-9_]{3,40}$/
const GROUP_RE = /^[a-z0-9_]{1,40}$/
const LANGS = ['kk', 'ru', 'en'] as const

export interface CampaignForm {
  template: CampaignTemplate
  key: string
  title: LocalizedText
  description: LocalizedText
  /** datetime-local values in the operator's time zone; '' = open. */
  startsAt: string
  endsAt: string
  priority: string
  exclusivityGroup: string
  /** '' = no limit; only approvers' drafts keep a budget. */
  budgetLimit: string
  changeNote: string
  rules: RulesForm
}

export function emptyCampaignForm(template: CampaignTemplate = 'first_ride_coupon'): CampaignForm {
  return {
    template,
    key: '',
    title: { kk: '', ru: '', en: '' },
    description: { kk: '', ru: '', en: '' },
    startsAt: '',
    endsAt: '',
    priority: '100',
    exclusivityGroup: '',
    budgetLimit: '',
    changeNote: '',
    rules: emptyRulesForm(),
  }
}

/** Form of the campaign's current version, for a new version. */
export function campaignToForm(c: Campaign): CampaignForm {
  return {
    template: c.template,
    key: c.key,
    title: { ...c.title },
    description: { kk: c.description?.kk ?? '', ru: c.description?.ru ?? '', en: c.description?.en ?? '' },
    startsAt: toLocalDateTimeInput(c.starts_at),
    endsAt: toLocalDateTimeInput(c.ends_at),
    priority: String(c.priority),
    exclusivityGroup: c.exclusivity_group ?? '',
    budgetLimit: c.budget_limit === null ? '' : String(c.budget_limit),
    changeNote: '',
    rules: rulesToForm(c.template, c.rules),
  }
}

function cleanLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

function localized(errors: FieldErrors, name: string, v: LocalizedText, max: number, required: boolean): LocalizedText {
  const out = { kk: cleanLine(v.kk), ru: cleanLine(v.ru), en: cleanLine(v.en) }
  const any = LANGS.some((l) => out[l] !== '')
  for (const l of LANGS) {
    if (codePoints(out[l]) > max) errors[`${name}.${l}`] = 'too_long'
    else if (out[l] === '' && (required || any)) errors[`${name}.${l}`] = 'required'
  }
  return out
}

/**
 * Checks a create (mode "create") or new-version (mode "version") form the
 * way POST /admin/marketing/campaigns and …/versions do. A version carries
 * copy, rules and the window only; budget, priority and group have their own
 * endpoint or stay as created. A new version keeps a start or end the current
 * version has when the field is left out, so `window` (what the current
 * version sets) makes an emptied field an error instead of a silent no-op.
 */
export function validateCampaignForm(
  form: CampaignForm,
  mode: 'create' | 'version',
  opts: { canBudget?: boolean; rulesLocked?: boolean; window?: { starts: boolean; ends: boolean } } = {},
): { errors: FieldErrors; payload: CampaignInput | null } {
  const errors: FieldErrors = {}
  const payload: CampaignInput = {}
  if (mode === 'create') {
    payload.template = form.template
    const key = form.key.trim()
    if (key) {
      if (!KEY_RE.test(key)) errors.key = 'invalid'
      payload.key = key
    }
  }
  payload.title = localized(errors, 'title', form.title, CAMPAIGN_LIMITS.title, true)
  const description = localized(errors, 'description', form.description, CAMPAIGN_LIMITS.description, false)
  // A version always sends the description, so clearing it is possible.
  if (mode === 'version' || LANGS.some((l) => description[l] !== '')) payload.description = description

  if (!opts.rulesLocked) {
    const r = formToRules(form.template, form.rules)
    Object.assign(errors, r.errors)
    if (r.rules) payload.rules = r.rules
  }

  const starts = form.startsAt ? parseLocalDateTime(form.startsAt) : null
  const ends = form.endsAt ? parseLocalDateTime(form.endsAt) : null
  if (form.startsAt && !starts) errors.starts_at = 'invalid'
  if (form.endsAt && !ends) errors.ends_at = 'invalid'
  if (starts && ends && ends.getTime() <= starts.getTime()) errors.ends_at = 'before_start'
  if (form.template === 'driver_milestone_bonus' && (!form.startsAt || !form.endsAt) && !errors.ends_at) errors.ends_at = 'window_required'
  if (mode === 'version' && opts.window?.starts && !form.startsAt && !errors.starts_at) errors.starts_at = 'required'
  if (mode === 'version' && opts.window?.ends && !form.endsAt && !errors.ends_at) errors.ends_at = 'required'
  if (starts) payload.starts_at = starts.toISOString()
  if (ends) payload.ends_at = ends.toISOString()

  if (mode === 'create') {
    const priority = parseInteger(form.priority)
    if (form.priority.trim() !== '') {
      if (priority === null) errors.priority = 'integer'
      else if (priority < 0 || priority > CAMPAIGN_LIMITS.priority) errors.priority = 'out_of_range'
      else payload.priority = priority
    }
    const group = form.exclusivityGroup.trim()
    if (group) {
      if (!GROUP_RE.test(group)) errors.exclusivity_group = 'invalid'
      payload.exclusivity_group = group
    }
    if (opts.canBudget && form.budgetLimit.trim() !== '' && hasMoney(form.template)) {
      const budget = parseInteger(form.budgetLimit)
      if (budget === null) errors.budget_limit = 'integer'
      else if (budget < 0 || budget > CAMPAIGN_LIMITS.budget) errors.budget_limit = 'out_of_range'
      else payload.budget_limit = budget
    }
  } else {
    const note = cleanLine(form.changeNote)
    if (codePoints(note) > CAMPAIGN_LIMITS.changeNote) errors.change_note = 'too_long'
    else if (note) payload.change_note = note
  }
  return { errors, payload: Object.keys(errors).length ? null : payload }
}

/** Budget usage of a campaign: committed + spent, the limit and the used share (0..1; null without a limit). */
export function budgetUsage(c: Pick<Campaign, 'budget_limit' | 'usage'>): { used: number; limit: number | null; share: number | null } {
  const used = (c.usage?.committed ?? 0) + (c.usage?.spent ?? 0)
  const limit = c.budget_limit
  return { used, limit, share: limit === null ? null : limit <= 0 ? 1 : Math.min(1, used / limit) }
}
