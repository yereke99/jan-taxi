import { describe, expect, it } from 'vitest'
import type { DriverRegistration, RegistrationField } from '../../api/types'
import type { VehicleClassOption } from '../vehicle/classes'
import { matchColor } from './colors'
import {
  buildUpdate,
  defaultClass,
  editableFields,
  firstOpenStep,
  formatPlate,
  formFromRegistration,
  isFormFieldLocked,
  isStepComplete,
  isValidPlate,
  normalizePlateInput,
  parsePlate,
  sameUpdate,
  stepsForFields,
  validatePersonal,
  validateVehicle,
  validateVehicleClass,
  visibleSteps,
  type RegistrationForm,
} from './form'

const IIN = '900101300017'

function registration(over: Partial<DriverRegistration> = {}): DriverRegistration {
  return {
    id: 'd1',
    status: 'draft',
    code: 'EMB001',
    city: null,
    first_name: 'Әлі',
    last_name: 'Серіков',
    patronymic: 'Ахметұлы',
    iin: IIN,
    phone: '+77011234567',
    phone_verified: true,
    avatar: { id: 'f1', kind: 'avatar', url: '/api/v1/driver/files/f1', width: 1, height: 1, created_at: '' },
    documents: { id_front: null, id_back: null },
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

describe('plates (admin/backend/internal/platform/plate)', () => {
  it.each([
    ['123 АВС 04', '123ABC04', 'current', '04'], // Cyrillic look-alikes
    ['123 ABC 04', '123ABC04', 'current', '04'],
    ['123-abc-04', '123ABC04', 'current', '04'],
    ['777 кхм 02', '777KXM02', 'current', '02'],
    ['001 AB 01', '001AB01', 'current', '01'],
    ['555 ННН 20', '555HHH20', 'current', '20'],
    ['123 ҮВС 13', '123YBC13', 'current', '13'], // Kazakh Ү
    ['KZ 123 ABC 04', '123ABC04', 'current', '04'],
    ['КZ 123 ABC 04', '123ABC04', 'current', '04'], // Cyrillic К in the country mark
    ['１２３ ＡＢＣ ０４', '123ABC04', 'current', '04'], // full-width
    ['123–ABC–04', '123ABC04', 'current', '04'], // en dash
    ['123.ABC/04', '123ABC04', 'current', '04'],
    ['D 123 ABC', 'D123ABC', 'legacy', 'D'],
    ['d123ac', 'D123AC', 'legacy', 'D'],
    ['Е 456 КМ', 'E456KM', 'legacy', 'E'],
  ])('reads %s as %s', (input, normalized, kind, region) => {
    expect(parsePlate(input)).toEqual({ normalized, kind, region })
    expect(isValidPlate(input)).toBe(true)
    expect(normalizePlateInput(input)).toBe(normalized)
  })

  it.each([
    '',
    '   ',
    '123 ABC 00', // region 00 does not exist
    '123 ABC 21', // beyond Ulytau (20)
    '123 ABC 99',
    '12 ABC 04',
    '1234 ABC 04',
    '123 A 04',
    '123 ABCD 04',
    '123 ДЖЗ 04', // Cyrillic letters without a Latin look-alike
    '123 ABC 04 extra',
    'А123ВС77RUS',
    'D 12 ABC',
    '123ABC04!',
    '123 SSA 04'.replace('SS', 'ß'), // "ß" is not upper-cased to "SS"
    'KZ',
    'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789',
  ])('rejects %j', (input) => {
    expect(parsePlate(input)).toBeNull()
    expect(isValidPlate(input)).toBe(false)
  })

  it('formats current and legacy plates for display', () => {
    expect(formatPlate('123abc04')).toBe('123 ABC 04')
    expect(formatPlate('001AB01')).toBe('001 AB 01')
    expect(formatPlate('d123abc')).toBe('D 123 ABC')
    expect(formatPlate('garbage')).toBeNull()
  })

  it('compares unfinished input without separators', () => {
    expect(normalizePlateInput('123 ab')).toBe('123AB')
    expect(normalizePlateInput('123 ab!')).toBe('123AB!')
  })
})

describe('validation', () => {
  it('requires names made of letters (Kazakh included)', () => {
    expect(validatePersonal(form())).toEqual({})
    expect(validatePersonal(form({ first_name: ' ' }))).toEqual({ first_name: 'required' })
    expect(validatePersonal(form({ last_name: 'Smith1' }))).toEqual({ last_name: 'invalid' })
    expect(validatePersonal(form({ first_name: 'ә'.repeat(61) }))).toEqual({ first_name: 'too_long' })
    expect(validatePersonal(form({ last_name: "O'Connor-Әлі" }))).toEqual({})
    // iOS types the typographic apostrophe; the API accepts it.
    expect(validatePersonal(form({ last_name: 'O’Connor' }))).toEqual({})
    expect(validatePersonal(form({ first_name: '- .' }))).toEqual({ first_name: 'invalid' })
  })

  it('takes the patronymic as optional', () => {
    expect(validatePersonal(form({ patronymic: '' }))).toEqual({})
    expect(validatePersonal(form({ patronymic: 'Ахмет-ұлы' }))).toEqual({})
    expect(validatePersonal(form({ patronymic: 'Ахмет2' }))).toEqual({ patronymic: 'invalid' })
    expect(validatePersonal(form({ patronymic: 'а'.repeat(61) }))).toEqual({ patronymic: 'too_long' })
  })

  it('requires a valid IIN', () => {
    expect(validatePersonal(form({ iin: '' }))).toEqual({ iin: 'required' })
    expect(validatePersonal(form({ iin: '90010130001' }))).toEqual({ iin: 'length' })
    expect(validatePersonal(form({ iin: '900101300018' }))).toEqual({ iin: 'checksum' })
  })

  it('checks vehicle fields and the plate', () => {
    expect(validateVehicle(form())).toEqual({})
    expect(validateVehicle(form({ make: '', color: '', plate: '' }))).toEqual({ make: 'required', color: 'required', plate: 'required' })
    expect(validateVehicle(form({ plate: '12 AB 04' }))).toEqual({ plate: 'invalid' })
    expect(validateVehicle(form({ plate: 'D 123 ABC' }))).toEqual({})
    expect(validateVehicle(form({ model: 'x'.repeat(41) }))).toEqual({ model: 'too_long' })
  })
})

describe('body type', () => {
  it('reads the stored class into the form', () => {
    expect(form()).toMatchObject({ vehicle_class: 'sedan' })
    const none = registration({ vehicle: { ...registration().vehicle, vehicle_class: null, vehicle_class_name: null, seats: null, cargo_volume_l: null } })
    expect(formFromRegistration(none)).toMatchObject({ vehicle_class: '' })
  })

  it('starts new registrations with Sedan when it is offered', () => {
    expect(defaultClass(CLASSES)?.code).toBe('sedan')
    expect(defaultClass([vclass('minivan', 7, 8, 800)])).toBeNull()
  })

  it('needs an offered class, or the stored one', () => {
    expect(validateVehicleClass(form(), CLASSES)).toEqual({})
    expect(validateVehicleClass(form({ vehicle_class: '' }), CLASSES)).toEqual({ vehicle_class: 'required' })
    const f = form({ vehicle_class: 'universal' })
    expect(validateVehicleClass(f, CLASSES, 'universal')).toEqual({})
    expect(validateVehicleClass(f, CLASSES, 'sedan')).toEqual({ vehicle_class: 'invalid' })
    // Classes not loaded: the class cannot be checked.
    expect(validateVehicleClass(f, [])).toEqual({})
  })

  it('sends the class only when it changes (the API applies its seats and cargo space)', () => {
    expect(buildUpdate(registration(), form()).vehicle).not.toHaveProperty('vehicle_class')
    const body = buildUpdate(registration(), form({ vehicle_class: 'minivan' }))
    expect(body.vehicle).toEqual({ make: 'Toyota', model: 'Camry', color: 'Ақ', plate: '123 ABC 04', vehicle_class: 'minivan' })
    const none = registration({ vehicle: { ...registration().vehicle, vehicle_class: null } })
    expect(buildUpdate(none, form({ vehicle_class: 'sedan' })).vehicle.vehicle_class).toBe('sedan')
    expect(buildUpdate(none, form({ vehicle_class: '' })).vehicle).not.toHaveProperty('vehicle_class')
    expect(sameUpdate(body, buildUpdate(registration(), form({ vehicle_class: 'minivan' })))).toBe(true)
    expect(sameUpdate(body, buildUpdate(registration(), form()))).toBe(false)
  })
})

describe('the update body', () => {
  it('sends names, the patronymic and the IIN', () => {
    const body = buildUpdate(registration(), form({ first_name: '  Әлі  ', patronymic: ' Ахметұлы ' }))
    expect(body).toMatchObject({ first_name: 'Әлі', last_name: 'Серіков', patronymic: 'Ахметұлы', iin: IIN })
  })

  it('keeps a half-typed IIN or an invalid patronymic out of the autosave', () => {
    const r = registration({ iin: null, patronymic: '' })
    expect(buildUpdate(r, form({ iin: '9001' }))).not.toHaveProperty('iin')
    expect(buildUpdate(r, form({ iin: '900101300018' }))).not.toHaveProperty('iin')
    expect(buildUpdate(r, form({ iin: '000301500128' })).iin).toBe('000301500128')
    expect(buildUpdate(r, form({ patronymic: 'Ахмет2' }))).not.toHaveProperty('patronymic')
    expect(buildUpdate(r, form({ patronymic: '' })).patronymic).toBe('')
  })

  it('detects a changed IIN or patronymic for the autosave', () => {
    const r = registration()
    const before = buildUpdate(r, formFromRegistration(r))
    expect(sameUpdate(before, buildUpdate(r, form()))).toBe(true)
    expect(sameUpdate(before, buildUpdate(r, form({ iin: '000301500128' })))).toBe(false)
    expect(sameUpdate(before, buildUpdate(r, form({ patronymic: 'Серікұлы' })))).toBe(false)
  })
})

describe('correction locking', () => {
  const flagged = (fields: RegistrationField[], over: Partial<DriverRegistration> = {}) =>
    registration({ status: 'changes_requested', corrections: fields.map((field) => ({ field, reason: 'other', comment: null })), ...over })

  it('everything is editable in draft (and before the profile exists)', () => {
    expect(editableFields(registration()).size).toBe(12)
    expect(editableFields(registration({ id: null, status: 'draft' })).size).toBe(12)
  })

  it('only flagged fields are editable in changes_requested', () => {
    const e = editableFields(flagged(['vehicle_plate', 'vehicle_photos']))
    expect([...e].sort()).toEqual(['vehicle_photos', 'vehicle_plate'])
    expect(isFormFieldLocked(e, 'plate')).toBe(false)
    expect(isFormFieldLocked(e, 'make')).toBe(true)
    expect(isFormFieldLocked(e, 'first_name')).toBe(true)
    expect(isFormFieldLocked(e, 'iin')).toBe(true)
  })

  it('a missing IIN stays editable even when not flagged', () => {
    const e = editableFields(flagged(['vehicle_color'], { iin: null }))
    expect(isFormFieldLocked(e, 'iin')).toBe(false)
    expect(isFormFieldLocked(e, 'patronymic')).toBe(false)
    expect(isFormFieldLocked(e, 'first_name')).toBe(true)
  })

  it('nothing is editable while pending or approved', () => {
    expect(editableFields(registration({ status: 'pending', editable: false })).size).toBe(0)
    expect(editableFields(registration({ status: 'approved', editable: false })).size).toBe(0)
  })

  it('locked fields are sent with the server value; locked IIN, patronymic and class are left out', () => {
    const r = flagged(['vehicle_plate'])
    const typed = { first_name: 'Changed', make: 'Lada', plate: '777 KZ 01', iin: '000301500128', patronymic: 'Other', vehicle_class: 'minivan' }
    const body = buildUpdate(r, form(typed))
    expect(body.first_name).toBe('Әлі')
    expect(body.vehicle.make).toBe('Toyota')
    expect(body.vehicle.plate).toBe('777 KZ 01')
    expect(body).not.toHaveProperty('iin')
    expect(body).not.toHaveProperty('patronymic')
    expect(body.vehicle).not.toHaveProperty('vehicle_class')
  })

  it('shows only the pages with flagged fields, at least one', () => {
    expect(visibleSteps(registration())).toEqual(['personal', 'vehicle'])
    expect(visibleSteps(flagged(['vehicle_color']))).toEqual(['vehicle'])
    expect(visibleSteps(flagged(['avatar', 'vehicle_plate']))).toEqual(['personal', 'vehicle'])
    // A retired field keeps its remark visible on its page.
    expect(visibleSteps(flagged(['id_back']))).toEqual(['personal'])
    expect(visibleSteps(flagged(['vehicle_photos']))).toEqual(['vehicle'])
    // A missing IIN opens the personal page too.
    expect(visibleSteps(flagged(['vehicle_color'], { iin: null }))).toEqual(['personal', 'vehicle'])
    expect(visibleSteps(flagged([]))).toEqual(['personal'])
  })
})

describe('pages', () => {
  const ctx = (r: DriverRegistration, f: RegistrationForm = formFromRegistration(r)) => ({ registration: r, form: f, classes: CLASSES })

  it('maps server missing_fields back to pages in form order', () => {
    expect(stepsForFields(['vehicle_plate', 'avatar', 'phone'])).toEqual(['personal', 'vehicle'])
    expect(stepsForFields(['vehicle_class'])).toEqual(['vehicle'])
    expect(stepsForFields(['identity'])).toEqual(['personal'])
  })

  it('page 1 needs the photo, names, a valid IIN and the phone', () => {
    expect(isStepComplete('personal', ctx(registration()))).toBe(true)
    expect(isStepComplete('personal', ctx(registration({ avatar: null })))).toBe(false)
    expect(isStepComplete('personal', ctx(registration(), form({ iin: '900101300018' })))).toBe(false)
    expect(isStepComplete('personal', ctx(registration({ phone_verified: false, missing_fields: ['phone'] })))).toBe(false)
    // driver.require_phone_verification off: the API does not list the phone.
    expect(isStepComplete('personal', ctx(registration({ phone_verified: false, missing_fields: [] })))).toBe(true)
  })

  it('page 2 needs a body type and the car details', () => {
    expect(isStepComplete('vehicle', ctx(registration()))).toBe(true)
    expect(isStepComplete('vehicle', ctx(registration(), form({ vehicle_class: '' })))).toBe(false)
    expect(isStepComplete('vehicle', ctx(registration(), form({ plate: '123 AB' })))).toBe(false)
  })

  it('opens the first page that needs work, else the last one', () => {
    const r = registration({ avatar: null })
    expect(firstOpenStep(visibleSteps(r), ctx(r))).toBe('personal')
    const noPlate = registration({ vehicle: { ...registration().vehicle, plate: '' } })
    expect(firstOpenStep(visibleSteps(noPlate), ctx(noPlate))).toBe('vehicle')
    expect(firstOpenStep(visibleSteps(registration()), ctx(registration()))).toBe('vehicle')
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
