// Registration form rules: steps, field → step mapping, client-side checks
// mirroring the API (names ≤ 60, make/model ≤ 40, colour ≤ 30, KZ plate) and
// correction locking (in changes_requested only the flagged fields change).

import type { DriverRegistration, DriverRegistrationUpdate, RegistrationField } from '../../api/types'
import { codePointLength } from '../../utils/text'

export const STEP_IDS = ['personal', 'phone', 'documents', 'vehicle', 'photos', 'consent', 'review'] as const
export type StepId = (typeof STEP_IDS)[number]

export const FIELD_STEP: Record<RegistrationField, StepId> = {
  avatar: 'personal',
  first_name: 'personal',
  last_name: 'personal',
  phone: 'phone',
  id_front: 'documents',
  id_back: 'documents',
  vehicle_make_model: 'vehicle',
  vehicle_color: 'vehicle',
  vehicle_plate: 'vehicle',
  vehicle_photos: 'photos',
}

export const LIMITS = { name: 60, make: 40, model: 40, color: 30, plate: 20 } as const

/** Extra vehicle photos the app lets the driver add besides the main one. */
export const MAX_EXTRA_PHOTOS = 4

export interface RegistrationForm {
  first_name: string
  last_name: string
  make: string
  model: string
  color: string
  plate: string
}

export type FieldError = 'required' | 'too_long' | 'invalid'
export type FormErrors = Partial<Record<keyof RegistrationForm, FieldError>>

export function formFromRegistration(r: DriverRegistration): RegistrationForm {
  return {
    first_name: r.first_name,
    last_name: r.last_name,
    make: r.vehicle.make,
    model: r.vehicle.model,
    color: r.vehicle.color,
    plate: r.vehicle.plate,
  }
}

// --- Plate ------------------------------------------------------------------

const CYRILLIC_LOOKALIKES: Record<string, string> = {
  А: 'A', В: 'B', Е: 'E', К: 'K', М: 'M', Н: 'H', О: 'O', Р: 'P', С: 'C', Т: 'T', У: 'Y', Х: 'X',
}

const PLATE_RE = /^(\d{3})([A-Z]{2,3})(0[1-9]|1\d|20)$/

/** Uppercase, spaces/dashes removed, Cyrillic look-alikes → Latin (as the API does). */
export function normalizePlateInput(s: string): string {
  let out = s
    .toUpperCase()
    .replace(/[\s-]+/g, '')
    .replace(/./gu, (ch) => CYRILLIC_LOOKALIKES[ch] ?? ch)
  if (out.startsWith('KZ') && out.length > 8) out = out.slice(2)
  return out
}

/** "123 ABC 04" for a valid plate in the current format, otherwise null. */
export function formatPlate(s: string): string | null {
  const m = PLATE_RE.exec(normalizePlateInput(s))
  return m ? `${m[1]} ${m[2]} ${m[3]}` : null
}

export function isValidPlate(s: string): boolean {
  return formatPlate(s) !== null
}

// --- Validation -------------------------------------------------------------

