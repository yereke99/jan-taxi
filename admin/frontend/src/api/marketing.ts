// Promotions (ARCHITECTURE §23): versioned campaigns, coupons, the passenger
// bonus balance, referrals, promotions applied to orders with the driver's
// compensation, segments, broadcasts, reports, reconciliation and the
// emergency switches. Shapes follow admin/backend/internal/marketing; the
// server computes every amount, the panel shows and sends decisions only.

import { api } from '../services/api'
import type {
  BroadcastStatus,
  CampaignStatus,
  Category,
  CouponStatus,
  OrderStatus,
  PromotionStatus,
  RecipientStatus,
  ReferralStatus,
} from '../utils/status'
import type { Language, LocalizedText, Page } from './common'
import { opt } from './common'
import type { SettingItem } from './config'

// --- templates and rules ------------------------------------------------------

export const CAMPAIGN_TEMPLATES = [
  'first_ride_coupon',
  'referral_first_ride',
  'passenger_credit',
  'driver_milestone_bonus',
  'manual_grant',
  'broadcast_only',
] as const
export type CampaignTemplate = (typeof CAMPAIGN_TEMPLATES)[number]

/** Templates an administrator may create; manual_grant is the system's bucket for manual bonus credits. */
export const CREATABLE_TEMPLATES: readonly CampaignTemplate[] = [
  'first_ride_coupon',
  'referral_first_ride',
  'passenger_credit',
  'driver_milestone_bonus',
  'broadcast_only',
]

/** When a passenger may invite friends (ReferralRules.unlock). */
export const UNLOCK_POLICIES = ['immediate', 'coupon_redeemed', 'coupon_redeemed_or_expired', 'first_ride_completed'] as const
export type UnlockPolicy = (typeof UNLOCK_POLICIES)[number]

/** first_ride_coupon: one coupon after the first completed ride, for a later order of at least min_order_fare. */
export interface CouponRules {
  amount: number
  validity_hours: number
  min_order_fare: number
  categories: Category[]
}

/** referral_first_ride: reward_amount to the inviter once, when an invited new passenger completes the first ride. */
export interface ReferralRules {
  reward_amount: number
  categories: Category[]
  attribution_window_minutes: number
  unlock: UnlockPolicy
  /** A qualifying ride shorter than this goes to manual review (0 = never). */
  min_ride_seconds: number
}

/** passenger_credit: every passenger of the segment (snapshotted at activation) gets amount once. */
export interface CreditRules {
  amount: number
  segment_id: string
}

/** driver_milestone_bonus: a driver who completes `rides` orders inside the version's window gets amount once. */
export interface MilestoneRules {
  amount: number
  rides: number
  categories: Category[]
}

/** manual_grant: one manual bonus credit may not exceed max_amount. */
export interface ManualRules {
  max_amount: number
}

/** broadcast_only carries copy only, no money. */
export type BroadcastOnlyRules = Record<string, never>

export interface RulesByTemplate {
  first_ride_coupon: CouponRules
  referral_first_ride: ReferralRules
  passenger_credit: CreditRules
  driver_milestone_bonus: MilestoneRules
  manual_grant: ManualRules
  broadcast_only: BroadcastOnlyRules
}
export type CampaignRules = RulesByTemplate[CampaignTemplate]

// --- campaigns ------------------------------------------------------------------

/**
 * Budget accounting (§23.8): committed = unexpired issued and reserved coupons
 * plus pending grants; spent = redeemed coupons plus credited grants;
 * remaining is null without a budget limit.
 */
export interface CampaignUsage {
  committed: number
  spent: number
  remaining: number | null
  coupons: Partial<Record<CouponStatus, number>>
  grants: Record<string, number>
}

/** A campaign with its current version (openapi MarketingCampaign). */
export interface Campaign {
  id: string
  key: string
  template: CampaignTemplate
  status: CampaignStatus
  is_system: boolean
  version: number
  version_id: string
  title: LocalizedText
  description: LocalizedText
  /** Typed per template (RulesByTemplate). */
  rules: CampaignRules
  starts_at: string | null
  ends_at: string | null
  /** Total KZT the campaign may commit; null = no cap. */
  budget_limit: number | null
  /** Existing coupons cannot be used while frozen (new awards are not affected). */
  redemptions_frozen: boolean
  priority: number
  exclusivity_group: string | null
  scheduled_at: string | null
  active_since: string | null
  activated_at: string | null
  paused_at: string | null
  ended_at: string | null
  archived_at: string | null
  created_by: string | null
  approved_by: string | null
  created_at: string
  updated_at: string
  usage?: CampaignUsage
}

