// Admin drivers, moderation, files and live locations.

import { api } from '../services/api'
import type { ModerationStatus } from '../utils/status'
import type { AdminRef, CityRef, DriverRef, FileRef, OrderRef, Page, Vehicle, VehicleClassFields } from './common'
import { opt } from './common'
import type { AdminLedgerEntry } from './finance'
import type { AdminOrderListItem } from './orders'

/**
 * Registration fields named by corrections (OpenAPI RegistrationField), in
 * form order. identity is the IIN (ЖСН) with the optional patronymic.
 * id_front, id_back and vehicle_photos belong to the retired ID-scan and
 * vehicle-photo steps: older corrections still name them.
 */
export const REGISTRATION_FIELDS = [
  'avatar',
  'first_name',
  'last_name',
  'identity',
  'phone',
  'id_front',
  'id_back',
  'vehicle_class',
  'vehicle_make_model',
  'vehicle_color',
  'vehicle_plate',
  'vehicle_photos',
] as const
export type RegistrationField = (typeof REGISTRATION_FIELDS)[number]

/** Fields a driver can still change in the registration form: the only ones offered for new corrections. */
export const CORRECTABLE_FIELDS = [
  'avatar',
  'first_name',
  'last_name',
  'identity',
  'phone',
  'vehicle_class',
  'vehicle_make_model',
  'vehicle_color',
  'vehicle_plate',
] as const satisfies readonly RegistrationField[]
export type CorrectableField = (typeof CORRECTABLE_FIELDS)[number]

export const CORRECTION_REASONS = [
  'invalid_plate',
  'plate_mismatch',
  'duplicate_plate',
  'duplicate_phone',
  'id_unreadable',
  'vehicle_photo_unclear',
  'missing_info',
  'other',
] as const
export type CorrectionReason = (typeof CORRECTION_REASONS)[number]

export interface CorrectionItem {
  field: RegistrationField
  reason: CorrectionReason
  comment?: string | null
}

export interface VehiclePhoto {
  id: string
  kind: 'main' | 'back' | 'side' | 'plate' | 'extra'
  url: string
  width: number
  height: number
  created_at: string
}

export interface RegistrationVehicle extends Omit<Vehicle, keyof VehicleClassFields>, VehicleClassFields {
  plate_normalized: string | null
  /** Plate format detected by the API (admins only), e.g. "current". */
  plate_kind?: string | null
  photos: VehiclePhoto[]
}

export interface DriverApplication {
  id: string
  round: number
  status: 'pending' | 'approved' | 'changes_requested' | 'rejected'
  submitted_at: string
  reviewed_at: string | null
  reviewed_by: AdminRef | null
  decision_comment: string | null
  corrections: CorrectionItem[]
  snapshot: Record<string, unknown> | null
}

export type ModerationAction =
  | 'submitted'
  | 'resubmitted'
  | 'approved'
  | 'changes_requested'
  | 'rejected'
  | 'suspended'
  | 'restored'
  | 'reopened'
  | 'city_changed'

export interface ModerationEvent {
  id: number
  application_id: string | null
  action: ModerationAction
  actor_type: 'driver' | 'admin' | 'system'
  admin: AdminRef | null
  from_status: string | null
  to_status: string | null
  reasons: CorrectionItem[]
  comment: string | null
  created_at: string
}

export interface Wallet {
  balance: number
  reserved: number
  available: number
  currency: string
  min_online_balance: number
  updated_at: string
}

/** Welcome balance credited on the first approval (§22.1). */
export interface WelcomeBonus {
  amount: number
  granted_at: string
}

/** One bonus credited to a driver (OpenAPI DriverBonus), newest first on the detail. */
export interface DriverBonus {
  ledger_entry_id: number
  kind: 'welcome' | 'manual'
  amount: number
  comment: string | null
  admin_id: string | null
  admin_name: string | null
  created_at: string
}

export interface DriverStats {
  completed_trips: number
  accepted_orders: number
  cancelled_by_driver: number
  total_fares: number
  total_commission: number
}

export interface DriverLastLocation {
  lat: number
  lon: number
  accuracy_m: number | null
  heading: number | null
  speed_mps: number | null
  source: string
  recorded_at: string
  received_at: string
  /** Seconds between recorded_at and the response. */
  age_s: number
}

export interface AdminDriverListItem {
  id: string
  code: string
  status: ModerationStatus
  first_name: string
  last_name: string
  phone: string | null
  phone_verified: boolean
  city: CityRef
  is_online: boolean
  last_heartbeat_at: string | null
  vehicle: Vehicle | null
  rating: number | null
  rating_count: number
  completed_trips: number
  wallet_available: number | null
  submitted_at: string | null
  approved_at: string | null
  created_at: string
}

