// Defensive mapping for payloads whose nested arrays/objects the UI iterates
// over. Unknown enum values degrade to safe defaults instead of crashing.

import { MODERATION_STATUSES, type ModerationStatus } from '../utils/status'
import { isUnnamedPlace } from '../utils/text'
import {
  CARGO_SIZES,
  CORRECTION_REASONS,
  REGISTRATION_FIELDS,
  type CargoSize,
  type CityRef,
  type CorrectionItem,
  type DriverRegistration,
  type FileRef,
  type LocalizedText,
  type OrderLoad,
  type OrderPassenger,
  type OrderPlace,
  type RegistrationField,
  type RegistrationHistoryItem,
  type TopUp,
  type TopUpReceipt,
  type VehicleClassFields,
} from './types'

export function rec(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
}

export function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

export function strOrNull(v: unknown): string | null {
  return typeof v === 'string' && v !== '' ? v : null
}

export function num(v: unknown, def = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : def
}

/** A non-negative integer, or null. */
export function intOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 ? v : null
}

/** A localized text with at least one language filled, or null. */
export function localizedOrNull(v: unknown): LocalizedText | null {
  const r = rec(v)
  const t = { kk: str(r.kk).trim(), ru: str(r.ru).trim(), en: str(r.en).trim() }
  return t.kk || t.ru || t.en ? t : null
}

function arr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : []
}

export function moderationStatus(v: unknown): ModerationStatus {
  return (MODERATION_STATUSES as readonly string[]).includes(str(v)) ? (str(v) as ModerationStatus) : 'draft'
}

export function isRegistrationField(v: unknown): v is RegistrationField {
  return (REGISTRATION_FIELDS as readonly string[]).includes(str(v))
}

export function normalizeCity(v: unknown): CityRef | null {
  const c = rec(v)
  if (!str(c.id)) return null
  const n = rec(c.name)
  return { id: str(c.id), code: str(c.code), name: { kk: str(n.kk), ru: str(n.ru), en: str(n.en) } }
}

export function normalizeFile(v: unknown): FileRef | null {
  const f = rec(v)
  if (!str(f.id) || !str(f.url)) return null
  return { id: str(f.id), kind: str(f.kind), url: str(f.url), width: num(f.width), height: num(f.height), created_at: str(f.created_at) }
}

const IIN_RE = /^\d{12}$/

/** The driver's own IIN as stored (twelve digits), null when absent or malformed. */
export function iinOrNull(v: unknown): string | null {
  const s = str(v)
  return IIN_RE.test(s) ? s : null
}

function normalizeCorrections(v: unknown): CorrectionItem[] {
  const out: CorrectionItem[] = []
  for (const raw of arr(v)) {
    const c = rec(raw)
    if (!isRegistrationField(c.field)) continue
    const reason = (CORRECTION_REASONS as readonly string[]).includes(str(c.reason)) ? (str(c.reason) as CorrectionItem['reason']) : 'other'
    out.push({ field: c.field, reason, comment: strOrNull(c.comment) })
  }
  return out
}

function normalizeHistory(v: unknown): RegistrationHistoryItem[] {
  return arr(v).map((raw) => {
    const h = rec(raw)
    return {
      action: str(h.action),
      from_status: h.from_status ? moderationStatus(h.from_status) : null,
      to_status: h.to_status ? moderationStatus(h.to_status) : null,
      comment: strOrNull(h.comment),
      corrections: normalizeCorrections(h.corrections),
      created_at: str(h.created_at),
    }
  })
}

/** The class fields of a vehicle object (registration, profile); null until a class is chosen. */
export function normalizeVehicleClassFields(v: unknown): VehicleClassFields {
  const r = rec(v)
  const code = strOrNull(r.vehicle_class)
  if (!code) return { vehicle_class: null, vehicle_class_name: null, seats: null, cargo_volume_l: null }
  return {
    vehicle_class: code,
    vehicle_class_name: localizedOrNull(r.vehicle_class_name),
    seats: intOrNull(r.seats),
    cargo_volume_l: intOrNull(r.cargo_volume_l),
  }
}

export function isCargoSize(v: unknown): v is CargoSize {
  return (CARGO_SIZES as readonly string[]).includes(str(v))
}

/** The load of an order view (§21); unknown or missing values read as "not given". */
export function normalizeOrderLoad(v: unknown): OrderLoad {
  const r = rec(v)
  const count = intOrNull(r.passenger_count)
  return {
    vehicle_class: str(r.vehicle_class),
    vehicle_class_name: localizedOrNull(r.vehicle_class_name),
    passenger_count: count !== null && count > 0 ? count : null,
    cargo_size: isCargoSize(r.cargo_size) ? r.cargo_size : null,
    cargo_volume_l: intOrNull(r.cargo_volume_l) ?? 0,
  }
}