/** One immutable rules/copy version; coupons and grants keep the version that created them. */
export interface CampaignVersion {
  id: string
  version: number
  title: LocalizedText
  description: LocalizedText
  rules: CampaignRules
  starts_at: string | null
  ends_at: string | null
  change_note: string | null
  created_by: string | null
  created_at: string
}

export interface CampaignDetail extends Campaign {
  versions: CampaignVersion[]
}

export const PREVIEW_WARNINGS = [
  'awards_switch_off',
  'redemptions_switch_off',
  'fares_below_coupon_minimum',
  'legacy_passengers_locked',
  'coupon_campaign_not_active',
  'no_budget_limit',
] as const
export type PreviewWarning = (typeof PREVIEW_WARNINGS)[number]

/** Who a campaign reaches and what it may cost, before it is switched on. */
export interface CampaignPreview {
  audience: number
  per_person: number
  /** audience × per_person capped by the budget; null for a campaign without money. */
  max_liability: number | null
  budget_limit: number | null
  usage: CampaignUsage
  warnings: Array<PreviewWarning | (string & {})>
}

/** Create body; a new version takes title, description, rules, starts_at, ends_at and change_note only. */
export interface CampaignInput {
  template?: CampaignTemplate
  key?: string
  title?: LocalizedText
  description?: LocalizedText
  rules?: CampaignRules
  starts_at?: string
  ends_at?: string
  /** Kept only when an approver creates the draft. */
  budget_limit?: number
  priority?: number
  exclusivity_group?: string
  change_note?: string
}

export const CAMPAIGN_ACTIONS = [
  'schedule',
  'unschedule',
  'activate',
  'pause',
  'resume',
  'end',
  'archive',
  'freeze_redemptions',
  'unfreeze_redemptions',
] as const
export type CampaignAction = (typeof CAMPAIGN_ACTIONS)[number]

/** Approver actions (marketing.approve) need a reason of at least 3 characters and confirm: true. */
export interface CampaignTransitionBody {
  action: CampaignAction
  reason?: string
  confirm?: boolean
  /** RFC 3339, in the future; schedule only. */
  scheduled_at?: string
  /** The version the approver reviewed; a campaign changed since is refused (409 version_conflict). */
  expected_version?: number
}

export type BudgetBody = { budget_limit: number; reason: string; confirm: true } | { unlimited: true; reason: string; confirm: true }

export interface CampaignFilters {
  status?: string
  template?: string
  q?: string
}

// --- coupons, bonus balance, people -------------------------------------------------

/** A coupon as admins see it; validity is decided by expires_at against the database clock. */
export interface Coupon {
  id: string
  campaign_id: string
  campaign_key: string
  version: number
  user_id: string
  amount: number
  min_order_fare: number
  categories: Category[]
  status: CouponStatus
  issued_at: string
  expires_at: string
  source_order_number: number | null
  reserved_order_id: string | null
  redeemed_order_number: number | null
  redeemed_at: string | null
  closed_at: string | null
  /** The operator's reason, or a system code (SYSTEM_VOID_REASONS). */
  void_reason: string | null
}

/** Why the system voided a coupon: its qualifying ride was cancelled later (a dispute). */
export const SYSTEM_VOID_REASONS = ['qualifying_ride_cancelled'] as const

export interface CouponFilters {
  campaign_id?: string
  user_id?: string
  status?: string
}

/** A passenger's bonus balance: available = balance − reserved (earmarked for active orders). */
export interface BonusAccount {
  balance: number
  reserved: number
  available: number
}

export const BONUS_ENTRY_TYPES = ['grant', 'reserve', 'release', 'spend', 'adjustment_credit', 'adjustment_debit'] as const
export type BonusEntryType = (typeof BONUS_ENTRY_TYPES)[number]

export interface BonusEntry {
  id: number
  user_id: string
  type: BonusEntryType
  balance_delta: number
  reserved_delta: number
  balance_after: number
  reserved_after: number
  campaign_id: string | null
  order_id: string | null
  order_number: number | null
  admin_id: string | null
  idempotency_key: string
  description: string
  created_at: string
}

