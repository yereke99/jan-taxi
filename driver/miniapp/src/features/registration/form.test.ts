import { describe, expect, it } from 'vitest'
import type { DriverRegistration, RegistrationField } from '../../api/types'
import { matchColor } from './colors'
import {
  buildUpdate,
  editableFields,
  firstOpenStep,
  formatPlate,
  formFromRegistration,
  isFormFieldLocked,
  isStepComplete,
  isValidPlate,
  normalizePlateInput,
  stepsForFields,
  validatePersonal,
  validateVehicle,
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

describe('correction locking', () => {
  const flagged = (fields: RegistrationField[]) =>
    registration({ status: 'changes_requested', corrections: fields.map((field) => ({ field, reason: 'other', comment: null })) })

  it('everything is editable in draft (and before the profile exists)', () => {
    expect(editableFields(registration()).size).toBe(10)
    expect(editableFields(registration({ id: null, status: 'draft' })).size).toBe(10)
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
  })

  it('shows only the steps with flagged fields, consent if not recorded, and review', () => {
    const r = flagged(['id_back', 'vehicle_color'])
    expect(visibleSteps(r, true)).toEqual(['documents', 'vehicle', 'review'])
    expect(visibleSteps(r, false)).toEqual(['documents', 'vehicle', 'consent', 'review'])
    expect(visibleSteps(registration(), false)).toEqual(['personal', 'phone', 'documents', 'vehicle', 'photos', 'consent', 'review'])
  })
})

describe('steps', () => {
  it('maps server missing_fields back to steps in form order', () => {
    expect(stepsForFields(['vehicle_photos', 'avatar', 'phone', 'vehicle_plate'])).toEqual(['personal', 'phone', 'vehicle', 'photos'])
  })

  it('opens the first incomplete step', () => {
    const r = registration({ phone_verified: false, documents: { id_front: null, id_back: null } })
    const ctx = { registration: r, form: formFromRegistration(r), consentGiven: false }
    expect(isStepComplete('personal', ctx)).toBe(true)
    expect(isStepComplete('phone', ctx)).toBe(false)
    expect(firstOpenStep(visibleSteps(r, false), ctx)).toBe('phone')
    const done = { registration: registration(), form: form(), consentGiven: true }
    expect(firstOpenStep(visibleSteps(registration(), false), done)).toBe('review')
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