/** Longest settlement name kept for display (the orders.locality limit). */
const LOCALITY_MAX = 120

/** The settlement of pickup A, trimmed; null when missing, too long or only coordinates. */
export function localityOrNull(v: unknown): string | null {
  const s = str(v).trim()
  if (isUnnamedPlace(s) || [...s].length > LOCALITY_MAX) return null
  return s
}

/** Locality and tariff region of an order view; anything malformed reads as unknown. */
export function normalizeOrderPlace(v: unknown): OrderPlace {
  const r = rec(v)
  const region = rec(r.tariff_region)
  const name = localizedOrNull(region.name)
  return {
    locality: localityOrNull(r.locality),
    tariff_region: str(region.code) && name ? { code: str(region.code), name } : null,
  }
}

const TG_USER_RE = /^tg:\/\/user\?id=\d{1,20}$/
const TG_USERNAME_RE = /^[A-Za-z][A-Za-z0-9_]{3,31}$/

/**
 * Only links that open a Telegram chat are kept: https://t.me/<username> or
 * tg://user?id=<digits>. Anything else is dropped rather than opened.
 */
export function safeTelegramUrl(v: unknown): string | null {
  const s = str(v).trim()
  if (TG_USER_RE.test(s)) return s
  try {
    const u = new URL(s)
    const name = u.pathname.replace(/^\//, '')
    if (u.protocol === 'https:' && u.hostname === 't.me' && TG_USERNAME_RE.test(name) && !u.search && !u.hash) return `https://t.me/${name}`
  } catch {
    // Not a URL.
  }
  return null
}

/** DriverOrderDetail.passenger: null when withheld. */
export function normalizePassenger(v: unknown): OrderPassenger | null {
  if (!v || typeof v !== 'object') return null
  const r = rec(v)
  const verified = r.phone_verified === true || (r.phone_verified === undefined && strOrNull(r.phone) !== null)
  const username = strOrNull(r.telegram_username)
  return {
    first_name: str(r.first_name),
    phone: verified ? strOrNull(r.phone) : null,
    phone_verified: verified,
    telegram_username: username && TG_USERNAME_RE.test(username.replace(/^@/, '')) ? username.replace(/^@/, '') : null,
    telegram_url: safeTelegramUrl(r.telegram_url),
  }
}

/** GET/PUT /driver/registration and POST /driver/register. */
export function normalizeRegistration(raw: unknown): DriverRegistration {
  const r = rec(raw)
  const docs = rec(r.documents)
  const v = rec(r.vehicle)
  return {
    id: strOrNull(r.id),
    status: moderationStatus(r.status),
    code: strOrNull(r.code),
    city: normalizeCity(r.city),
    first_name: str(r.first_name),
    last_name: str(r.last_name),
    patronymic: str(r.patronymic),
    iin: iinOrNull(r.iin),
    phone: strOrNull(r.phone),
    phone_verified: r.phone_verified === true,
    avatar: normalizeFile(r.avatar),
    documents: { id_front: normalizeFile(docs.id_front), id_back: normalizeFile(docs.id_back) },
    vehicle: {
      make: str(v.make),
      model: str(v.model),
      color: str(v.color),
      plate: str(v.plate),
      plate_normalized: strOrNull(v.plate_normalized),
      ...normalizeVehicleClassFields(v),
    },
    corrections: normalizeCorrections(r.corrections),
    correction_comment: strOrNull(r.correction_comment),
    rejection_reason: strOrNull(r.rejection_reason),
    missing_fields: arr(r.missing_fields).filter(isRegistrationField),
    editable: r.editable === true,
    can_submit: r.can_submit === true,
    submitted_at: strOrNull(r.submitted_at),
    history: normalizeHistory(r.history),
    pd_consent_at: strOrNull(r.pd_consent_at),
    pd_consent_version: strOrNull(r.pd_consent_version),
  }
}

/** The receipt check of a top-up; anything malformed reads as "no receipt". */
export function normalizeReceipt(v: unknown): TopUpReceipt | null {
  const r = rec(v)
  if (r.kind !== 'pdf' && r.kind !== 'image') return null
  const decision = r.decision === 'auto_confirmed' || r.decision === 'manual_review' ? r.decision : null
  return {
    kind: r.kind,
    decision,
    reasons: arr(r.reasons).filter((x): x is string => typeof x === 'string' && x !== ''),
    receipt_serial: strOrNull(r.receipt_serial),
    amount: typeof r.amount === 'number' && Number.isFinite(r.amount) ? r.amount : null,
    paid_at: strOrNull(r.paid_at),
  }
}

/** A top-up as returned by the API, with `receipt` always present (null when absent). */
export function normalizeTopUp(raw: unknown): TopUp {
  const t = rec(raw) as unknown as TopUp
  return { ...t, receipt: normalizeReceipt((raw as { receipt?: unknown } | null)?.receipt) }
}