export interface BonusLedgerFilters {
  user_id?: string
  type?: string
}

/** A person in marketing views: masked, never the full phone. */
export interface UserRef {
  id: string
  name: string
  phone_masked: string | null
  telegram_user_id: number
  created_at: string
}

export const REFERRAL_SOURCES = ['bot_start', 'miniapp'] as const
export type ReferralSource = (typeof REFERRAL_SOURCES)[number]

/** Why a referral was held, capped or rejected (referral_attributions.reason). */
export const REFERRAL_REASONS = [
  'short_ride',
  'budget_exhausted',
  'already_rewarded',
  'same_phone',
  'referrer_drove',
  'referrer_blocked',
  'category_not_eligible',
  'qualifying_ride_cancelled',
] as const

export interface ReferralRow {
  id: string
  campaign_key: string
  invitee: UserRef
  referrer: UserRef
  code: string
  source: ReferralSource
  status: ReferralStatus
  reason: string | null
  qualifying_order_number: number | null
  attributed_at: string
  qualified_at: string | null
  decided_at: string | null
}

export interface ReferralFilters {
  status?: string
  referrer_id?: string
}

export type ReferralDecision = 'approve' | 'reject'

export type PromotionKind = 'coupon' | 'bonus'

/** A promotion applied to an order: the discount and the driver's compensation. */
export interface ApplicationRow {
  id: string
  order_id: string
  order_number: number
  order_status: OrderStatus
  user_id: string
  kind: PromotionKind
  campaign_key: string | null
  gross_fare: number
  discount: number
  status: PromotionStatus
  driver_code: string | null
  /** Credited to the driver's wallet once the order completed. */
  compensation: number | null
  /** Set when the promotion needs a finance decision (REVIEW_REASONS). */
  review_reason: string | null
  reserved_at: string
  settled_at: string | null
  released_at: string | null
  reversed_at: string | null
}

/**
 * Why a promotion waits for a finance decision: its order was cancelled after
 * the discount was settled, or the coupon's own qualifying ride was cancelled
 * (a dispute) while the coupon was already on this order.
 */
export const REVIEW_REASONS = ['order_cancelled_after_settlement', 'coupon_source_cancelled'] as const

export interface ApplicationFilters {
  status?: string
  /** 'true' lists only promotions under review. */
  review?: string
}

/** GET /admin/marketing/users/{id}. */
export interface UserMarketing {
  user: UserRef
  bonus: BonusAccount
  coupons: Coupon[]
  ledger: BonusEntry[]
  referral_code: string | null
  invited_by: ReferralRow | null
  invited: ReferralRow[]
  promotions: ApplicationRow[]
}

/** Manual bonus correction (finance.manage): signed amount, the key is created when the dialog opens. */
export interface BonusAdjustmentBody {
  amount: number
  reason: string
  idempotency_key: string
  confirm: true
}

// --- segments ------------------------------------------------------------------

export const AUDIENCES = ['passengers', 'drivers'] as const
export type Audience = (typeof AUDIENCES)[number]

/** Driver profile statuses a driver segment may select (approved when none is chosen). */
export const SEGMENT_DRIVER_STATUSES = ['approved', 'suspended', 'pending', 'changes_requested'] as const

/**
 * Typed segment filters (marketing.Filters). Every field is optional;
 * passenger-only (last_ride_within_days, no_ride_for_days, has_active_coupon)
 * and driver-only (driver_statuses, online_within_days, city_ids) fields are
 * refused for the other audience.
 */
export interface SegmentFilters {
  languages?: Language[]
  registered_within_days?: number
  registered_before_days?: number
  completed_rides_min?: number
  completed_rides_max?: number
  last_ride_within_days?: number
  no_ride_for_days?: number
  has_active_coupon?: boolean
  driver_statuses?: string[]
  online_within_days?: number
  city_ids?: string[]
}

/** An estimate only: no personal data leaves the server. reachable = matching − suppressed (opted out or blocked the bot). */
export interface AudienceCount {
  matching: number
  reachable: number
  suppressed: number
}

export interface Segment {
  id: string
  name: string
  audience: Audience
  filters: SegmentFilters
  created_at: string
  updated_at: string
  archived_at: string | null
  /** Absent for archived segments. */
  count?: AudienceCount
}

export interface SegmentInput {
  name?: string
  /** Fixed once the segment exists. */
  audience?: Audience
  filters?: SegmentFilters
  archived?: boolean
}

