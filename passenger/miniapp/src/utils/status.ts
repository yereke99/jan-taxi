// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
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
