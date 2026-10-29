import { describe, expect, it } from 'vitest'
import type { DriverRegistration, RegistrationField } from '../../api/types'
import type { VehicleClassOption } from '../vehicle/classes'
import { matchColor } from './colors'
import {
  applyClass,
  buildUpdate,
  editableFields,
  firstOpenStep,
  formatPlate,
  formFromRegistration,
  isFormFieldLocked,
  isStepComplete,
  isValidPlate,
  normalizePlateInput,
  parseLitres,
  sameUpdate,
  stepsForFields,
  validatePersonal,
  validateVehicle,
  validateVehicleClass,
  visibleSteps,
  type RegistrationForm,
} from './form'

function registration(over: Partial<DriverRegistration> = {}): DriverRegistration {
  return {
    id: 'd1',
    status: 'draft',
    code: 'EMB001',
    city: null,
    first_name: 'Әлі',
    last_name: 'Серіков',
    phone: '+77011234567',
    phone_verified: true,
    avatar: { id: 'f1', kind: 'avatar', url: '/api/v1/driver/files/f1', width: 1, height: 1, created_at: '' },
    documents: {
      id_front: { id: 'f2', kind: 'id_front', url: '/api/v1/driver/files/f2', width: 1, height: 1, created_at: '' },
      id_back: { id: 'f3', kind: 'id_back', url: '/api/v1/driver/files/f3', width: 1, height: 1, created_at: '' },
    },
    vehicle: {
      make: 'Toyota',
      model: 'Camry',
      color: 'Ақ',
      plate: '123 ABC 04',
      plate_normalized: '123ABC04',
      vehicle_class: 'sedan',
      vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
      seats: 4,
      cargo_volume_l: 400,
      photos: [{ id: 'f4', kind: 'main', url: '/api/v1/driver/files/f4', width: 1, height: 1, created_at: '' }],
    },
    corrections: [],
    correction_comment: null,
    rejection_reason: null,
    missing_fields: [],
    editable: true,
    can_submit: true,
    submitted_at: null,
    history: [],
    pd_consent_at: null,
    pd_consent_version: null,
    ...over,
  }
}

function form(over: Partial<RegistrationForm> = {}): RegistrationForm {
  return { ...formFromRegistration(registration()), ...over }
}

function vclass(code: string, seatsDefault: number, seatsMax: number, cargo: number): VehicleClassOption {
  return {
    code,
    name: { kk: code, ru: code, en: code },
    examples: '',
    icon: 'sedan',
    seats_default: seatsDefault,
    seats_max: seatsMax,
    cargo_volume_l_default: cargo,
    passenger_capable: true,
    cargo_capable: true,
  }
}

const CLASSES = [vclass('sedan', 4, 4, 400), vclass('minivan', 7, 8, 800), vclass('truck', 2, 2, 15000)]

describe('plates (KZ format 123 ABC 04)', () => {
  it.each([
    ['123 abc 04', '123ABC04'],
    ['123-AB-01', '123AB01'],
    ['123 АВС 04', '123ABC04'], // Cyrillic look-alikes
    ['KZ 123 ABC 04', '123ABC04'],
  ])('normalises %s → %s', (input, out) => {
    expect(normalizePlateInput(input)).toBe(out)
  })

  it('accepts the current format and region codes 01..20', () => {
    expect(isValidPlate('123 ABC 04')).toBe(true)
    expect(isValidPlate('777 KZ 20')).toBe(true)
    expect(isValidPlate('123 ABC 21')).toBe(false)
    expect(isValidPlate('123 ABC 00')).toBe(false)
    expect(isValidPlate('A123BC')).toBe(false)
    expect(formatPlate('123abc04')).toBe('123 ABC 04')
  })
})

describe('validation', () => {
  it('requires names made of letters (Kazakh included)', () => {
    expect(validatePersonal(form())).toEqual({})
    expect(validatePersonal(form({ first_name: ' ' }))).toEqual({ first_name: 'required' })
    expect(validatePersonal(form({ last_name: 'Smith1' }))).toEqual({ last_name: 'invalid' })
    expect(validatePersonal(form({ first_name: 'ә'.repeat(61) }))).toEqual({ first_name: 'too_long' })
    expect(validatePersonal(form({ last_name: "O'Connor-Әлі" }))).toEqual({})
  })

  it('checks vehicle fields and the plate', () => {
    expect(validateVehicle(form())).toEqual({})
    expect(validateVehicle(form({ make: '', color: '', plate: '' }))).toEqual({ make: 'required', color: 'required', plate: 'required' })
    expect(validateVehicle(form({ plate: '12 AB 04' }))).toEqual({ plate: 'invalid' })
    expect(validateVehicle(form({ model: 'x'.repeat(41) }))).toEqual({ model: 'too_long' })
  })
})

