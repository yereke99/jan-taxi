import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as api from '../api/driver'
import type { DriverRegistration } from '../api/types'
import { PD_CONSENT_VERSION } from '../features/registration/consent'
import { ApiError } from '../services/http'
import * as telegram from '../telegram'
import type { VehicleClassOption } from '../features/vehicle/classes'
import { fieldFromRequestKey, PHONE_POLL_MS, PHONE_WAIT_MS, submitProblem, useRegistrationStore } from './registration'
import { useSessionStore } from './session'

vi.mock('../api/driver', () => ({
  getRegistration: vi.fn(),
  saveRegistration: vi.fn(),
  submitRegistration: vi.fn(),
  checkPlate: vi.fn(),
  uploadRegistrationFile: vi.fn(),
}))
vi.mock('../telegram', () => ({ requestContact: vi.fn(), getLocation: vi.fn() }))

const mocked = vi.mocked(api)
const tg = vi.mocked(telegram)

const MINIVAN: VehicleClassOption = {
  code: 'minivan',
  name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' },
  examples: 'Toyota Alphard, Sienna',
  icon: 'minivan',
  seats_default: 7,
  seats_max: 8,
  cargo_volume_l_default: 800,
  passenger_capable: true,
  cargo_capable: true,
}

const SEDAN: VehicleClassOption = { ...MINIVAN, code: 'sedan', name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, examples: '', icon: 'sedan', seats_default: 4, seats_max: 4, cargo_volume_l_default: 400 }

const IIN = '900101300017'

function reg(over: Partial<DriverRegistration> = {}): DriverRegistration {
  return {
    id: 'd1',
    status: 'draft',
    code: 'EMB001',
    city: null,
    first_name: 'Әлі',
    last_name: 'Серіков',
    patronymic: '',
    iin: null,
    phone: null,
    phone_verified: false,
    avatar: null,
    documents: { id_front: null, id_back: null },
    vehicle: {
      make: '',
      model: '',
      color: '',
      plate: '',
      plate_normalized: null,
      vehicle_class: null,
      vehicle_class_name: null,
      seats: null,
      cargo_volume_l: null,
    },
    corrections: [],
    correction_comment: null,
    rejection_reason: null,
    missing_fields: [],
    editable: true,
    can_submit: false,
    submitted_at: null,
    history: [],
    pd_consent_at: null,
    pd_consent_version: null,
    ...over,
  }
}

