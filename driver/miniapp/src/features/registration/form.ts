// Registration form rules (ARCHITECTURE §11): the two pages, field → page
// mapping, client-side checks mirroring the API (names ≤ 60 letters, the
// optional patronymic, a 12-digit IIN with its check digit, make/model ≤ 40,
// colour ≤ 30, the plate formats of platform/plate, an active vehicle class)
// and correction locking (in changes_requested only the flagged fields change,
// plus the IIN while none is stored).

import {
  REGISTRATION_FIELDS,
  type DriverRegistration,
  type DriverRegistrationUpdate,
  type RegistrationField,
  type VehicleInput,
} from '../../api/types'
import { codePointLength } from '../../utils/text'
import { findClass, type VehicleClassOption } from '../vehicle/classes'
import { iinProblem, isValidIin, type IinProblem } from './iin'

/** Page 1: photo, names, IIN and phone; page 2: the car and, last, the consent. */
export const STEP_IDS = ['personal', 'vehicle'] as const
export type StepId = (typeof STEP_IDS)[number]

/**
 * The page of every registration field. The retired ID scans and car photos
 * keep a page so remarks on applications from before are still shown.
 */
export const FIELD_STEP: Record<RegistrationField, StepId> = {
  avatar: 'personal',
  first_name: 'personal',
  last_name: 'personal',
  identity: 'personal',
  phone: 'personal',
  id_front: 'personal',
  id_back: 'personal',
  vehicle_class: 'vehicle',
  vehicle_make_model: 'vehicle',
  vehicle_color: 'vehicle',
  vehicle_plate: 'vehicle',
  vehicle_photos: 'vehicle',
}

export const LIMITS = { name: 60, patronymic: 60, make: 40, model: 40, color: 30, plate: 20 } as const

/** The body type a new registration starts with (vehicleclasses.Default). */
export const DEFAULT_CLASS = 'sedan'

export interface RegistrationForm {
  first_name: string
  last_name: string
  /** Optional (Әкесінің аты). */
  patronymic: string
  /** Digits only, as typed. */
  iin: string
  /** Vehicle class code; '' until chosen. Seats and cargo space follow the class. */
  vehicle_class: string
  make: string
  model: string
  color: string
  plate: string
}

export type FieldError = 'required' | 'too_long' | 'invalid'
export type FormErrors = Partial<Record<Exclude<keyof RegistrationForm, 'iin'>, FieldError>> & { iin?: IinProblem }

export function emptyForm(): RegistrationForm {
  return { first_name: '', last_name: '', patronymic: '', iin: '', vehicle_class: '', make: '', model: '', color: '', plate: '' }
}

export function formFromRegistration(r: DriverRegistration): RegistrationForm {
  return {
    first_name: r.first_name,
    last_name: r.last_name,
    patronymic: r.patronymic,
    iin: r.iin ?? '',
    vehicle_class: r.vehicle.vehicle_class ?? '',
    make: r.vehicle.make,
    model: r.vehicle.model,
    color: r.vehicle.color,
    plate: r.vehicle.plate,
  }
}

// --- Vehicle class (§21) ----------------------------------------------------------

/**
 * Checks the body type. A class that is no longer offered is accepted only
 * when it is the one already stored (the API keeps it), not as a new choice.
 */
export function validateVehicleClass(f: RegistrationForm, classes: readonly VehicleClassOption[], storedClass: string | null = null): FormErrors {
  if (!f.vehicle_class) return { vehicle_class: 'required' }
  if (classes.length > 0 && !findClass(classes, f.vehicle_class) && f.vehicle_class !== storedClass) return { vehicle_class: 'invalid' }
  return {}
}

/** The default body type when it is offered, else null (the driver picks one). */
export function defaultClass(classes: readonly VehicleClassOption[]): VehicleClassOption | null {
  return findClass(classes, DEFAULT_CLASS)
}

// --- Plate (admin/backend/internal/platform/plate) ------------------------------------

export type PlateKind = 'current' | 'legacy'

export interface ParsedPlate {
  /** As stored in vehicles.plate_normalized, e.g. "123ABC04" or "D123ABC". */
  normalized: string
  kind: PlateKind
  /** "04" for current plates, "D" for legacy ones. */
  region: string
}

/** Cyrillic capitals that look like the Latin plate letters. */
const LOOKALIKES: Readonly<Record<string, string>> = {
  А: 'A', В: 'B', Е: 'E', К: 'K', М: 'M', Н: 'H', О: 'O', Р: 'P', С: 'C', Т: 'T', У: 'Y', Х: 'X',
  Ү: 'Y', І: 'I', Ј: 'J', Ѕ: 'S', Ԛ: 'Q', Ԝ: 'W',
}

const SEPARATORS = new Set([' ', '\t', '-', '.', '_', '|', '/', ' ', '‐', '‑', '‒', '–', '—', '−'])

