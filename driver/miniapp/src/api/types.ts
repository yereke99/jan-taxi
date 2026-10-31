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

/** The city or the nationwide region (code KAZ) whose tariff priced an order. */
export interface TariffRegion {
  code: string
  name: LocalizedText
}

/**
 * Where an order happens: the settlement of pickup A from reverse geocoding
 * (city, town or village, else district), null when unknown; and the region
 * whose tariff applied. Never coordinates.
 */
export interface OrderPlace {
  locality: string | null
  tariff_region: TariffRegion | null
}

export interface CursorList<T> {
  items: T[]
  next_cursor: string | null
}

// --- Files ----------------------------------------------------------------

/** The only registration upload: ID scans and car photos are no longer collected (§12). */
export type UploadKind = 'avatar'

export interface FileRef {
  id: string
  kind: string
  /** /api/v1/driver/files/{id}; fetch with the Authorization header. */
  url: string
  width: number
  height: number
  created_at: string
}

// --- Registration ---------------------------------------------------------

/**
 * Registration fields in form order (drivers.RegistrationFields). identity is
 * the IIN with the optional patronymic; id_front, id_back and vehicle_photos
 * belong to the retired document and car-photo steps and only appear in
 * remarks on applications from before.
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

/** What a registration needs before it is finished (drivers.RequiredFields). */
export const REQUIRED_FIELDS: readonly RegistrationField[] = [
  'avatar',
  'first_name',
  'last_name',
  'identity',
  'phone',
  'vehicle_class',
  'vehicle_make_model',
  'vehicle_color',
  'vehicle_plate',
]

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

/**
 * The vehicle class fields (ARCHITECTURE §21) of the driver's vehicle: all
 * null until the driver chose a class.
 */
export interface VehicleClassFields {
  vehicle_class: string | null
  vehicle_class_name: LocalizedText | null
  seats: number | null
  cargo_volume_l: number | null
}

export interface RegistrationVehicle extends VehicleClassFields {
  make: string
  model: string
  color: string
  plate: string
  plate_normalized: string | null
}

export interface RegistrationHistoryItem {
  action: string
  from_status: ModerationStatus | null
  to_status: ModerationStatus | null
  comment: string | null
  corrections: CorrectionItem[]
  created_at: string
}

/** The driver's own application (GET/PUT /driver/registration, POST /driver/register). */
export interface DriverRegistration {
  id: string | null
  status: ModerationStatus
  code: string | null
  city: CityRef | null
  first_name: string
  last_name: string
  /** Optional (Әкесінің аты); '' when not given. */
  patronymic: string
  /** The 12-digit IIN (ЖСН) as stored, shown only to its owner; null until entered. */
  iin: string | null
  phone: string | null
  phone_verified: boolean
  avatar: FileRef | null
  /** ID scans uploaded before they were retired; both null for newer drivers. */
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
  /** Omitted: the stored class stays. A new class takes its own seat and cargo defaults. */
  vehicle_class?: string
}

/**
 * PUT /driver/registration. A key that is not sent keeps the stored value;
 * an empty string clears it while drafting.
 */
export interface DriverRegistrationUpdate {
  first_name: string
  last_name: string
  patronymic?: string
  /** Twelve digits with a valid check digit; sent only when complete. */
  iin?: string
  vehicle: VehicleInput
  /**
   * Device position, sent only with the save that creates the profile: the
   * server takes the driver's city (and code prefix) from it, the nationwide
   * region (KAZ) outside every city zone.
   */
  location?: { lat: number; lon: number }
}

export interface PlateCheck {
  input: string
  normalized: string | null
  display: string | null
  valid: boolean
  available: boolean | null
}

// --- Profile and availability ----------------------------------------------

