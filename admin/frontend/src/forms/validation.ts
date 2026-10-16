// Client-side checks that mirror the API's validation, so operators see the
// problem before submitting. Errors use the API's field paths and reason
// codes ({"items.0.comment": "required"}), translated through field_errors.*.
// The server stays authoritative; its 422 fields are shown the same way.

import type { CorrectionItem, CorrectionReason, RegistrationField } from '../api/drivers'
import type { ExtraFee, TariffInput } from '../api/config'
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

// --- generic reason text --------------------------------------------------

/** Required free-text reason (cancel, reject, suspend, refund …). */
export function validateReason(text: string, max: number): string | null {
  return textProblem(text, max, true)
}

/** Clean form of a free-text value as the API stores it. */
export function cleanText(s: string): string {
  return cleaned(s)
}
