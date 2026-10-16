// Wire types of the driver Mini App API (admin/backend/api/openapi.yaml and the
// Go handlers). Field names are the API's snake_case; amounts are integer KZT.

import type { Locale } from '../utils/format'
import type { Category, ModerationStatus, OrderStatus, PaymentMethod, TopUpStatus } from '../utils/status'

export type { Category, ModerationStatus, OrderStatus, PaymentMethod, TopUpStatus }

export interface LocalizedText {
  kk: string
  ru: string
  en: string
}

export interface CityRef {
  id: string
  code: string
  name: LocalizedText
}

export interface CursorList<T> {
  items: T[]
  next_cursor: string | null
}

// --- Files ----------------------------------------------------------------

export type UploadKind = 'avatar' | 'id_front' | 'id_back' | 'vehicle_main' | 'vehicle_back' | 'vehicle_side' | 'vehicle_plate' | 'vehicle_extra'

export interface FileRef {
  id: string
  kind: string
  /** /api/v1/driver/files/{id}; fetch with the Authorization header. */
  url: string
  width: number
  height: number
  created_at: string
}

export type VehiclePhotoKind = 'main' | 'back' | 'side' | 'plate' | 'extra'

export interface VehiclePhoto extends FileRef {
  kind: VehiclePhotoKind
}

// --- Registration ---------------------------------------------------------

export const REGISTRATION_FIELDS = [
  'avatar',
  'first_name',
  'last_name',
  'phone',
  'id_front',
  'id_back',
  'vehicle_make_model',
  'vehicle_color',
  'vehicle_plate',
  'vehicle_photos',
] as const
export type RegistrationField = (typeof REGISTRATION_FIELDS)[number]

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
  comment: string | null
}

export interface RegistrationVehicle {
  make: string
  model: string
  color: string
  plate: string
  plate_normalized: string | null
  photos: VehiclePhoto[]
}

export interface RegistrationHistoryItem {
  action: string
  from_status: ModerationStatus | null
  to_status: ModerationStatus | null
  comment: string | null
  corrections: CorrectionItem[]
  created_at: string
}

export interface DriverRegistration {
  id: string | null
  status: ModerationStatus
  code: string | null
  city: CityRef | null
  first_name: string
  last_name: string
  phone: string | null
  phone_verified: boolean
  avatar: FileRef | null
  documents: { id_front: FileRef | null; id_back: FileRef | null }
  vehicle: RegistrationVehicle
  corrections: CorrectionItem[]
  correction_comment: string | null
  rejection_reason: string | null
  missing_fields: RegistrationField[]
  editable: boolean
  can_submit: boolean
  submitted_at: string | null
  history: RegistrationHistoryItem[]
  pd_consent_at: string | null
  pd_consent_version: string | null
}

export interface VehicleInput {
  make: string
  model: string
  color: string
  plate: string
}

export interface DriverRegistrationUpdate {
  first_name: string
  last_name: string
  vehicle: VehicleInput
}

export interface PlateCheck {
  input: string
  normalized: string | null
  display: string | null
  valid: boolean
  available: boolean | null
}

// --- Profile and availability ----------------------------------------------

export interface Vehicle {
  make: string
  model: string
  color: string
  plate: string
}

export interface DriverProfile {
  id: string
  code: string
  status: ModerationStatus
  first_name: string
  last_name: string
  phone: string | null
  phone_verified: boolean
  avatar: FileRef | null
  city: CityRef
  vehicle: Vehicle | null
  rating: number | null
  rating_count: number
  completed_trips: number
  is_online: boolean
  online_since: string | null
  approved_at: string | null
  suspended_reason: string | null
  created_at: string
}

export type AvailabilityBlock = 'not_approved' | 'suspended' | 'insufficient_balance'

export interface Availability {
  is_online: boolean
  online_since: string | null
  can_go_online: boolean
  blocked_reason: AvailabilityBlock | null
  available: number
  min_online_balance: number
  currency: 'KZT'
}

export interface LocationInput {
  lat: number
  lon: number
  accuracy_m?: number
  heading?: number
  speed_mps?: number
  recorded_at?: string
}

// --- Orders -----------------------------------------------------------------

export type StopKind = 'pickup' | 'intermediate' | 'destination'

export interface StopSummary {
  seq: number
  kind: StopKind
  lat: number
  lon: number
  address: string
}

export interface Stop extends StopSummary {
  id: string
  address_details: string | null
  completed_at: string | null
}

export type EligibilityReason =
  | 'not_approved'
  | 'offline'
  | 'insufficient_balance'
  | 'has_active_order'
  | 'location_required'
  | 'location_stale'