// --- broadcasts ----------------------------------------------------------------

/** Mini App start parameters a broadcast button may open, per audience (§18). */
export const START_PARAMS: Record<Audience, readonly string[]> = {
  passengers: ['home', 'taxi', 'orders', 'coupons', 'bonus', 'invite', 'support'],
  drivers: ['home', 'orders', 'wallet', 'profile'],
}

export interface BroadcastButton {
  label: LocalizedText
  start_param: string
}

/** A marketing message to one bot's audience (openapi MarketingBroadcast). */
export interface Broadcast {
  id: string
  campaign_id: string | null
  title: string
  audience: Audience
  segment_id: string | null
  filters: SegmentFilters
  /** Plain text per language; line breaks are kept. */
  content: LocalizedText
  button: BroadcastButton | null
  status: BroadcastStatus
  scheduled_at: string | null
  started_at: string | null
  completed_at: string | null
  cancelled_at: string | null
  /** Frozen when sending starts. */
  recipient_count: number | null
  created_by: string | null
  approved_by: string | null
  /** The starter's note; "approval_required" when the worker refused a scheduled broadcast whose audience outgrew the threshold. */
  approval_note: string | null
  version: number
  created_at: string
  updated_at: string
  /** Deliveries by status; test sends as test_<status>. "sent" = accepted by Telegram, not read. */
  stats: Record<string, number>
}

export interface BroadcastDetail extends Broadcast {
  /** Audience estimate while a draft or scheduled. */
  estimate: AudienceCount | null
  /** Above this many recipients only marketing.approve may start the broadcast. */
  approval_threshold: number
}

/**
 * Create/edit body (drafts only). A nil UUID in segment_id or campaign_id
 * clears it; no_button removes the button.
 */
export interface BroadcastInput {
  title?: string
  audience?: Audience
  segment_id?: string
  filters?: SegmentFilters
  content?: LocalizedText
  button?: BroadcastButton
  no_button?: boolean
  campaign_id?: string
}

export const BROADCAST_ACTIONS = ['schedule', 'send_now', 'pause', 'resume', 'cancel'] as const
export type BroadcastAction = (typeof BROADCAST_ACTIONS)[number]

/** schedule and send_now need confirm: true; reason is stored as the approval note. */
export interface BroadcastTransitionBody {
  action: BroadcastAction
  scheduled_at?: string
  reason?: string
  confirm?: boolean
}

/** A delivery as operators see it: no names or phones. */
export interface BroadcastRecipient {
  id: number
  user_id: string
  language: Language
  is_test: boolean
  status: RecipientStatus
  attempts: number
  http_status: number | null
  last_error: string | null
  sent_at: string | null
  updated_at: string
}

/** Clears segment_id / campaign_id in a broadcast edit. */
export const NIL_UUID = '00000000-0000-0000-0000-000000000000'

// --- overview, reports, reconciliation, settings ----------------------------------

export interface MarketingSwitches {
  awards_enabled: boolean
  redemptions_enabled: boolean
  broadcasts_enabled: boolean
}

/** GET /admin/marketing/overview: aggregates only. */
export interface MarketingOverview {
  switches: MarketingSwitches
  campaigns: Campaign[]
  coupons: Partial<Record<CouponStatus, number>>
  referrals: Partial<Record<ReferralStatus, number>>
  broadcasts: Partial<Record<BroadcastStatus, number>>
  bonus: { outstanding: number; reserved: number; accounts: number }
  compensation: { credited: number; pending: number }
  health: {
    events_pending: number
    events_failed: number
    /** Age of the oldest pending event, seconds; null without one. */
    oldest_pending_s: number | null
    stuck_reservations: number
    review_promotions: number
    review_referrals: number
    broadcast_queue: number
    overdue_scheduled: number
  }
}

export interface MarketingReportDay {
  /** YYYY-MM-DD in the report time zone. */
  date: string
  coupons_issued: number
  coupons_redeemed: number
  coupons_expired: number
  referrals: number
  referral_rewards: number
  bonus_granted: number
  discounts_settled: number
  messages_sent: number
  messages_failed: number
}

export interface MarketingReport {
  from: string
  to: string
  timezone: string
  days: MarketingReportDay[]
  funnel: {
    attributed: number
    qualified: number
    rewarded: number
    capped: number
    coupons_issued: number
    coupons_redeemed: number
  }
}

