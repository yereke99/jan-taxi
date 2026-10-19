// Client-side checks that mirror the API's validation, so operators see the
// problem before submitting. Errors use the API's field paths and reason
// codes ({"items.0.comment": "required"}), translated through field_errors.*.
// The server stays authoritative; its 422 fields are shown the same way.

import type { CorrectionItem, CorrectionReason, RegistrationField } from '../api/drivers'
import type { ExtraFee, TariffInput } from '../api/config'
import { VEHICLE_CLASS_ICONS, type VehicleClassInput } from '../api/vehicleClasses'
import type { Category } from '../utils/status'
import { codePoints } from '../utils/text'
import { parseInteger, percentToBps } from './numbers'

export type FieldErrors = Record<string, string>

/** Limits shared with the Go handlers (code points / KZT). */
export const LIMITS = {
  correctionComment: 200,
  correctionItems: 10,
  decisionComment: 500,
  rejectReason: 500,
  suspendReason: 300,
  orderCancelReason: 200,
  disputeReason: 500,
  disputeResolution: 1000,
  topUpReason: 300,
  topUpNote: 300,
  topUpPayerInfo: 200,
  transactionId: 100,
  maxVerifiedAmount: 10_000_000,
  maxAdjustment: 10_000_000,
  adjustmentReason: 300,
  tariffMoney: 10_000_000,
  tariffOptions: 500,
  tariffExtraFees: 10,
  feeName: 60,
  maxCommissionBps: 5000,
  className: 40,
  classExamples: 80,
  classSeats: 60,
  classCargoL: 100_000,
  classServes: 10,
  classSort: 1000,
  maxBonus: 1_000_000,
  bonusComment: 200,
} as const

/** Same normalisation as the API (§11) for counting: trim + collapse spaces. */
function cleaned(s: string | null | undefined): string {
  return (s ?? '').replace(/[\r\n\t]+/g, ' ').replace(/\s+/g, ' ').trim()
}

function textProblem(s: string, max: number, required: boolean): string | null {
  const v = cleaned(s)
  if (!v) return required ? 'required' : null
  return codePoints(v) > max ? 'too_long' : null
}

// --- moderation corrections ----------------------------------------------

export interface CorrectionRow {
  field: RegistrationField
  reason: CorrectionReason | null
  comment: string
}

export interface CorrectionForm {
  items: CorrectionRow[]
  comment: string
}

/** Validates the request-corrections form; reason "other" needs a comment. */
export function validateCorrectionForm(form: CorrectionForm): { errors: FieldErrors; items: CorrectionItem[]; comment: string | null } {
  const errors: FieldErrors = {}
  if (form.items.length === 0) errors.items = 'too_few'
  else if (form.items.length > LIMITS.correctionItems) errors.items = 'too_many'
  const seen = new Set<string>()
  const items: CorrectionItem[] = []
  form.items.forEach((row, i) => {
    const p = `items.${i}.`
    if (!row.reason) errors[`${p}reason`] = 'required'
    const commentProblem = textProblem(row.comment, LIMITS.correctionComment, row.reason === 'other')
    if (commentProblem) errors[`${p}comment`] = commentProblem
    const key = `${row.field}/${row.reason ?? ''}`
    if (row.reason && seen.has(key)) errors[`${p}field`] = 'duplicate'
    seen.add(key)
    if (row.reason) {
      const c = cleaned(row.comment)
      items.push({ field: row.field, reason: row.reason, comment: c || null })
    }
  })
  const general = textProblem(form.comment, LIMITS.decisionComment, false)
  if (general) errors.comment = general
  return { errors, items, comment: cleaned(form.comment) || null }
}

// --- top-up confirmation -------------------------------------------------

export interface TopUpConfirmForm {
  verifiedAmount: string
  transactionId: string
  /** datetime-local value (YYYY-MM-DDTHH:mm) in the operator's time zone. */
  paidAt: string
  payerInfo: string
  note: string
}

export interface TopUpConfirmPayload {
  verified_amount: number
  provider_transaction_id: string
  paid_at: string
  payer_info?: string
  note?: string
}

/** Tolerance for clock differences, as on the server. */
const PAID_AT_SKEW_MS = 10 * 60 * 1000

/** Parses a datetime-local value as local time; null when malformed. */
export function parseLocalDateTime(v: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(v.trim())
  if (!m) return null
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]), Number(m[6] ?? 0))
  return Number.isNaN(d.getTime()) ? null : d
}