describe('vehicle class step', () => {
  it('reads the stored class, seats and cargo into the form', () => {
    expect(form()).toMatchObject({ vehicle_class: 'sedan', seats: 4, cargo_volume_l: '400' })
    const none = registration({ vehicle: { ...registration().vehicle, vehicle_class: null, vehicle_class_name: null, seats: null, cargo_volume_l: null } })
    expect(formFromRegistration(none)).toMatchObject({ vehicle_class: '', seats: null, cargo_volume_l: '' })
  })

  it('a chosen class brings its default seats and cargo space', () => {
    const f = form({ vehicle_class: '', seats: null, cargo_volume_l: '' })
    applyClass(f, CLASSES[1]!)
    expect(f).toMatchObject({ vehicle_class: 'minivan', seats: 7, cargo_volume_l: '800' })
    f.seats = 8
    applyClass(f, CLASSES[1]!) // the same class again keeps the driver's values
    expect(f.seats).toBe(8)
  })

  it('bounds seats by the class and cargo by the API maximum', () => {
    expect(validateVehicleClass(form(), CLASSES)).toEqual({})
    expect(validateVehicleClass(form({ vehicle_class: '' }), CLASSES)).toEqual({ vehicle_class: 'required' })
    expect(validateVehicleClass(form({ vehicle_class: 'minivan', seats: 8 }), CLASSES)).toEqual({})
    expect(validateVehicleClass(form({ vehicle_class: 'minivan', seats: 9 }), CLASSES)).toEqual({ seats: 'too_large' })
    expect(validateVehicleClass(form({ seats: 0 }), CLASSES)).toEqual({ seats: 'too_small' })
    expect(validateVehicleClass(form({ cargo_volume_l: '' }), CLASSES)).toEqual({ cargo_volume_l: 'required' })
    expect(validateVehicleClass(form({ cargo_volume_l: '1,5' }), CLASSES)).toEqual({ cargo_volume_l: 'invalid' })
    expect(validateVehicleClass(form({ cargo_volume_l: '100001' }), CLASSES)).toEqual({ cargo_volume_l: 'too_large' })
    expect(validateVehicleClass(form({ cargo_volume_l: '0' }), CLASSES)).toEqual({})
  })

  it('accepts a class no longer offered only when it is the stored one', () => {
    const f = form({ vehicle_class: 'universal' })
    expect(validateVehicleClass(f, CLASSES, 'universal')).toEqual({})
    expect(validateVehicleClass(f, CLASSES, 'sedan')).toEqual({ vehicle_class: 'invalid' })
    // Classes not loaded: the class cannot be checked, seats use the API limit.
    expect(validateVehicleClass(form({ seats: 40 }), [])).toEqual({})
  })

  it('parses litres the way drivers type them', () => {
    expect(parseLitres('1500')).toBe(1500)
    expect(parseLitres(' 1 500 л')).toBe(1500)
    expect(parseLitres('600 L')).toBe(600)
    expect(parseLitres('1.5')).toBeNull()
    expect(parseLitres('')).toBeNull()
  })

  it('sends the class fields only once chosen, and compares them for autosave', () => {
    const body = buildUpdate(registration(), form({ vehicle_class: 'minivan', seats: 8, cargo_volume_l: '1 000' }))
    expect(body.vehicle).toMatchObject({ vehicle_class: 'minivan', seats: 8, cargo_volume_l: 1000 })
    const none = buildUpdate(registration(), form({ vehicle_class: '', seats: null, cargo_volume_l: '' }))
    expect(none.vehicle).not.toHaveProperty('vehicle_class')
    expect(sameUpdate(body, buildUpdate(registration(), form({ vehicle_class: 'minivan', seats: 8, cargo_volume_l: '1000' })))).toBe(true)
    expect(sameUpdate(body, buildUpdate(registration(), form({ vehicle_class: 'minivan', seats: 7, cargo_volume_l: '1000' })))).toBe(false)
  })

  it('is complete with a class and valid capacity', () => {
    const ctx = (f: RegistrationForm) => ({ registration: registration(), form: f, consentGiven: true, classes: CLASSES })
    expect(isStepComplete('vehicle_class', ctx(form()))).toBe(true)
    expect(isStepComplete('vehicle_class', ctx(form({ vehicle_class: '' })))).toBe(false)
    expect(isStepComplete('vehicle_class', ctx(form({ seats: 5 })))).toBe(false)
  })
})