describe('registration store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  describe('phone verification through the driver bot', () => {
    it('waits for the bot: polls every 3 s and finishes when the phone is verified', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      await store.load()
      tg.requestContact.mockResolvedValue('sent')

      await store.requestPhone()
      expect(store.phoneFlow).toBe('waiting')
      const calls = mocked.getRegistration.mock.calls.length

      await vi.advanceTimersByTimeAsync(PHONE_POLL_MS)
      expect(mocked.getRegistration.mock.calls.length).toBe(calls + 1)
      expect(store.phoneFlow).toBe('waiting')

      mocked.getRegistration.mockResolvedValue(reg({ phone: '+77011234567', phone_verified: true }))
      await vi.advanceTimersByTimeAsync(PHONE_POLL_MS)
      expect(store.phoneFlow).toBe('verified')
      expect(store.registration?.phone).toBe('+77011234567')

      const after = mocked.getRegistration.mock.calls.length
      await vi.advanceTimersByTimeAsync(PHONE_POLL_MS * 5)
      expect(mocked.getRegistration.mock.calls.length).toBe(after)
    })

    it('gives up after 60 s with a timeout state', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      await store.load()
      tg.requestContact.mockResolvedValue('sent')
      await store.requestPhone()
      await vi.advanceTimersByTimeAsync(PHONE_WAIT_MS + PHONE_POLL_MS)
      expect(store.phoneFlow).toBe('timeout')
      const calls = mocked.getRegistration.mock.calls.length
      await vi.advanceTimersByTimeAsync(PHONE_POLL_MS * 3)
      expect(mocked.getRegistration.mock.calls.length).toBe(calls)
    })

    it('explains a cancelled, failed or unsupported request', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      await store.load()
      tg.requestContact.mockResolvedValueOnce('cancelled')
      await store.requestPhone()
      expect(store.phoneFlow).toBe('cancelled')
      tg.requestContact.mockResolvedValueOnce('failed')
      await store.requestPhone()
      expect(store.phoneFlow).toBe('failed')
      tg.requestContact.mockResolvedValueOnce('unavailable')
      await store.requestPhone()
      expect(store.phoneFlow).toBe('unavailable')
    })

    it('sharing again waits for the new number or the realtime event', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ phone: '+77011234567', phone_verified: true }))
      await store.load()
      expect(store.phoneFlow).toBe('verified')

      tg.requestContact.mockResolvedValue('sent')
      await store.requestPhone()
      // The old number is still on file: keep waiting.
      expect(store.phoneFlow).toBe('waiting')

      // driver.phone_verified arrives (same number shared again).
      await store.onPhoneVerified()
      expect(store.phoneFlow).toBe('verified')
    })
  })

  describe('draft saving', () => {
    it('debounces typing into one PUT with the full body', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockImplementation(async (body) => reg({ first_name: body.first_name, last_name: body.last_name }))
      await store.load()
      store.form.first_name = 'Ә'
      store.touch()
      store.form.first_name = 'Әлихан'
      store.touch()
      await vi.advanceTimersByTimeAsync(1_000)
      expect(mocked.saveRegistration).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1_000)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.calls[0]![0]).toEqual({
        first_name: 'Әлихан',
        last_name: 'Серіков',
        patronymic: '',
        vehicle: { make: '', model: '', color: '', plate: '' },
      })
    })

    it('sends the IIN only once all twelve digits are valid, and never erases what is half-typed', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockImplementation(async (body) => reg({ first_name: body.first_name, iin: body.iin ?? null }))
      await store.load()
      store.form.iin = '9001013'
      store.form.first_name = 'Ерлан'
      store.touch()
      await vi.advanceTimersByTimeAsync(2_000)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.calls[0]![0]).not.toHaveProperty('iin')
      // The saved answer (and a later background refresh) leave the unfinished IIN on screen.
      expect(store.form.iin).toBe('9001013')
      mocked.getRegistration.mockResolvedValue(reg({ first_name: 'Ерлан' }))
      await store.refresh()
      expect(store.form.iin).toBe('9001013')
      store.form.iin = IIN
      store.touch()
      await vi.advanceTimersByTimeAsync(2_000)
      expect(mocked.saveRegistration.mock.calls[1]![0].iin).toBe(IIN)
    })

    it('does not send again an IIN or a plate the API refused as another driver\'s', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.checkPlate.mockResolvedValue(null)
      mocked.saveRegistration.mockRejectedValueOnce(new ApiError(409, 'iin_taken', 'x'))
      await store.load()
      store.form.iin = IIN
      store.touch()
      await expect(store.save()).rejects.toMatchObject({ code: 'iin_taken' })
      expect(store.taken.iin).toBe(IIN)
      mocked.saveRegistration.mockResolvedValue(reg({ last_name: 'Әлиев' }))
      store.form.last_name = 'Әлиев'
      store.touch()
      await store.save()
      expect(mocked.saveRegistration.mock.calls[1]![0]).not.toHaveProperty('iin')
      expect(mocked.saveRegistration.mock.calls[1]![0].last_name).toBe('Әлиев')

      mocked.saveRegistration.mockRejectedValueOnce(new ApiError(409, 'plate_taken', 'x'))
      store.form.plate = '123 abc 04'
      store.touch()
      await expect(store.save()).rejects.toMatchObject({ code: 'plate_taken' })
      expect(store.taken.plate).toBe('123ABC04')
      store.form.first_name = 'Ерлан'
      store.touch()
      await store.save()
      expect(mocked.saveRegistration.mock.calls.at(-1)![0].vehicle.plate).toBe('')
    })

    it('keeps what was typed while a save was in flight', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      let release: (r: DriverRegistration) => void = () => undefined
      mocked.saveRegistration.mockImplementationOnce(() => new Promise((res) => (release = res)))
      await store.load()
      store.form.first_name = 'Ерж'
      store.touch()
      const pending = store.save()
      await vi.advanceTimersByTimeAsync(0)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      store.form.first_name = 'Ержан'
      store.touch()
      release(reg({ first_name: 'Ерж' }))
      await pending
      expect(store.form.first_name).toBe('Ержан')
      mocked.saveRegistration.mockResolvedValueOnce(reg({ first_name: 'Ержан' }))
      await store.save()
      expect(mocked.saveRegistration.mock.calls[1]![0].first_name).toBe('Ержан')
    })

    it('does not send an unfinished plate', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      mocked.checkPlate.mockResolvedValue(null)
      await store.load()
      store.form.plate = '123 AB'
      await store.save(true)
      expect(mocked.saveRegistration.mock.calls[0]![0].vehicle.plate).toBe('')
    })

    it('opening the form or hiding the app without typing sends nothing (no profile is created)', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ id: null, code: null }))
      await store.load()
      await store.save()
      expect(mocked.saveRegistration).not.toHaveBeenCalled()
    })

    it('suggests the Telegram name for a new application and saves it with the first step', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ id: null, code: null, first_name: '', last_name: '' }))
      mocked.saveRegistration.mockImplementation(async (body) => reg({ first_name: body.first_name, last_name: body.last_name }))
      await store.load()
      store.prefillNames('Ержан', 'John2')
      expect(store.form.first_name).toBe('Ержан')
      expect(store.form.last_name).toBe('') // not a valid name: left for the driver
      tg.getLocation.mockRejectedValue(new Error('denied'))
      await store.save(false, true)
      expect(mocked.saveRegistration.mock.calls[0]![0].first_name).toBe('Ержан')
    })

    it('the first "Next" creates the profile with the device position (city and code prefix)', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ id: null, code: null }))
      mocked.saveRegistration.mockResolvedValue(reg({ code: 'KAZ001' }))
      tg.getLocation.mockResolvedValue({ lat: 43.23891234, lon: 76.88971234, accuracy_m: 12, heading: null, speed_mps: null, timestamp: Date.now(), source: 'telegram' })
      await store.load()
      store.form.first_name = 'Ержан'
      store.touch()
      // Background saves never create the profile: typing waits for the step action.
      await vi.advanceTimersByTimeAsync(2_000)
      await store.save()
      expect(mocked.saveRegistration).not.toHaveBeenCalled()
      expect(tg.getLocation).not.toHaveBeenCalled()
      await store.save(false, true)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.calls[0]![0].location).toEqual({ lat: 43.238912, lon: 76.889712 })
      // Later saves do not send it again.
      store.form.last_name = 'Әлиев'
      store.touch()
      await store.save(false, true)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(2)
      expect(mocked.saveRegistration.mock.calls[1]![0]).not.toHaveProperty('location')
      expect(tg.getLocation).toHaveBeenCalledTimes(1)
    })

    it('creates the profile without a position when the device has none', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ id: null, code: null }))
      mocked.saveRegistration.mockResolvedValue(reg())
      tg.getLocation.mockRejectedValue(new Error('denied'))
      await store.load()
      store.form.first_name = 'Ержан'
      store.touch()
      await store.save(false, true)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.calls[0]![0]).not.toHaveProperty('location')
    })

    it('creates the profile (first PUT) before the first upload', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ id: null, code: null }))
      mocked.saveRegistration.mockResolvedValue(reg())
      mocked.uploadRegistrationFile.mockResolvedValue({ id: 'f1', kind: 'avatar', url: '/api/v1/driver/files/f1', width: 1, height: 1, created_at: '' })
      tg.getLocation.mockRejectedValue(new Error('unavailable'))
      await store.load()
      await store.upload('avatar', new Blob(['x']), () => undefined, new AbortController().signal)
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.invocationCallOrder[0]!).toBeLessThan(mocked.uploadRegistrationFile.mock.invocationCallOrder[0]!)
    })
  })

  describe('body type (§21)', () => {
    it('a new registration starts with Sedan once the classes are known, and saves it', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      await store.load()
      expect(store.form.vehicle_class).toBe('')
      useSessionStore().extras.vehicleClasses = [MINIVAN, SEDAN]
      await vi.advanceTimersByTimeAsync(0)
      expect(store.form.vehicle_class).toBe('sedan')
      await vi.advanceTimersByTimeAsync(2_000) // autosave
      expect(mocked.saveRegistration.mock.calls[0]![0].vehicle).toMatchObject({ vehicle_class: 'sedan' })
    })

    it('choosing a class saves only the class: the API gives it the class seats and cargo space', async () => {
      const store = useRegistrationStore()
      useSessionStore().extras.vehicleClasses = [MINIVAN]
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      await store.load()
      store.chooseClass(MINIVAN)
      expect(store.form.vehicle_class).toBe('minivan')
      await vi.advanceTimersByTimeAsync(2_000) // autosave
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.calls[0]![0].vehicle).toEqual({ make: '', model: '', color: '', plate: '', vehicle_class: 'minivan' })
    })

    it('takes the class the API stored with the new profile', async () => {
      const store = useRegistrationStore()
      const stored = { ...reg().vehicle, vehicle_class: 'sedan', vehicle_class_name: SEDAN.name, seats: 4, cargo_volume_l: 400 }
      mocked.getRegistration.mockResolvedValue(reg({ vehicle: stored }))
      mocked.saveRegistration.mockResolvedValue(reg({ vehicle: stored }))
      await store.load()
      expect(store.form.vehicle_class).toBe('sedan')
      await store.save(true)
      expect(mocked.saveRegistration.mock.calls[0]![0].vehicle).not.toHaveProperty('vehicle_class')
    })

    it('keeps a class the moderator did not flag (corrections)', async () => {
      const store = useRegistrationStore()
      useSessionStore().extras.vehicleClasses = [MINIVAN]
      const stored = { ...reg().vehicle, vehicle_class: 'sedan', vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, seats: 4, cargo_volume_l: 400 }
      mocked.getRegistration.mockResolvedValue(
        reg({ status: 'changes_requested', vehicle: stored, corrections: [{ field: 'vehicle_color', reason: 'other', comment: null }] }),
      )
      await store.load()
      store.chooseClass(MINIVAN)
      expect(store.form.vehicle_class).toBe('sedan')
    })

    it('keeps an unflagged IIN editable while none is stored (corrections)', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ status: 'changes_requested', corrections: [{ field: 'vehicle_color', reason: 'other', comment: null }] }))
      mocked.saveRegistration.mockResolvedValue(reg())
      await store.load()
      expect(store.editable.has('identity')).toBe(true)
      store.form.iin = IIN
      store.touch()
      await store.save()
      expect(mocked.saveRegistration.mock.calls[0]![0].iin).toBe(IIN)
    })
  })

  describe('submission', () => {
    it('finishes with the consent version; the answer is the approved registration', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      mocked.submitRegistration.mockResolvedValue(reg({ status: 'approved', editable: false, iin: IIN }))
      await store.load()
      const res = await store.submit()
      expect(mocked.submitRegistration).toHaveBeenCalledWith(PD_CONSENT_VERSION)
      expect(res.ok).toBe(true)
      expect(store.registration?.status).toBe('approved')
      expect(store.editable.size).toBe(0)
    })

    it('remembers a phone the API refused as another driver\'s until a new one is verified', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg({ phone: '+77011234567', phone_verified: true }))
      mocked.saveRegistration.mockResolvedValue(reg())
      mocked.submitRegistration.mockRejectedValue(new ApiError(409, 'phone_taken', 'x'))
      await store.load()
      const res = await store.submit()
      expect(res.ok).toBe(false)
      expect(store.taken.phone).toBe(true)
      tg.requestContact.mockResolvedValue('sent')
      await store.requestPhone()
      mocked.getRegistration.mockResolvedValue(reg({ phone: '+77017654321', phone_verified: true }))
      await vi.advanceTimersByTimeAsync(PHONE_POLL_MS)
      expect(store.phoneFlow).toBe('verified')
      expect(store.taken.phone).toBe(false)
    })

    it('maps server errors back to the pages that need work', () => {
      const incomplete = new ApiError(422, 'registration_incomplete', 'x', {}, { missing_fields: ['vehicle_plate', 'identity', 'bogus'] })
      expect(submitProblem(incomplete, reg())).toMatchObject({ steps: ['personal', 'vehicle'], fields: ['vehicle_plate', 'identity'] })
      expect(submitProblem(new ApiError(422, 'phone_not_verified', 'x'), reg()).steps).toEqual(['personal'])
      expect(submitProblem(new ApiError(409, 'iin_taken', 'x'), reg())).toMatchObject({ steps: ['personal'], fields: ['identity'] })
      expect(submitProblem(new ApiError(409, 'plate_taken', 'x'), reg()).steps).toEqual(['vehicle'])
      expect(submitProblem(new ApiError(409, 'phone_taken', 'x'), reg()).steps).toEqual(['personal'])
      const invalid = new ApiError(422, 'validation_failed', 'x', { 'vehicle.color': 'too_long', iin: 'checksum' })
      expect(submitProblem(invalid, reg())).toMatchObject({ steps: ['personal', 'vehicle'], fields: ['vehicle_color', 'identity'] })
      const consent = new ApiError(422, 'validation_failed', 'x', { pd_consent: 'must_be_true' })
      expect(submitProblem(consent, reg()).steps).toEqual([])
    })

    it('maps request field names to registration fields', () => {
      expect(fieldFromRequestKey('vehicle.make')).toBe('vehicle_make_model')
      expect(fieldFromRequestKey('vehicle.plate')).toBe('vehicle_plate')
      expect(fieldFromRequestKey('vehicle.vehicle_class')).toBe('vehicle_class')
      expect(fieldFromRequestKey('vehicle.seats')).toBe('vehicle_class')
      expect(fieldFromRequestKey('first_name')).toBe('first_name')
      expect(fieldFromRequestKey('iin')).toBe('identity')
      expect(fieldFromRequestKey('patronymic')).toBe('identity')
      expect(fieldFromRequestKey('pd_consent')).toBeNull()
    })
  })
})