export interface MarketingReconciliation {
  ok: boolean
  checked_at: string
  totals: {
    discounts_settled: number
    compensation_credited: number
    compensation_reversed: number
    bonus_outstanding: number
    bonus_reserved: number
    coupon_liability: number
    promotions_reserved: number
    promotions_under_review: number
    events_failed: number
    events_pending: number
  }
  /** Ids only, no personal data. */
  issues: Array<{ kind: string; ref: string; note?: string }>
}

/** Keys of Marketing → Safety (app_settings), in display order. */
export const MARKETING_SETTING_KEYS = [
  'marketing.awards_enabled',
  'marketing.redemptions_enabled',
  'marketing.broadcasts_enabled',
  'marketing.broadcast_rate_per_s',
  'marketing.broadcast_approval_threshold',
  // The only Telegram ids a test broadcast may reach (int_list, empty by default).
  'marketing.test_recipients',
] as const
export type MarketingSettingKey = (typeof MARKETING_SETTING_KEYS)[number]

// --- requests -------------------------------------------------------------------

function id(v: string): string {
  return encodeURIComponent(v)
}

export const marketingApi = {
  overview(signal?: AbortSignal): Promise<MarketingOverview> {
    return api.get('/admin/marketing/overview', { signal })
  },
  /** from/to: local dates YYYY-MM-DD in the report time zone; "to" is inclusive. */
  report(q: { from?: string; to?: string }, signal?: AbortSignal): Promise<MarketingReport> {
    return api.get('/admin/marketing/reports', { query: { from: opt(q.from), to: opt(q.to) }, signal })
  },
  reconciliation(signal?: AbortSignal): Promise<MarketingReconciliation> {
    return api.get('/admin/marketing/reconciliation', { signal })
  },
  settings(signal?: AbortSignal): Promise<{ items: SettingItem[] }> {
    return api.get('/admin/marketing/settings', { signal })
  },
  /** Approver only; the reason is required and stored in the audit log with every changed key. */
  updateSettings(values: Partial<Record<MarketingSettingKey, unknown>>, reason: string): Promise<{ items: SettingItem[] }> {
    return api.patch('/admin/marketing/settings', { ...values, reason })
  },
}

