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
  deleteRegistrationFile: vi.fn(),
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

function reg(over: Partial<DriverRegistration> = {}): DriverRegistration {
  return {
    id: 'd1',
    status: 'draft',
    code: 'EMB001',
    city: null,
    first_name: 'Әлі',
    last_name: 'Серіков',
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
      photos: [],
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
        vehicle: { make: '', model: '', color: '', plate: '' },
      })
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

  describe('vehicle class (§21)', () => {
    it('choosing a class prefills seats and cargo from the class and saves them with the draft', async () => {
      const store = useRegistrationStore()
      useSessionStore().extras.vehicleClasses = [MINIVAN]
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      await store.load()
      store.chooseClass(MINIVAN)
      expect(store.form).toMatchObject({ vehicle_class: 'minivan', seats: 7, cargo_volume_l: '800' })
      await vi.advanceTimersByTimeAsync(2_000) // autosave
      expect(mocked.saveRegistration).toHaveBeenCalledTimes(1)
      expect(mocked.saveRegistration.mock.calls[0]![0].vehicle).toMatchObject({ vehicle_class: 'minivan', seats: 7, cargo_volume_l: 800 })
    })

    it('sends no class fields before a class is chosen (the API keeps what it has)', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      await store.load()
      store.form.first_name = 'Ержан'
      await store.save(true)
      const body = mocked.saveRegistration.mock.calls[0]![0].vehicle
      expect(body).not.toHaveProperty('vehicle_class')
      expect(body).not.toHaveProperty('seats')
      expect(body).not.toHaveProperty('cargo_volume_l')
    })

    it('does not send seats above the class maximum', async () => {
      const store = useRegistrationStore()
      useSessionStore().extras.vehicleClasses = [MINIVAN]
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      await store.load()
      store.chooseClass(MINIVAN)
      store.form.seats = 9
      await store.save(true)
      const body = mocked.saveRegistration.mock.calls.at(-1)![0].vehicle
      expect(body.vehicle_class).toBe('minivan')
      expect(body).not.toHaveProperty('seats')
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
  })

  describe('submission', () => {
    it('submits with the consent version', async () => {
      const store = useRegistrationStore()
      mocked.getRegistration.mockResolvedValue(reg())
      mocked.saveRegistration.mockResolvedValue(reg())
      mocked.submitRegistration.mockResolvedValue(reg({ status: 'pending', editable: false }))
      await store.load()
      const res = await store.submit()
      expect(mocked.submitRegistration).toHaveBeenCalledWith(PD_CONSENT_VERSION)
      expect(res.ok).toBe(true)
      expect(store.registration?.status).toBe('pending')
    })

    it('maps server errors back to the steps that need work', () => {
      const incomplete = new ApiError(422, 'registration_incomplete', 'x', {}, { missing_fields: ['vehicle_photos', 'id_back', 'bogus'] })
      expect(submitProblem(incomplete, reg())).toMatchObject({ steps: ['documents', 'photos'], fields: ['vehicle_photos', 'id_back'] })
      expect(submitProblem(new ApiError(422, 'phone_not_verified', 'x'), reg()).steps).toEqual(['phone'])
      expect(submitProblem(new ApiError(409, 'plate_taken', 'x'), reg()).steps).toEqual(['vehicle'])
      expect(submitProblem(new ApiError(409, 'phone_taken', 'x'), reg()).steps).toEqual(['phone'])
      const invalid = new ApiError(422, 'validation_failed', 'x', { 'vehicle.color': 'too_long', last_name: 'invalid' })
      expect(submitProblem(invalid, reg()).steps).toEqual(['personal', 'vehicle'])
      const noClass = new ApiError(422, 'registration_incomplete', 'x', {}, { missing_fields: ['vehicle_plate', 'vehicle_class'] })
      expect(submitProblem(noClass, reg()).steps).toEqual(['vehicle_class', 'vehicle'])
      const seats = new ApiError(422, 'validation_failed', 'x', { 'vehicle.seats': 'too_large' }, { seats_max: 8 })
      expect(submitProblem(seats, reg()).steps).toEqual(['vehicle_class'])
    })

    it('maps request field names to registration fields', () => {
      expect(fieldFromRequestKey('vehicle.make')).toBe('vehicle_make_model')
      expect(fieldFromRequestKey('vehicle.plate')).toBe('vehicle_plate')
      expect(fieldFromRequestKey('vehicle.vehicle_class')).toBe('vehicle_class')
      expect(fieldFromRequestKey('vehicle.seats')).toBe('vehicle_class')
      expect(fieldFromRequestKey('vehicle.cargo_volume_l')).toBe('vehicle_class')
      expect(fieldFromRequestKey('first_name')).toBe('first_name')
      expect(fieldFromRequestKey('pd_consent')).toBeNull()
    })
  })
})