/** RFC 3339 instant → datetime-local value (YYYY-MM-DDTHH:mm) in local time; '' when invalid. */
export function toLocalDateTimeInput(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

/** Kaspi transaction/receipt numbers are compared without spaces and case. */
export function normalizeTransactionId(s: string): string {
  return s.split(/\s+/).join('').toUpperCase()
}

export function validateTopUpConfirm(form: TopUpConfirmForm, now: Date = new Date()): { errors: FieldErrors; payload: TopUpConfirmPayload | null } {
  const errors: FieldErrors = {}
  const amount = parseInteger(form.verifiedAmount)
  if (form.verifiedAmount.trim() === '') errors.verified_amount = 'required'
  else if (amount === null) errors.verified_amount = 'integer'
  else if (amount < 1) errors.verified_amount = 'positive'
  else if (amount > LIMITS.maxVerifiedAmount) errors.verified_amount = 'too_large'

  const txn = normalizeTransactionId(form.transactionId)
  if (!txn) errors.provider_transaction_id = 'required'
  else if (codePoints(txn) > LIMITS.transactionId) errors.provider_transaction_id = 'too_long'
  else if (/[\u0000-\u001f\u007f]/.test(txn)) errors.provider_transaction_id = 'invalid'

  const paidAt = form.paidAt ? parseLocalDateTime(form.paidAt) : null
  if (!form.paidAt) errors.paid_at = 'required'
  else if (!paidAt) errors.paid_at = 'invalid'
  else if (paidAt.getTime() > now.getTime() + PAID_AT_SKEW_MS) errors.paid_at = 'in_future'

  const payer = textProblem(form.payerInfo, LIMITS.topUpPayerInfo, false)
  if (payer) errors.payer_info = payer
  const note = textProblem(form.note, LIMITS.topUpNote, false)
  if (note) errors.note = note

  if (Object.keys(errors).length > 0 || amount === null || !paidAt) return { errors, payload: null }
  const payload: TopUpConfirmPayload = { verified_amount: amount, provider_transaction_id: txn, paid_at: paidAt.toISOString() }
  if (cleaned(form.payerInfo)) payload.payer_info = cleaned(form.payerInfo)
  if (cleaned(form.note)) payload.note = cleaned(form.note)
  return { errors, payload }
}

// --- wallet adjustment ---------------------------------------------------

export interface AdjustmentForm {
  direction: 'credit' | 'debit'
  amount: string
  reason: string
}

export function validateAdjustment(form: AdjustmentForm): { errors: FieldErrors; amount: number | null; reason: string } {
  const errors: FieldErrors = {}
  const n = parseInteger(form.amount)
  if (form.amount.trim() === '') errors.amount = 'required'
  else if (n === null) errors.amount = 'integer'
  else if (n <= 0) errors.amount = 'positive'
  else if (n > LIMITS.maxAdjustment) errors.amount = 'too_large'
  const reason = textProblem(form.reason, LIMITS.adjustmentReason, true)
  if (reason) errors.reason = reason
  const signed = n !== null && n > 0 ? (form.direction === 'debit' ? -n : n) : null
  return { errors, amount: Object.keys(errors).length ? null : signed, reason: cleaned(form.reason) }
}

// --- tariffs ---------------------------------------------------------------

export interface FeeRow {
  code: string
  nameKk: string
  nameRu: string
  nameEn: string
  amount: string
}

export interface TariffForm {
  city_id: string
  category: Category
  vehicle_class: string
  min_fare: string
  max_fare: string
  fare_step: string
  base_fare: string
  per_km: string
  per_minute: string
  extra_stop_fee: string
  /** Percent, e.g. "10" for 1000 bps. */
  commission_percent: string
  extra_fees: FeeRow[]
}

export const DEFAULT_FARE_STEP = 50
const FEE_CODE_RE = /^[a-z0-9_]{1,32}$/

function money(errors: FieldErrors, name: string, raw: string, min: number, required: boolean): number {
  if (raw.trim() === '') {
    if (required) errors[name] = 'required'
    return 0
  }
  const n = parseInteger(raw)
  if (n === null) errors[name] = 'integer'
  else if (n < min) errors[name] = 'too_small'
  else if (n > LIMITS.tariffMoney) errors[name] = 'too_large'
  return n ?? 0
}

/** Same rules as POST /admin/tariffs: min/max are multiples of the step, max ≥ min, ≤ 500 fare options. */
export function validateTariffForm(form: TariffForm): { errors: FieldErrors; payload: TariffInput | null } {
  const errors: FieldErrors = {}
  if (!form.city_id) errors.city_id = 'required'
  if (!form.vehicle_class) errors.vehicle_class = 'required'
  const min = money(errors, 'min_fare', form.min_fare, 1, true)
  const max = money(errors, 'max_fare', form.max_fare, 1, true)
  const step = money(errors, 'fare_step', form.fare_step, 1, true)
  const base = money(errors, 'base_fare', form.base_fare, 0, false)
  const perKm = money(errors, 'per_km', form.per_km, 0, false)
  const perMin = money(errors, 'per_minute', form.per_minute, 0, false)
  const extraStop = money(errors, 'extra_stop_fee', form.extra_stop_fee, 0, false)

  if (!errors.min_fare && !errors.max_fare && max < min) errors.max_fare = 'less_than_min'
  if (!errors.fare_step) {
    if (!errors.min_fare && min % step !== 0) errors.min_fare = 'not_multiple_of_step'
    if (!errors.max_fare && max % step !== 0) errors.max_fare = 'not_multiple_of_step'
    if (!errors.min_fare && !errors.max_fare && (max - min) / step + 1 > LIMITS.tariffOptions) errors.fare_step = 'too_small'
  }

  let bps = 0
  if (form.commission_percent.trim() === '') errors.commission_bps = 'required'
  else {
    const v = percentToBps(form.commission_percent)
    if (v === null) errors.commission_bps = 'invalid'
    else if (v < 0) errors.commission_bps = 'too_small'
    else if (v > LIMITS.maxCommissionBps) errors.commission_bps = 'too_large'
    else bps = v
  }

  if (form.extra_fees.length > LIMITS.tariffExtraFees) errors.extra_fees = 'too_many'
  const seen = new Set<string>()
  const fees: ExtraFee[] = form.extra_fees.map((f, i) => {
    const p = `extra_fees.${i}`
    const code = f.code.trim()
    if (!code) errors[`${p}.code`] = 'required'
    else if (!FEE_CODE_RE.test(code)) errors[`${p}.code`] = 'invalid'
    else if (seen.has(code)) errors[`${p}.code`] = 'duplicate'
    seen.add(code)
    const name = { kk: cleaned(f.nameKk), ru: cleaned(f.nameRu), en: cleaned(f.nameEn) }
    for (const lang of ['kk', 'ru', 'en'] as const) {
      const problem = textProblem(name[lang], LIMITS.feeName, true)
      if (problem) errors[`${p}.name.${lang}`] = problem
    }
    const amount = money(errors, `${p}.amount`, f.amount, 1, true)
    return { code, name, amount }
  })

  if (Object.keys(errors).length > 0) return { errors, payload: null }
  return {
    errors,
    payload: {
      city_id: form.city_id,
      category: form.category,
      vehicle_class: form.vehicle_class,
      min_fare: min,
      max_fare: max,
      fare_step: step,
      base_fare: base,
      per_km: perKm,
      per_minute: perMin,
      extra_stop_fee: extraStop,
      extra_fees: fees,
      commission_bps: bps,
    },
  }
}

// --- vehicle classes (§21) ---------------------------------------------------

export interface VehicleClassForm {
  code: string
  nameKk: string
  nameRu: string
  nameEn: string
  examples: string
  seats_default: string
  seats_max: string
  cargo_volume_l_default: string
  passenger_capable: boolean
  cargo_capable: boolean
  icon: string
  /** Other classes whose orders this class may take (the class itself is implied). */
  serves: string[]
  sort: string
  is_active: boolean
}

export const CLASS_CODE_RE = /^[a-z_]{2,20}$/

function intIn(errors: FieldErrors, name: string, raw: string, lo: number, hi: number, required: boolean): number | null {
  if (raw.trim() === '') {
    if (required) errors[name] = 'required'
    return null
  }
  const n = parseInteger(raw)
  if (n === null) errors[name] = 'integer'
  else if (n < lo) errors[name] = 'too_small'
  else if (n > hi) errors[name] = 'too_large'
  return n !== null && n >= lo && n <= hi ? n : null
}

/**
 * Same rules as POST/PATCH /admin/vehicle-classes: code ^[a-z_]{2,20}$ (new
 * classes only), names ≤ 40 in every language, seats 1..60 with the default
 * not above the maximum, cargo 0..100 000 L, passengers or cargo (at least
 * one), a known icon, ≤ 10 served classes, sort 0..1000.
 */
export function validateVehicleClassForm(form: VehicleClassForm, creating: boolean): { errors: FieldErrors; payload: VehicleClassInput | null } {
  const errors: FieldErrors = {}
  const code = form.code.trim()
  if (creating) {
    if (!code) errors.code = 'required'
    else if (!CLASS_CODE_RE.test(code)) errors.code = 'invalid'
  }
  const name = { kk: cleaned(form.nameKk), ru: cleaned(form.nameRu), en: cleaned(form.nameEn) }
  for (const lang of ['kk', 'ru', 'en'] as const) {
    const problem = textProblem(name[lang], LIMITS.className, true)
    if (problem) errors[`name.${lang}`] = problem
  }
  const examples = textProblem(form.examples, LIMITS.classExamples, false)
  if (examples) errors.examples = examples
  const seatsMax = intIn(errors, 'seats_max', form.seats_max, 1, LIMITS.classSeats, true)
  const seatsDefault = intIn(errors, 'seats_default', form.seats_default, 1, LIMITS.classSeats, true)
  if (seatsMax !== null && seatsDefault !== null && seatsDefault > seatsMax) errors.seats_default = 'too_large'
  const cargo = intIn(errors, 'cargo_volume_l_default', form.cargo_volume_l_default, 0, LIMITS.classCargoL, true)
  if (!form.passenger_capable && !form.cargo_capable) {
    errors.passenger_capable = 'one_required'
    errors.cargo_capable = 'one_required'
  }
  if (!form.icon) errors.icon = 'required'
  else if (!(VEHICLE_CLASS_ICONS as readonly string[]).includes(form.icon)) errors.icon = 'invalid'
  const serves = [...new Set(form.serves.map((s) => s.trim()).filter((s) => s && s !== code))]
  if (serves.length + 1 > LIMITS.classServes) errors.serves = 'too_many'
  else if (serves.some((s) => !CLASS_CODE_RE.test(s))) errors.serves = 'invalid'
  const sort = form.sort.trim() === '' ? 100 : intIn(errors, 'sort', form.sort, 0, LIMITS.classSort, false)
  if (errors.sort === 'too_small' || errors.sort === 'too_large') errors.sort = 'out_of_range'

  if (Object.keys(errors).length > 0 || seatsMax === null || seatsDefault === null || cargo === null || sort === null) return { errors, payload: null }
  const payload: VehicleClassInput = {
    name,
    examples: cleaned(form.examples),
    seats_default: seatsDefault,
    seats_max: seatsMax,
    cargo_volume_l_default: cargo,
    passenger_capable: form.passenger_capable,
    cargo_capable: form.cargo_capable,
    serves,
    icon: form.icon,
    sort,
    is_active: form.is_active,
  }
  if (creating) payload.code = code
  return { errors, payload }
}

// --- bonuses and commission (§22.7) -------------------------------------------

export interface BonusForm {
  amount: string
  comment: string
}

/** POST /admin/drivers/{id}/bonus: whole tenge 1..1 000 000, optional comment ≤ 200. */
export function validateBonus(form: BonusForm): { errors: FieldErrors; amount: number | null; comment: string } {
  const errors: FieldErrors = {}
  const n = parseInteger(form.amount)
  if (form.amount.trim() === '') errors.amount = 'required'
  else if (n === null) errors.amount = 'integer'
  else if (n < 1) errors.amount = 'positive'
  else if (n > LIMITS.maxBonus) errors.amount = 'too_large'
  const comment = textProblem(form.comment, LIMITS.bonusComment, false)
  if (comment) errors.comment = comment
  return { errors, amount: Object.keys(errors).length ? null : n, comment: cleaned(form.comment) }
}

/** A setting amount in [min, max] (e.g. commission.fixed_amount within 50..100 ₸); null when fine. */
export function amountInRange(raw: string, min: number, max: number): string | null {
  if (raw.trim() === '') return 'required'
  const n = parseInteger(raw)
  if (n === null) return 'integer'
  return n < min || n > max ? 'out_of_range' : null
}

// --- generic reason text --------------------------------------------------

/** Required free-text reason (cancel, reject, suspend, refund …). */
export function validateReason(text: string, max: number): string | null {
  return textProblem(text, max, true)
}

/** Clean form of a free-text value as the API stores it. */
export function cleanText(s: string): string {
  return cleaned(s)
}