export interface Eligibility {
  eligible: boolean
  reason: EligibilityReason | null
}

export interface DriverOrderCard {
  id: string
  number: number
  category: Category
  service_type: 'passenger' | 'delivery'
  cargo_type: 'none' | 'parcel' | 'other'
  stops: StopSummary[]
  stop_count: number
  comment: string | null
  cargo_description: string | null
  fare: number
  currency: 'KZT'
  commission_amount: number
  payment_method: PaymentMethod
  distance_m: number
  duration_s: number
  distance_to_pickup_m: number | null
  created_at: string
  search_expires_at: string | null
}

export interface AvailableOrders {
  items: DriverOrderCard[]
  eligibility: Eligibility
}

export interface LineString {
  type: 'LineString'
  coordinates: Array<[number, number]>
}

export interface RouteSnapshot {
  geometry: LineString
  legs: Array<{ distance_m: number; duration_s: number }>
  bbox: [number, number, number, number]
  distance_m: number
  duration_s: number
}

export interface OrderPassenger {
  first_name: string
  phone: string | null
  /** Telegram username without "@", when the backend provides it. */
  username?: string | null
}

export type ActorType = 'passenger' | 'driver' | 'admin' | 'system'

export interface OrderEvent {
  from_status: OrderStatus | null
  to_status: OrderStatus
  actor_type: ActorType
  reason: string | null
  created_at: string
}

export interface DriverOrderDetail {
  id: string
  number: number
  status: OrderStatus
  is_mine: boolean
  category: Category
  service_type: 'passenger' | 'delivery'
  cargo_type: 'none' | 'parcel' | 'other'
  cargo_description: string | null
  recipient_phone: string | null
  comment: string | null
  fare: number
  currency: 'KZT'
  commission_bps: number
  commission_amount: number
  payment_method: PaymentMethod
  distance_m: number
  duration_s: number
  distance_to_pickup_m: number | null
  stop_count: number
  stops: Stop[]
  route: RouteSnapshot | null
  passenger: OrderPassenger | null
  next_stop_id: string | null
  events: OrderEvent[]
  rating: { rating: number; comment: string | null; created_at: string } | null
  cancel_reason: string | null
  cancelled_by: ActorType | null
  can_accept: boolean
  can_arrive: boolean
  can_start: boolean
  can_complete: boolean
  can_cancel: boolean
  version: number
  created_at: string
  search_expires_at: string | null
  accepted_at: string | null
  arrived_at: string | null
  started_at: string | null
  completed_at: string | null
  cancelled_at: string | null
}

export interface DriverOrderSummary {
  id: string
  number: number
  status: OrderStatus
  category: Category
  fare: number
  currency: 'KZT'
  commission_amount: number
  payment_method: PaymentMethod
  pickup: StopSummary
  destination: StopSummary
  stop_count: number
  distance_m: number
  created_at: string
  completed_at: string | null
  cancelled_at: string | null
  cancelled_by: ActorType | null
}

// --- Wallet and top-ups ------------------------------------------------------

export interface Wallet {
  balance: number
  reserved: number
  available: number
  currency: 'KZT'
  min_online_balance: number
  updated_at: string
}

export type LedgerEntryType =
  | 'topup_credit'
  | 'topup_refund'
  | 'commission_reserve'
  | 'commission_release'
  | 'commission_charge'
  | 'commission_refund'
  | 'adjustment_credit'
  | 'adjustment_debit'

export interface LedgerEntry {
  id: number
  entry_type: LedgerEntryType
  balance_delta: number
  reserved_delta: number
  balance_after: number
  reserved_after: number
  currency: 'KZT'
  order_id: string | null
  order_number: number | null
  topup_id: string | null
  topup_reference: string | null
  description: string | null
  created_at: string
}

export interface PaymentInstructions {
  method: 'kaspi_manual'
  url: string | null
  recipient_name: string
  recipient_phone: string | null
  instructions: string
  reference: string
  amount: number
  currency: 'KZT'
}

export interface TopUp {
  id: string
  number: number
  reference: string
  amount: number
  verified_amount: number | null
  currency: 'KZT'
  status: TopUpStatus
  provider: 'kaspi_manual'
  payment: PaymentInstructions
  driver_payment_reference: string | null
  driver_note: string | null
  has_receipt: boolean
  can_upload_receipt: boolean
  can_submit: boolean
  can_cancel: boolean
  created_at: string
  driver_reported_paid_at: string | null
  expires_at: string
  confirmed_at: string | null
  rejected_at: string | null
  rejected_reason: string | null
  refunded_at: string | null
}

export interface TopUpSubmit {
  payment_reference?: string
  note?: string
}

/** Language codes shared with i18n. */
export type Language = Locale