export interface Vehicle extends VehicleClassFields {
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
  patronymic: string
  /** "********1234"; null while the driver has not entered the IIN (PUT /driver/profile/iin). */
  iin_masked: string | null
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

export type AvailabilityBlock = 'not_approved' | 'suspended' | 'insufficient_balance' | 'location_required'

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

export const CARGO_SIZES = ['s', 'm', 'l', 'xl'] as const
export type CargoSize = (typeof CARGO_SIZES)[number]

/**
 * What an order asks for (ARCHITECTURE §21): the vehicle class, the
 * passenger count of a taxi order and the cargo size/volume of a parcel or
 * "other" order. The driver only sees orders their vehicle fits.
 */
export interface OrderLoad {
  vehicle_class: string
  vehicle_class_name: LocalizedText | null
  /** Taxi only, otherwise null. */
  passenger_count: number | null
  /** Parcel/other only, otherwise null. */
  cargo_size: CargoSize | null
  /** Litres; 0 for taxi. */
  cargo_volume_l: number
}

export type StopKind = 'pickup' | 'intermediate' | 'destination'

export interface StopSummary {
  mode?: 'geocoded' | 'manual'
  seq: number
  kind: StopKind
  lat: number | null
  lon: number | null
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

export interface DriverOrderCard extends OrderLoad, OrderPlace {
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
  distance_m: number | null
  duration_s: number | null
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
  distance_m: number | null
  duration_s: number | null
}

/**
 * The passenger as the assigned driver sees them while the order is active
 * (§22.4/§22.5): the phone only when they shared it through Telegram, and a
 * Telegram link (https://t.me/<username> or tg://user?id=<id>).
 */
export interface OrderPassenger {
  first_name: string
  /** E.164, only when verified through Telegram. */
  phone: string | null
  phone_verified: boolean
  telegram_username: string | null
  /** Validated: https://t.me/<username> or tg://user?id=<digits>; null otherwise. */
  telegram_url: string | null
}

export type ActorType = 'passenger' | 'driver' | 'admin' | 'system'

export interface OrderEvent {
  from_status: OrderStatus | null
  to_status: OrderStatus
  actor_type: ActorType
  reason: string | null
  created_at: string
}

export interface DriverOrderDetail extends OrderLoad, OrderPlace {
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
  /** What the wallet paid for this order (charges minus refunds); kept when the passenger cancels (§22.2). */
  commission_charged: number
  payment_method: PaymentMethod
  distance_m: number | null
  duration_s: number | null
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
  /** Always false: drivers cannot cancel (§22.3); they contact support. */
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

export interface DriverOrderSummary extends OrderLoad, OrderPlace {
  id: string
  number: number
  status: OrderStatus
  category: Category
  fare: number
  currency: 'KZT'
  commission_amount: number
  /** What the wallet paid for this order (charges minus refunds). */
  commission_charged: number
  payment_method: PaymentMethod
  pickup: StopSummary
  destination: StopSummary
  stop_count: number
  distance_m: number | null
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
  /** Welcome bonus on first approval or a manual bonus from Jan Taxi (§22.1, §22.7). */
  | 'bonus_credit'

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

/** Outcome of the automatic Kaspi receipt check (payments.DriverReceiptView). */
export type ReceiptDecision = 'auto_confirmed' | 'manual_review'

/**
 * The receipt attached to a top-up: a Kaspi PDF (checked automatically) or an
 * image (evidence for the administrator only). `reasons` are the review
 * reasons (amount_mismatch, receipt_too_old, …, duplicate_receipt, image_receipt).
 */
export interface TopUpReceipt {
  kind: 'pdf' | 'image'
  decision: ReceiptDecision | null
  reasons: string[]
  receipt_serial: string | null
  amount: number | null
  paid_at: string | null
}

export interface TopUp {
  id: string
  number: number
  reference: string
  amount: number
  verified_amount: number | null
  currency: 'KZT'
  status: TopUpStatus
  provider: string
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
  /** The latest receipt check; null without a receipt. */
  receipt: TopUpReceipt | null
}

export interface TopUpSubmit {
  payment_reference?: string
  note?: string
}

/** Language codes shared with i18n. */
export type Language = Locale