/** Current plates (since 2012): NNN LL(L) RR with region 01–20. */
const CURRENT_RE = /^(\d{3})([A-Z]{2,3})(0[1-9]|1\d|20)$/
/** Legacy plates (1993–2012), still valid: L NNN LL(L), the first letter is the region. */
const LEGACY_RE = /^([A-Z])(\d{3})([A-Z]{2,3})$/
const PLATE_INPUT_MAX = 32
const PLATE_CHAR_RE = /^[0-9A-Z]$/

/**
 * Full-width forms folded (NFKC), upper-cased one character at a time (as Go
 * does: "ß" stays "ß"), look-alikes mapped to Latin and separators dropped;
 * null on any other character.
 */
function canonicalPlate(s: string): string | null {
  let out = ''
  for (const ch of s.normalize('NFKC')) {
    const up = ch.toUpperCase()
    const single = [...up].length === 1 ? up : ch
    const c = LOOKALIKES[single] ?? single
    if (PLATE_CHAR_RE.test(c)) out += c
    else if (!SEPARATORS.has(c)) return null
  }
  return out
}

function matchPlate(s: string): ParsedPlate | null {
  const current = CURRENT_RE.exec(s)
  if (current) return { normalized: s, kind: 'current', region: current[3]! }
  const legacy = LEGACY_RE.exec(s)
  if (legacy) return { normalized: s, kind: 'legacy', region: legacy[1]! }
  return null
}

/** The plate as the API reads it, or null when it is not an accepted Kazakhstan plate. */
export function parsePlate(s: string): ParsedPlate | null {
  if ([...s].length > PLATE_INPUT_MAX) return null
  const canon = canonicalPlate(s)
  if (!canon) return null
  // Some drivers copy the "KZ" country mark printed on the plate.
  return matchPlate(canon) ?? (canon.startsWith('KZ') ? matchPlate(canon.slice(2)) : null)
}

/** The canonical form for comparisons while typing: the parsed plate, else the input without separators. */
export function normalizePlateInput(s: string): string {
  return parsePlate(s)?.normalized ?? canonicalPlate(s) ?? s.toUpperCase().replace(/[\s-]+/g, '')
}

/** "123 ABC 04" or "D 123 ABC" for an accepted plate, otherwise null. */
export function formatPlate(s: string): string | null {
  const p = parsePlate(s)
  if (!p) return null
  const m = (p.kind === 'current' ? CURRENT_RE : LEGACY_RE).exec(p.normalized)!
  return `${m[1]} ${m[2]} ${m[3]}`
}

export function isValidPlate(s: string): boolean {
  return parsePlate(s) !== null
}

// --- Validation -------------------------------------------------------------

