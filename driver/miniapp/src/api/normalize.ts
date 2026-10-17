// Defensive mapping for payloads whose nested arrays/objects the UI iterates
// over. Unknown enum values degrade to safe defaults instead of crashing.

import { MODERATION_STATUSES, type ModerationStatus } from '../utils/status'
import {
  CORRECTION_REASONS,
  REGISTRATION_FIELDS,
  type CityRef,
  type CorrectionItem,
  type DriverRegistration,
  type FileRef,
  type RegistrationField,
  type RegistrationHistoryItem,
  type TopUp,
  type TopUpReceipt,
  type VehiclePhoto,
  type VehiclePhotoKind,
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

const PHOTO_KINDS: readonly VehiclePhotoKind[] = ['main', 'back', 'side', 'plate', 'extra']

function normalizePhoto(v: unknown): VehiclePhoto | null {
  const f = normalizeFile(v)
  if (!f || !(PHOTO_KINDS as readonly string[]).includes(f.kind)) return null
  return { ...f, kind: f.kind as VehiclePhotoKind }
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
      photos: arr(v.photos)
        .map(normalizePhoto)
        .filter((p): p is VehiclePhoto => p !== null),
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