describe('correction locking', () => {
  const flagged = (fields: RegistrationField[]) =>
    registration({ status: 'changes_requested', corrections: fields.map((field) => ({ field, reason: 'other', comment: null })) })

  it('everything is editable in draft (and before the profile exists)', () => {
    expect(editableFields(registration()).size).toBe(12)
    expect(editableFields(registration({ id: null, status: 'draft' })).size).toBe(12)
  })

  it('preserves identity values, leading zeros and document type, and detects edits', () => {
    const r=registration({identity_enabled:true,identity:{iin:'999999999990',document_number:'001234567',document_type:'id_card',patronymic:''}})
    const f=formFromRegistration(r)
    const before=buildUpdate(r,f)
    expect(before.identity?.document_number).toBe('001234567')
    f.identity!.document_number='009876543'
    expect(sameUpdate(before,buildUpdate(r,f))).toBe(false)
    const locked=registration({...r,status:'changes_requested',corrections:[{field:'first_name',reason:'other',comment:null}]})
    expect(buildUpdate(locked,f).identity?.document_number).toBe('001234567')
  })

  it('only flagged fields are editable in changes_requested', () => {
    const r = flagged(['vehicle_plate', 'vehicle_photos'])
    const e = editableFields(r)
    expect([...e].sort()).toEqual(['vehicle_photos', 'vehicle_plate'])
    expect(isFormFieldLocked(e, 'plate')).toBe(false)
    expect(isFormFieldLocked(e, 'make')).toBe(true)
    expect(isFormFieldLocked(e, 'first_name')).toBe(true)
  })

  it('nothing is editable while pending or approved', () => {
    expect(editableFields(registration({ status: 'pending', editable: false })).size).toBe(0)
    expect(editableFields(registration({ status: 'approved', editable: false })).size).toBe(0)
  })

  it('locked fields are always sent with the server value', () => {
    const r = flagged(['vehicle_plate'])
    const body = buildUpdate(r, form({ first_name: 'Changed', make: 'Lada', plate: '777 KZ 01' }))
    expect(body.first_name).toBe('Әлі')
    expect(body.vehicle.make).toBe('Toyota')
    expect(body.vehicle.plate).toBe('777 KZ 01')
    // The class was not flagged: it is not sent, so the stored one stays.
    const classBody = buildUpdate(r, form({ vehicle_class: 'minivan', seats: 7 }))
    expect(classBody.vehicle).not.toHaveProperty('vehicle_class')
    expect(classBody.vehicle).not.toHaveProperty('seats')
  })

  it('a flagged vehicle_class unlocks class, seats and cargo together', () => {
    const e = editableFields(flagged(['vehicle_class']))
    expect(isFormFieldLocked(e, 'vehicle_class')).toBe(false)
    expect(isFormFieldLocked(e, 'seats')).toBe(false)
    expect(isFormFieldLocked(e, 'cargo_volume_l')).toBe(false)
    expect(isFormFieldLocked(e, 'make')).toBe(true)
    expect(visibleSteps(flagged(['vehicle_class']), true)).toEqual(['vehicle_class', 'review'])
  })

  it('shows only the steps with flagged fields, consent if not recorded, and review', () => {
    const r = flagged(['id_back', 'vehicle_color'])
    expect(visibleSteps(r, true)).toEqual(['documents', 'vehicle', 'review'])
    expect(visibleSteps(r, false)).toEqual(['documents', 'vehicle', 'consent', 'review'])
    expect(visibleSteps(registration(), false)).toEqual(['personal', 'phone', 'documents', 'vehicle_class', 'vehicle', 'photos', 'consent', 'review'])
  })
})

describe('steps', () => {
  it('maps server missing_fields back to steps in form order', () => {
    expect(stepsForFields(['vehicle_photos', 'avatar', 'phone', 'vehicle_plate'])).toEqual(['personal', 'phone', 'vehicle', 'photos'])
    expect(stepsForFields(['vehicle_plate', 'vehicle_class'])).toEqual(['vehicle_class', 'vehicle'])
  })

  it('opens the first incomplete step', () => {
    const r = registration({ phone_verified: false, documents: { id_front: null, id_back: null } })
    const ctx = { registration: r, form: formFromRegistration(r), consentGiven: false }
    expect(isStepComplete('personal', ctx)).toBe(true)
    expect(isStepComplete('phone', ctx)).toBe(false)
    expect(firstOpenStep(visibleSteps(r, false), ctx)).toBe('phone')
    const done = { registration: registration(), form: form(), consentGiven: true, classes: CLASSES }
    expect(firstOpenStep(visibleSteps(registration(), false), done)).toBe('review')
    const noClass = { ...done, form: form({ vehicle_class: '', seats: null, cargo_volume_l: '' }) }
    expect(firstOpenStep(visibleSteps(registration(), false), noClass)).toBe('vehicle_class')
    expect(isStepComplete('review', done)).toBe(true)
  })

  it('needs the main vehicle photo', () => {
    const r = registration({ vehicle: { ...registration().vehicle, photos: [] } })
    expect(isStepComplete('photos', { registration: r, form: form(), consentGiven: true })).toBe(false)
  })
})

describe('vehicle colours', () => {
  it('recognises a listed colour in any language', () => {
    expect(matchColor('Ақ')?.id).toBe('white')
    expect(matchColor('белый')?.id).toBe('white')
    expect(matchColor(' Black ')?.id).toBe('black')
    expect(matchColor('Чёрный')?.id).toBe('black')
    expect(matchColor('Черный')?.id).toBe('black')
    expect(matchColor('Вишнёвый металлик')).toBeNull()
    expect(matchColor('')).toBeNull()
  })
})