/** Letters of any script with spaces, hyphens, apostrophes and dots (drivers.validName). */
const NAME_CHARS_RE = /^[\p{L}\p{M} '’ʼ.-]+$/u
const LETTER_RE = /\p{L}/u

function text(value: string, max: number, required: boolean): FieldError | undefined {
  const v = value.trim()
  if (!v) return required ? 'required' : undefined
  if (codePointLength(v) > max) return 'too_long'
  return undefined
}

function nameError(value: string, max: number, required: boolean): FieldError | undefined {
  const base = text(value, max, required)
  const v = value.trim()
  if (base || !v) return base
  return NAME_CHARS_RE.test(v) && LETTER_RE.test(v) ? undefined : 'invalid'
}

export function validateName(value: string): FieldError | undefined {
  return nameError(value, LIMITS.name, true)
}

/** The patronymic is optional; when given it follows the name rules. */
export function validatePatronymic(value: string): FieldError | undefined {
  return nameError(value, LIMITS.patronymic, false)
}

/** Names, patronymic and IIN of page 1 (the photo and the phone are checked by isStepComplete). */
export function validatePersonal(f: RegistrationForm): FormErrors {
  const e: FormErrors = {}
  const first = validateName(f.first_name)
  const last = validateName(f.last_name)
  const patronymic = validatePatronymic(f.patronymic)
  const iin = iinProblem(f.iin)
  if (first) e.first_name = first
  if (last) e.last_name = last
  if (patronymic) e.patronymic = patronymic
  if (iin) e.iin = iin
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

/**
 * Fields the driver may change now: all in draft; in changes_requested the
 * flagged ones, plus the IIN while none is stored (the API allows it, so an
 * application from before the IIN was asked for is never stuck).
 */
export function editableFields(r: Pick<DriverRegistration, 'status' | 'corrections' | 'editable' | 'id' | 'iin'>): Set<RegistrationField> {
  if (r.status === 'draft' || r.id === null) return new Set(REGISTRATION_FIELDS)
  if (r.status === 'changes_requested' && r.editable) {
    const out = new Set(r.corrections.map((c) => c.field))
    if (!r.iin) out.add('identity')
    return out
  }
  return new Set()
}

export function isCorrectionMode(r: Pick<DriverRegistration, 'status'>): boolean {
  return r.status === 'changes_requested'
}

/** Form fields belonging to each registration field (for locking inputs). */
const FORM_FIELD_OWNER: Record<keyof RegistrationForm, RegistrationField> = {
  first_name: 'first_name',
  last_name: 'last_name',
  patronymic: 'identity',
  iin: 'identity',
  vehicle_class: 'vehicle_class',
  make: 'vehicle_make_model',
  model: 'vehicle_make_model',
  color: 'vehicle_color',
  plate: 'vehicle_plate',
}

export function isFormFieldLocked(editable: ReadonlySet<RegistrationField>, field: keyof RegistrationForm): boolean {
  return !editable.has(FORM_FIELD_OWNER[field])
}

type TextField = 'first_name' | 'last_name' | 'make' | 'model' | 'color' | 'plate'

function clean(s: string): string {
  return s.trim().replace(/\s+/g, ' ')
}

/**
 * PUT /driver/registration body. A locked text field is sent with the value
 * the server holds (changing it would be refused with not_editable). The
 * patronymic is sent only while valid and the IIN only once all twelve digits
 * pass the check, so a half-typed value never fails the autosave; omitted, the
 * stored values stay. The class goes only when it changes: the API then gives
 * the vehicle the class's seats and cargo space.
 */
export function buildUpdate(r: DriverRegistration, f: RegistrationForm): DriverRegistrationUpdate {
  const editable = editableFields(r)
  const pick = (field: TextField, server: string): string => (isFormFieldLocked(editable, field) ? server : clean(f[field]))
  const vehicle: VehicleInput = {
    make: pick('make', r.vehicle.make),
    model: pick('model', r.vehicle.model),
    color: pick('color', r.vehicle.color),
    plate: pick('plate', r.vehicle.plate),
  }
  if (!isFormFieldLocked(editable, 'vehicle_class') && f.vehicle_class && f.vehicle_class !== r.vehicle.vehicle_class) {
    vehicle.vehicle_class = f.vehicle_class
  }
  const body: DriverRegistrationUpdate = { first_name: pick('first_name', r.first_name), last_name: pick('last_name', r.last_name), vehicle }
  if (!isFormFieldLocked(editable, 'patronymic') && !validatePatronymic(f.patronymic)) body.patronymic = clean(f.patronymic)
  if (!isFormFieldLocked(editable, 'iin') && isValidIin(f.iin)) body.iin = f.iin
  return body
}

export function sameUpdate(a: DriverRegistrationUpdate, b: DriverRegistrationUpdate): boolean {
  return (
    a.first_name === b.first_name &&
    a.last_name === b.last_name &&
    a.patronymic === b.patronymic &&
    a.iin === b.iin &&
    a.vehicle.make === b.vehicle.make &&
    a.vehicle.model === b.vehicle.model &&
    a.vehicle.color === b.vehicle.color &&
    a.vehicle.plate === b.vehicle.plate &&
    a.vehicle.vehicle_class === b.vehicle.vehicle_class
  )
}

// --- Pages --------------------------------------------------------------------

export interface StepContext {
  registration: DriverRegistration
  form: RegistrationForm
  /** Active vehicle classes from the public config (empty while unavailable). */
  classes?: readonly VehicleClassOption[]
}

/**
 * The phone is confirmed through Telegram, or not required: the API lists
 * phone in missing_fields only while driver.require_phone_verification is on.
 */
export function phoneDone(r: DriverRegistration): boolean {
  return r.phone_verified || (r.id !== null && !r.missing_fields.includes('phone'))
}

/** True when the page has everything it needs (client view; the server re-checks). */
export function isStepComplete(step: StepId, c: StepContext): boolean {
  const r = c.registration
  switch (step) {
    case 'personal':
      return r.avatar !== null && Object.keys(validatePersonal(c.form)).length === 0 && phoneDone(r)
    case 'vehicle':
      return (
        Object.keys(validateVehicleClass(c.form, c.classes ?? [], r.vehicle.vehicle_class)).length === 0 &&
        Object.keys(validateVehicle(c.form)).length === 0
      )
  }
}

/**
 * Pages shown: both for a new application; for corrections the pages with a
 * flagged or editable field (a missing IIN opens page 1), at least one.
 */
export function visibleSteps(r: DriverRegistration): StepId[] {
  if (!isCorrectionMode(r)) return [...STEP_IDS]
  const fields = [...r.corrections.map((c) => c.field), ...editableFields(r)]
  const steps = STEP_IDS.filter((s) => fields.some((f) => FIELD_STEP[f] === s))
  return steps.length > 0 ? steps : [STEP_IDS[0]]
}

/** Pages that hold the given server-side fields, in form order. */
export function stepsForFields(fields: readonly RegistrationField[]): StepId[] {
  const set = new Set(fields.map((f) => FIELD_STEP[f]))
  return STEP_IDS.filter((s) => set.has(s))
}

/** First visible page that still needs work, or the last one when everything is filled in. */
export function firstOpenStep(steps: readonly StepId[], c: StepContext): StepId {
  return steps.find((s) => !isStepComplete(s, c)) ?? steps[steps.length - 1] ?? STEP_IDS[0]
}