export interface AdminDriverDetail {
  id: string
  code: string
  status: ModerationStatus
  first_name: string
  last_name: string
  /** Optional (Әкесінің аты); '' when not given. */
  patronymic: string
  /** The IIN with all but the last four digits hidden ("********1234"); null until the driver entered one. */
  iin_masked: string | null
  phone: string | null
  phone_verified: boolean
  phone_verified_at: string | null
  city: CityRef | null
  is_online: boolean
  online_since: string | null
  last_heartbeat_at: string | null
  user: {
    id: string
    telegram_user_id: number
    username: string | null
    is_blocked: boolean
    driver_bot_started_at: string | null
    driver_bot_blocked_at: string | null
  }
  avatar: FileRef | null
  /**
   * ID scans uploaded before they were retired (both null for drivers who
   * registered later); null without documents.view.
   */
  documents: { id_front: FileRef | null; id_back: FileRef | null } | null
  /** The vehicle; photos exist only for drivers who registered before vehicle photos were retired. */
  vehicle: RegistrationVehicle | null
  corrections: CorrectionItem[]
  applications: DriverApplication[]
  moderation_events: ModerationEvent[]
  wallet: Wallet | null
  /** Null until granted; it never is when the bonus was off or the phone/plate already had it. */
  welcome_bonus: WelcomeBonus | null
  /** Up to 50 latest bonuses; the full history is the ledger filtered by bonus_credit. */
  bonuses: DriverBonus[]
  stats: DriverStats
  last_location: DriverLastLocation | null
  active_order: OrderRef | null
  rating: number | null
  rating_count: number
  completed_trips: number
  submitted_at: string | null
  approved_at: string | null
  suspended_at: string | null
  suspended_reason: string | null
  pd_consent_at: string | null
  pd_consent_version: string | null
  created_at: string
  updated_at: string
}

export interface DriverLocationItem {
  driver: DriverRef
  status: ModerationStatus
  is_online: boolean
  active_order: OrderRef | null
  location: DriverLastLocation
}

export interface DriverFilters {
  status?: string
  city_id?: string
  online?: string
  q?: string
}

export const driversApi = {
  list(f: DriverFilters, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminDriverListItem>> {
    return api.get('/admin/drivers', {
      query: { status: opt(f.status), city_id: opt(f.city_id), online: opt(f.online), q: opt(f.q?.trim()), page, page_size: pageSize },
      signal,
    })
  },
  get(id: string, signal?: AbortSignal): Promise<AdminDriverDetail> {
    return api.get(`/admin/drivers/${encodeURIComponent(id)}`, { signal })
  },
  orders(id: string, status: string, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<AdminOrderListItem>> {
    return api.get(`/admin/drivers/${encodeURIComponent(id)}/orders`, {
      query: { status: opt(status), page, page_size: pageSize },
      signal,
    })
  },
  locations(cityId: string | undefined, signal?: AbortSignal): Promise<{ items: DriverLocationItem[] }> {
    return api.get('/admin/drivers/locations', { query: { city_id: opt(cityId) }, signal })
  },
  approve(id: string, comment: string): Promise<AdminDriverDetail> {
    return api.post(`/admin/drivers/${encodeURIComponent(id)}/approve`, comment ? { comment } : {})
  },
  reject(id: string, reason: string, comment: string): Promise<AdminDriverDetail> {
    return api.post(`/admin/drivers/${encodeURIComponent(id)}/reject`, comment ? { reason, comment } : { reason })
  },
  requestCorrections(id: string, items: CorrectionItem[], comment: string): Promise<AdminDriverDetail> {
    return api.post(`/admin/drivers/${encodeURIComponent(id)}/request-corrections`, comment ? { items, comment } : { items })
  },
  suspend(id: string, reason: string): Promise<AdminDriverDetail> {
    return api.post(`/admin/drivers/${encodeURIComponent(id)}/suspend`, { reason })
  },
  restore(id: string): Promise<AdminDriverDetail> {
    return api.post(`/admin/drivers/${encodeURIComponent(id)}/restore`)
  },
  changeCity(id: string, cityId: string): Promise<AdminDriverDetail> {
    return api.patch(`/admin/drivers/${encodeURIComponent(id)}`, { city_id: cityId })
  },
  /**
   * Company bonus to one driver (finance.manage, §22.7). The idempotency key
   * makes a repeated submit return the stored entry (200) instead of paying twice.
   */
  grantBonus(id: string, body: { amount: number; comment?: string; idempotency_key: string }): Promise<BonusResult> {
    return api.post(`/admin/drivers/${encodeURIComponent(id)}/bonus`, body)
  },
}

/** The driver a bonus goes to (a list row or the detail page's driver). */
export interface BonusTarget {
  id: string
  code: string
  first_name: string
  last_name: string
  phone: string | null
  status?: ModerationStatus
  /** Current balance, for the "balance after" line. */
  balance?: number | null
}

/** POST /admin/drivers/{id}/bonus response: wallet after the credit and its ledger entry. */
export interface BonusResult {
  wallet: Wallet
  entry: AdminLedgerEntry
}