const NAME_RE = /^\p{L}[\p{L}\p{M}' .-]*$/u

function text(value: string, max: number, required: boolean): FieldError | undefined {
  const v = value.trim()
  if (!v) return required ? 'required' : undefined
  if (codePointLength(v) > max) return 'too_long'
  return undefined
}

export function validateName(value: string): FieldError | undefined {
  const base = text(value, LIMITS.name, true)
  if (base) return base
  return NAME_RE.test(value.trim()) ? undefined : 'invalid'
}

export function validatePersonal(f: RegistrationForm): FormErrors {
  const e: FormErrors = {}
  const first = validateName(f.first_name)
  const last = validateName(f.last_name)
  if (first) e.first_name = first
  if (last) e.last_name = last
  return e
}

export function validateVehicle(f: RegistrationForm): FormErrors {
  const e: FormErrors = {}
  const make = text(f.make, LIMITS.make, true)
  const model = text(f.model, LIMITS.model, true)
  const color = text(f.color, LIMITS.color, true)
  if (make) e.make = make
  if (model) e.model = model
  if (color) e.color = color
  if (!f.plate.trim()) e.plate = 'required'
  else if (codePointLength(f.plate.trim()) > LIMITS.plate) e.plate = 'too_long'
  else if (!isValidPlate(f.plate)) e.plate = 'invalid'
  return e
}

// --- Correction locking ------------------------------------------------------

/** Fields the driver may change now: all in draft, the flagged ones in changes_requested. */
export function editableFields(r: Pick<DriverRegistration, 'status' | 'corrections' | 'editable' | 'id'>): Set<RegistrationField> {
  if (r.status === 'draft' || r.id === null) {
    return new Set(Object.keys(FIELD_STEP) as RegistrationField[])
  }
  if (r.status === 'changes_requested' && r.editable) return new Set(r.corrections.map((c) => c.field))
  return new Set()
}

export function isCorrectionMode(r: Pick<DriverRegistration, 'status'>): boolean {
  return r.status === 'changes_requested'
}

/** Form fields belonging to each registration field (for locking text inputs). */
const FORM_FIELD_OWNER: Record<keyof RegistrationForm, RegistrationField> = {
  first_name: 'first_name',
  last_name: 'last_name',
  make: 'vehicle_make_model',
  model: 'vehicle_make_model',
  color: 'vehicle_color',
  plate: 'vehicle_plate',
}

export function isFormFieldLocked(editable: ReadonlySet<RegistrationField>, field: keyof RegistrationForm): boolean {
  return !editable.has(FORM_FIELD_OWNER[field])
}

/**
 * PUT /driver/registration body. The endpoint replaces all text fields, so a
 * locked field is always sent with the value the server holds (changing it
 * would be rejected with fields.<name> = not_editable).
 */
export function buildUpdate(r: DriverRegistration, f: RegistrationForm): DriverRegistrationUpdate {
  const editable = editableFields(r)
  const pick = (field: keyof RegistrationForm, server: string): string =>
    isFormFieldLocked(editable, field) ? server : f[field].trim().replace(/\s+/g, ' ')
  return {
    first_name: pick('first_name', r.first_name),
    last_name: pick('last_name', r.last_name),
    vehicle: {
      make: pick('make', r.vehicle.make),
      model: pick('model', r.vehicle.model),
      color: pick('color', r.vehicle.color),
      plate: pick('plate', r.vehicle.plate),
    },
  }
}

export function sameUpdate(a: DriverRegistrationUpdate, b: DriverRegistrationUpdate): boolean {
  return (
    a.first_name === b.first_name &&
    a.last_name === b.last_name &&
    a.vehicle.make === b.vehicle.make &&
    a.vehicle.model === b.vehicle.model &&
    a.vehicle.color === b.vehicle.color &&
    a.vehicle.plate === b.vehicle.plate
  )
}

// --- Steps --------------------------------------------------------------------

export interface StepContext {
  registration: DriverRegistration
  form: RegistrationForm
  consentGiven: boolean
}

export function hasMainPhoto(r: DriverRegistration): boolean {
  return r.vehicle.photos.some((p) => p.kind === 'main')
}

export function extraPhotos(r: DriverRegistration): DriverRegistration['vehicle']['photos'] {
  return r.vehicle.photos.filter((p) => p.kind === 'extra')
}

/** True when the step has everything it needs (client view; the server re-checks). */
export function isStepComplete(step: StepId, c: StepContext): boolean {
  const r = c.registration
  switch (step) {
    case 'personal':
      return r.avatar !== null && Object.keys(validatePersonal(c.form)).length === 0
    case 'phone':
      return r.phone_verified
    case 'documents':
      return r.documents.id_front !== null && r.documents.id_back !== null
    case 'vehicle':
      return Object.keys(validateVehicle(c.form)).length === 0
    case 'photos':
      return hasMainPhoto(r)
    case 'consent':
      return c.consentGiven
    case 'review':
      return STEP_IDS.filter((s) => s !== 'review').every((s) => isStepComplete(s, c))
  }
}

/** Steps shown in this mode: all of them for a new application, flagged ones for corrections. */
export function visibleSteps(r: DriverRegistration, consentRecorded: boolean): StepId[] {
  if (!isCorrectionMode(r)) return [...STEP_IDS]
  const flagged = new Set(r.corrections.map((c) => FIELD_STEP[c.field]))
  const steps = STEP_IDS.filter((s) => flagged.has(s))
  if (!consentRecorded) steps.push('consent')
  steps.push('review')
  return steps
}

/** Steps that hold the given server-side missing fields, in form order. */
export function stepsForFields(fields: readonly RegistrationField[]): StepId[] {
  const set = new Set(fields.map((f) => FIELD_STEP[f]))
  return STEP_IDS.filter((s) => set.has(s))
}

/** First visible step that still needs work, or review when everything is done. */
export function firstOpenStep(steps: readonly StepId[], c: StepContext): StepId {
  return steps.find((s) => s !== 'review' && !isStepComplete(s, c)) ?? 'review'
}
