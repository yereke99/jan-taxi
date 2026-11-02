// Domain enums (mirroring the database CHECK constraints) and the semantic tone
// each status is shown with. Accent colours carry meaning only (§15).

export type Tone = 'neutral' | 'dark' | 'success' | 'danger' | 'info'

export const ORDER_STATUSES = [
  'draft',
  'searching',
  'accepted',
  'driver_arriving',
  'in_progress',
  'completed',
  'cancelled',
  'disputed',
] as const
export type OrderStatus = (typeof ORDER_STATUSES)[number]

export const ACTIVE_ORDER_STATUSES: readonly OrderStatus[] = ['searching', 'accepted', 'driver_arriving', 'in_progress']

export const CATEGORIES = ['taxi', 'parcel', 'other'] as const
export type Category = (typeof CATEGORIES)[number]

export const PAYMENT_METHODS = ['cash', 'bank_transfer'] as const
export type PaymentMethod = (typeof PAYMENT_METHODS)[number]

export const MODERATION_STATUSES = ['draft', 'pending', 'approved', 'changes_requested', 'rejected', 'suspended'] as const
export type ModerationStatus = (typeof MODERATION_STATUSES)[number]

export const TOPUP_STATUSES = ['created', 'pending_verification', 'confirmed', 'rejected', 'expired', 'refunded'] as const
export type TopUpStatus = (typeof TOPUP_STATUSES)[number]

export const DISPUTE_STATUSES = ['open', 'resolved'] as const
export type DisputeStatus = (typeof DISPUTE_STATUSES)[number]

export const NOTIFICATION_STATUSES = ['pending', 'sending', 'sent', 'dead', 'cancelled'] as const
export type NotificationStatus = (typeof NOTIFICATION_STATUSES)[number]

// Marketing (§23): campaigns, coupons, referrals, promotions applied to
// orders, broadcasts and their deliveries.

export const CAMPAIGN_STATUSES = ['draft', 'scheduled', 'active', 'paused', 'ended', 'archived'] as const
export type CampaignStatus = (typeof CAMPAIGN_STATUSES)[number]

export const COUPON_STATUSES = ['issued', 'reserved', 'redeemed', 'expired', 'voided'] as const
export type CouponStatus = (typeof COUPON_STATUSES)[number]

export const REFERRAL_STATUSES = ['attributed', 'review', 'rewarded', 'capped', 'rejected'] as const
export type ReferralStatus = (typeof REFERRAL_STATUSES)[number]

export const PROMOTION_STATUSES = ['reserved', 'released', 'settled', 'reversed'] as const
export type PromotionStatus = (typeof PROMOTION_STATUSES)[number]

export const BROADCAST_STATUSES = ['draft', 'scheduled', 'sending', 'paused', 'completed', 'cancelled', 'failed'] as const
export type BroadcastStatus = (typeof BROADCAST_STATUSES)[number]

export const RECIPIENT_STATUSES = ['queued', 'sending', 'sent', 'failed', 'blocked', 'suppressed', 'cancelled'] as const
export type RecipientStatus = (typeof RECIPIENT_STATUSES)[number]

const ORDER_TONES: Record<OrderStatus, Tone> = {
  draft: 'neutral',
  searching: 'neutral',
  accepted: 'dark',
  driver_arriving: 'dark',
  in_progress: 'info',
  completed: 'success',
  cancelled: 'danger',
  disputed: 'danger',
}

const MODERATION_TONES: Record<ModerationStatus, Tone> = {
  draft: 'neutral',
  pending: 'info',
  approved: 'success',
  changes_requested: 'dark',
  rejected: 'danger',
  suspended: 'danger',
}

const TOPUP_TONES: Record<TopUpStatus, Tone> = {
  created: 'neutral',
  pending_verification: 'info',
  confirmed: 'success',
  rejected: 'danger',
  expired: 'neutral',
  refunded: 'dark',
}

const DISPUTE_TONES: Record<DisputeStatus, Tone> = { open: 'danger', resolved: 'success' }

const NOTIFICATION_TONES: Record<NotificationStatus, Tone> = {
  pending: 'neutral',
  sending: 'info',
  sent: 'success',
  dead: 'danger',
  cancelled: 'neutral',
}

// Green marks money that reached someone or a finished success; red a stop
// or a problem; black something in progress that holds money or messages.
const CAMPAIGN_TONES: Record<CampaignStatus, Tone> = {
  draft: 'neutral',
  scheduled: 'info',
  active: 'success',
  paused: 'dark',
  ended: 'neutral',
  archived: 'neutral',
}

const COUPON_TONES: Record<CouponStatus, Tone> = {
  issued: 'info',
  reserved: 'dark',
  redeemed: 'success',
  expired: 'neutral',
  voided: 'danger',
}

const REFERRAL_TONES: Record<ReferralStatus, Tone> = {
  attributed: 'neutral',
  review: 'info',
  rewarded: 'success',
  capped: 'neutral',
  rejected: 'danger',
}

const PROMOTION_TONES: Record<PromotionStatus, Tone> = {
  reserved: 'dark',
  released: 'neutral',
  settled: 'success',
  reversed: 'danger',
}

const BROADCAST_TONES: Record<BroadcastStatus, Tone> = {
  draft: 'neutral',
  scheduled: 'info',
  sending: 'dark',
  paused: 'dark',
  completed: 'success',
  cancelled: 'neutral',
  failed: 'danger',
}

const RECIPIENT_TONES: Record<RecipientStatus, Tone> = {
  queued: 'neutral',
  sending: 'info',
  sent: 'success',
  failed: 'danger',
  blocked: 'danger',
  suppressed: 'neutral',
  cancelled: 'neutral',
}

export type StatusKind =
  | 'order'
  | 'moderation'
  | 'topup'
  | 'dispute'
  | 'notification'
  | 'campaign'
  | 'coupon'
  | 'referral'
  | 'promotion'
  | 'broadcast'
  | 'recipient'

/** Tone for any status kind shown in the admin tables. */
export function statusTone(kind: StatusKind, status: string): Tone {
  switch (kind) {
    case 'order':
      return orderStatusTone(status)
    case 'moderation':
      return moderationStatusTone(status)
    case 'topup':
      return topUpStatusTone(status)
    case 'dispute':
      return DISPUTE_TONES[status as DisputeStatus] ?? 'neutral'
    case 'notification':
      return NOTIFICATION_TONES[status as NotificationStatus] ?? 'neutral'
    case 'campaign':
      return CAMPAIGN_TONES[status as CampaignStatus] ?? 'neutral'
    case 'coupon':
      return COUPON_TONES[status as CouponStatus] ?? 'neutral'
    case 'referral':
      return REFERRAL_TONES[status as ReferralStatus] ?? 'neutral'
    case 'promotion':
      return PROMOTION_TONES[status as PromotionStatus] ?? 'neutral'
    case 'broadcast':
      return BROADCAST_TONES[status as BroadcastStatus] ?? 'neutral'
    case 'recipient':
      return RECIPIENT_TONES[status as RecipientStatus] ?? 'neutral'
  }
}

export function orderStatusTone(s: string): Tone {
  return ORDER_TONES[s as OrderStatus] ?? 'neutral'
}

export function moderationStatusTone(s: string): Tone {
  return MODERATION_TONES[s as ModerationStatus] ?? 'neutral'
}

export function topUpStatusTone(s: string): Tone {
  return TOPUP_TONES[s as TopUpStatus] ?? 'neutral'
}

export function isActiveOrderStatus(s: string): boolean {
  return (ACTIVE_ORDER_STATUSES as readonly string[]).includes(s)
}