export const campaignsApi = {
  list(f: CampaignFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<Campaign>> {
    return api.get('/admin/marketing/campaigns', {
      query: { status: opt(f.status), template: opt(f.template), q: opt(f.q?.trim()), page, page_size: pageSize },
      signal,
    })
  },
  get(campaignId: string, signal?: AbortSignal): Promise<CampaignDetail> {
    return api.get(`/admin/marketing/campaigns/${id(campaignId)}`, { signal })
  },
  preview(campaignId: string, signal?: AbortSignal): Promise<CampaignPreview> {
    return api.get(`/admin/marketing/campaigns/${id(campaignId)}/preview`, { signal })
  },
  create(body: CampaignInput): Promise<CampaignDetail> {
    return api.post('/admin/marketing/campaigns', body)
  },
  /** A draft needs marketing.edit; a scheduled, active or paused campaign marketing.approve. */
  newVersion(campaignId: string, body: CampaignInput): Promise<CampaignDetail> {
    return api.post(`/admin/marketing/campaigns/${id(campaignId)}/versions`, body)
  },
  duplicate(campaignId: string): Promise<CampaignDetail> {
    return api.post(`/admin/marketing/campaigns/${id(campaignId)}/duplicate`)
  },
  transition(campaignId: string, body: CampaignTransitionBody): Promise<CampaignDetail> {
    return api.post(`/admin/marketing/campaigns/${id(campaignId)}/transition`, body)
  },
  budget(campaignId: string, body: BudgetBody): Promise<CampaignDetail> {
    return api.patch(`/admin/marketing/campaigns/${id(campaignId)}/budget`, body)
  },
}

export const couponsApi = {
  list(f: CouponFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<Coupon>> {
    return api.get('/admin/marketing/coupons', {
      query: { campaign_id: opt(f.campaign_id), user_id: opt(f.user_id), status: opt(f.status), page, page_size: pageSize },
      signal,
    })
  },
  /** Only an unused (issued) coupon can be voided. */
  void(couponId: string, reason: string): Promise<Coupon> {
    return api.post(`/admin/marketing/coupons/${id(couponId)}/void`, { reason })
  },
}

export const bonusApi = {
  ledger(f: BonusLedgerFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<BonusEntry>> {
    return api.get('/admin/marketing/bonus-ledger', {
      query: { user_id: opt(f.user_id), type: opt(f.type), page, page_size: pageSize },
      signal,
    })
  },
  adjust(userId: string, body: BonusAdjustmentBody): Promise<BonusAccount> {
    return api.post(`/admin/marketing/users/${id(userId)}/bonus-adjustments`, body)
  },
}

export const promoUsersApi = {
  /** Phone digits, Telegram id or referral code; fewer than 3 characters find nobody. */
  find(q: string, signal?: AbortSignal): Promise<{ items: UserRef[] }> {
    return api.get('/admin/marketing/users', { query: { q: q.trim() }, signal })
  },
  get(userId: string, signal?: AbortSignal): Promise<UserMarketing> {
    return api.get(`/admin/marketing/users/${id(userId)}`, { signal })
  },
}

export const referralsApi = {
  list(f: ReferralFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<ReferralRow>> {
    return api.get('/admin/marketing/referrals', {
      query: { status: opt(f.status), referrer_id: opt(f.referrer_id), page, page_size: pageSize },
      signal,
    })
  },
  /** Only a referral under review; approve pays the reward (once per inviter, within the budget). */
  review(referralId: string, decision: ReferralDecision, reason: string): Promise<{ outcome: string }> {
    return api.post(`/admin/marketing/referrals/${id(referralId)}/review`, { decision, reason })
  },
}

export const promotionsApi = {
  list(f: ApplicationFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<ApplicationRow>> {
    return api.get('/admin/marketing/applications', {
      query: { status: opt(f.status), review: f.review === 'true' ? 'true' : undefined, page, page_size: pageSize },
      signal,
    })
  },
  /** finance.manage; only a settled promotion. 409 insufficient_balance when the driver's available balance is short. */
  reverse(applicationId: string, reason: string): Promise<{ status: string }> {
    return api.post(`/admin/marketing/applications/${id(applicationId)}/reverse`, { reason, confirm: true })
  },
}

export const segmentsApi = {
  list(signal?: AbortSignal): Promise<{ items: Segment[] }> {
    return api.get('/admin/marketing/segments', { signal })
  },
  create(body: SegmentInput): Promise<Segment> {
    return api.post('/admin/marketing/segments', body)
  },
  update(segmentId: string, body: SegmentInput): Promise<Segment> {
    return api.patch(`/admin/marketing/segments/${id(segmentId)}`, body)
  },
  preview(audience: Audience, filters: SegmentFilters, signal?: AbortSignal): Promise<AudienceCount> {
    return api.post('/admin/marketing/segments/preview', { audience, filters }, { signal })
  },
}

export const broadcastsApi = {
  list(f: { status?: string }, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<Broadcast>> {
    return api.get('/admin/marketing/broadcasts', { query: { status: opt(f.status), page, page_size: pageSize }, signal })
  },
  get(broadcastId: string, signal?: AbortSignal): Promise<BroadcastDetail> {
    return api.get(`/admin/marketing/broadcasts/${id(broadcastId)}`, { signal })
  },
  create(body: BroadcastInput): Promise<BroadcastDetail> {
    return api.post('/admin/marketing/broadcasts', body)
  },
  /** Drafts only. */
  update(broadcastId: string, body: BroadcastInput): Promise<BroadcastDetail> {
    return api.patch(`/admin/marketing/broadcasts/${id(broadcastId)}`, body)
  },
  recipients(broadcastId: string, f: { status?: string }, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<BroadcastRecipient>> {
    return api.get(`/admin/marketing/broadcasts/${id(broadcastId)}/recipients`, {
      query: { status: opt(f.status), page, page_size: pageSize },
      signal,
    })
  },
  /**
   * One message of a draft or scheduled broadcast to a Telegram id listed in
   * marketing.test_recipients whose owner started the audience's bot.
   */
  test(broadcastId: string, telegramUserId: number): Promise<{ delivery_id: number }> {
    return api.post(`/admin/marketing/broadcasts/${id(broadcastId)}/test`, { telegram_user_id: telegramUserId })
  },
  transition(broadcastId: string, body: BroadcastTransitionBody): Promise<BroadcastDetail> {
    return api.post(`/admin/marketing/broadcasts/${id(broadcastId)}/transition`, body)
  },
}
