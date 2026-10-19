// Driver registration: the server draft (GET/PUT /driver/registration), photo
// uploads, Telegram phone verification, plate availability and submission.
// Text fields are saved to the server as the driver goes (debounced and on
// every step change), so closing the Mini App loses nothing.

import { defineStore } from 'pinia'
import { computed, reactive, ref, shallowRef, watch } from 'vue'
import * as api from '../api/driver'
import type { UploadProgress } from '../api/upload'
import { REGISTRATION_FIELDS, type DriverRegistration, type FileRef, type PlateCheck, type RegistrationField, type UploadKind } from '../api/types'
import { PD_CONSENT_VERSION } from '../features/registration/consent'
import {
  applyClass,
  buildUpdate,
  editableFields,
  formFromRegistration,
  isValidPlate,
  normalizePlateInput,
  sameUpdate,
  seatsMaxFor,
  stepsForFields,
  validateName,
  type RegistrationForm,
  type StepId,
} from '../features/registration/form'
import { MAX_CARGO_VOLUME_L, type VehicleClassOption } from '../features/vehicle/classes'
import { isAbortError, isApiError } from '../services/http'
import { getLocation, requestContact } from '../telegram'
import { roundCoord } from '../utils/geo'
import { useSessionStore } from './session'

export type PhoneFlow = 'idle' | 'requesting' | 'waiting' | 'verified' | 'cancelled' | 'failed' | 'unavailable' | 'timeout'

export const PHONE_POLL_MS = 3_000
export const PHONE_WAIT_MS = 60_000
const AUTOSAVE_MS = 1_500
const PLATE_CHECK_MS = 450
/** How long the profile-creating save waits for the device position. */
export const PROFILE_LOCATION_TIMEOUT_MS = 8_000

/**
 * The device position for the save that creates the profile: the server
 * takes the driver's city, and so the code prefix, from it (the nationwide
 * region KAZ outside every city zone). Best effort: no position (denied,
 * unavailable, app hidden) leaves the choice to the server's default city.
 */
async function profileLocation(): Promise<{ lat: number; lon: number } | undefined> {
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return undefined
  try {
    const loc = await getLocation({ timeoutMs: PROFILE_LOCATION_TIMEOUT_MS, maxAgeMs: 300_000 })
    return { lat: roundCoord(loc.lat), lon: roundCoord(loc.lon) }
  } catch {
    return undefined
  }
}

/**
 * The ticked consent box survives closing the Mini App until submission. The
 * key names the Telegram account: Telegram keeps one storage per bot on a
 * device, shared by every account signed in there.
 */
function consentKey(): string {
  const user = useSessionStore().me?.telegram_user_id ?? 0
  return `jt.driver.pd_consent.${PD_CONSENT_VERSION}.${user}`
}

function readConsent(): boolean {
  try {
    return localStorage.getItem(consentKey()) === '1'
  } catch {
    return false
  }
}

function writeConsent(v: boolean): void {
  try {
    if (v) localStorage.setItem(consentKey(), '1')
    else localStorage.removeItem(consentKey())
  } catch {
    // Storage unavailable: the checkbox simply has to be ticked again next time.
  }
}

export interface SubmitProblem {
  /** Steps that need attention, in form order. */
  steps: StepId[]
  fields: RegistrationField[]
  error: unknown
}

export const useRegistrationStore = defineStore('registration', () => {
  const registration = shallowRef<DriverRegistration | null>(null)
  const loading = ref(false)
  const loadError = shallowRef<unknown>(null)
  const form = reactive<RegistrationForm>({
    first_name: '',
    last_name: '',
    vehicle_class: '',
    seats: null,
    cargo_volume_l: '',
    make: '',
    model: '',
    color: '',
    plate: '',
  })
  const saving = ref(false)
  const saveError = shallowRef<unknown>(null)
  const consentGiven = ref(readConsent())
  const phoneFlow = ref<PhoneFlow>('idle')
  const plate = reactive<{ checking: boolean; result: PlateCheck | null; forInput: string }>({ checking: false, result: null, forInput: '' })
  const submitting = ref(false)
  let dirty = false
  /** Incremented on every edit, so a save only clears `dirty` for what it sent. */
  let revision = 0
  let autosaveTimer: ReturnType<typeof setTimeout> | undefined
  let saveChain: Promise<void> = Promise.resolve()
  let phoneTimer: ReturnType<typeof setTimeout> | undefined
  let phoneDeadline = 0
  /** The verified phone on file when the request started (null when none). */
  let phoneBefore: string | null = null
  let plateTimer: ReturnType<typeof setTimeout> | undefined
  let plateAbort: AbortController | null = null

  const editable = computed(() => (registration.value ? editableFields(registration.value) : new Set<RegistrationField>()))
  const consentRecorded = computed(() => registration.value?.pd_consent_version === PD_CONSENT_VERSION)

  function adopt(r: DriverRegistration, resetForm: boolean): void {
    registration.value = r
    if (resetForm || !dirty) Object.assign(form, formFromRegistration(r))
    if (phoneFlow.value === 'waiting') {
      // Sharing again (flagged phone): wait for a different number or the bot's event.
      if (r.phone_verified && r.phone !== phoneBefore) finishPhone('verified')
    } else if (phoneFlow.value !== 'requesting' && r.phone_verified) {
      phoneFlow.value = 'verified'
    }
  }

  async function load(): Promise<void> {
    loading.value = true
    try {
      adopt(await api.getRegistration(), !dirty)
      loadError.value = null
    } catch (err) {
      loadError.value = err
    } finally {
      loading.value = false
    }
  }

  /** Re-reads the server state without discarding unsaved typing. */
  async function refresh(): Promise<void> {
    try {
      adopt(await api.getRegistration(), false)
    } catch {
      // Background refresh: the visible state stays as it is.
    }
  }

  function payload(): ReturnType<typeof buildUpdate> | null {
    const r = registration.value
    if (!r || (r.id !== null && !r.editable)) return null
    const body = buildUpdate(r, form)
    // An unfinished plate is not sent (the API rejects invalid plates); the
    // vehicle step blocks "Next" until it is valid.
    if (body.vehicle.plate && !isValidPlate(body.vehicle.plate)) body.vehicle.plate = r.vehicle.plate
    // Seats outside the class range are not sent either (the class step keeps
    // them in range; the API would answer 422).
    const seats = body.vehicle.seats
    if (seats !== undefined && body.vehicle.vehicle_class && (seats < 1 || seats > seatsMaxFor(classes(), body.vehicle.vehicle_class))) delete body.vehicle.seats
    if (body.vehicle.cargo_volume_l !== undefined && body.vehicle.cargo_volume_l > MAX_CARGO_VOLUME_L) delete body.vehicle.cargo_volume_l
    return body
  }

  async function doSave(force: boolean, create: boolean): Promise<void> {
    if (!registration.value || !payload()) return
    // Nothing typed: no request (and no profile is created just by opening the form).
    if (!force && !dirty) return
    const creating = registration.value.id === null
    // Only an explicit step action creates the profile, with the device position;
    // background saves (typing, app hidden) wait for it.
    if (creating && !create) return
    const location = creating ? await profileLocation() : undefined
    const r = registration.value
    const body = payload()
    if (!r || !body) return
    if (!force && r.id !== null && sameUpdate(body, buildUpdate(r, formFromRegistration(r)))) {
      dirty = false
      return
    }
    saving.value = true
    const sentRevision = revision
    try {
      const saved = await api.saveRegistration(r.id === null && location ? { ...body, location } : body)
      // Typing that happened while the request was in flight stays dirty (and on screen).
      if (revision === sentRevision) dirty = false
      adopt(saved, false)
      saveError.value = null
    } catch (err) {
      saveError.value = err
      throw err
    } finally {
      saving.value = false
    }
  }

  /**
   * Saves pending text changes now (step change, app hidden, before
   * upload/submit). `create` marks an explicit step action ("Next", first
   * upload, submit): only such a save creates the profile, sending the device
   * position with it; before that, background saves send nothing.
   */
  function save(force = false, create = force): Promise<void> {
    clearTimeout(autosaveTimer)
    const run = saveChain.then(() => doSave(force, create))
    saveChain = run.catch(() => undefined)
    return run
  }

  function classes(): readonly VehicleClassOption[] {
    return useSessionStore().extras.vehicleClasses
  }

  /** Chooses a vehicle class: seats and cargo take its defaults (saved like typed text). */
  function chooseClass(c: VehicleClassOption): void {
    if (!editable.value.has('vehicle_class') || form.vehicle_class === c.code) return
    applyClass(form, c)
    touch()
  }

  /** Marks the form edited and schedules a background save. */
  function touch(): void {
    dirty = true
    revision++
    clearTimeout(autosaveTimer)
    autosaveTimer = setTimeout(() => void save().catch(() => undefined), AUTOSAVE_MS)
  }

  /**
   * Suggests the Telegram name for a new application (only into empty fields,
   * only names the API accepts). Saved with the first step like typed text.
   */
  function prefillNames(first: string, last: string): void {
    const r = registration.value
    if (!r || r.id !== null) return
    let changed = false
    if (!form.first_name && first && !validateName(first)) {
      form.first_name = first.trim()
      changed = true
    }
    if (!form.last_name && last && !validateName(last)) {
      form.last_name = last.trim()
      changed = true
    }
    if (changed) dirty = true
  }

  /** The profile must exist before files can be attached (first PUT creates it). */
  async function ensureProfile(): Promise<void> {
    if (registration.value?.id === null || !registration.value) await save(true)
  }

  async function upload(kind: UploadKind, blob: Blob, onProgress: (p: UploadProgress) => void, signal: AbortSignal): Promise<FileRef> {
    await ensureProfile()
    const ref = await api.uploadRegistrationFile(kind, blob, { onProgress, signal })
    await refresh()
    return ref
  }

  async function removeExtraPhoto(fileId: string): Promise<void> {
    await api.deleteRegistrationFile(fileId)
    await refresh()
  }

  // --- Phone verification through the driver bot (§11) ---------------------

  function stopPhonePolling(): void {
    clearTimeout(phoneTimer)
    phoneTimer = undefined
  }

  function finishPhone(state: PhoneFlow): void {
    stopPhonePolling()
    phoneFlow.value = state
  }

  function pollPhone(): void {
    stopPhonePolling()
    phoneTimer = setTimeout(async () => {
      if (phoneFlow.value !== 'waiting') return
      await refresh()
      if (phoneFlow.value !== 'waiting') return
      if (Date.now() >= phoneDeadline) finishPhone('timeout')
      else pollPhone()
    }, PHONE_POLL_MS)
  }

  /**
   * Asks Telegram to share the phone with the driver bot. The bot verifies the
   * contact and reports it to the API; the app then waits for
   * driver.phone_verified (and polls every 3 s for up to 60 s).
   */
  async function requestPhone(): Promise<void> {
    if (phoneFlow.value === 'requesting' || phoneFlow.value === 'waiting') return
    phoneFlow.value = 'requesting'
    phoneBefore = registration.value?.phone_verified ? registration.value.phone : null
    const result = await requestContact()
    if (result === 'unavailable') return finishPhone('unavailable')
    if (result === 'cancelled') return finishPhone('cancelled')
    if (result === 'failed') return finishPhone('failed')
    phoneFlow.value = 'waiting'
    phoneDeadline = Date.now() + PHONE_WAIT_MS
    await refresh()
    if (phoneFlow.value === 'waiting') pollPhone()
  }

  /** Realtime driver.phone_verified: the bot accepted a contact just now. */
  async function onPhoneVerified(): Promise<void> {
    const waiting = phoneFlow.value === 'waiting'
    await refresh()
    if (waiting && phoneFlow.value === 'waiting' && registration.value?.phone_verified) finishPhone('verified')
  }

  // --- Plate availability -----------------------------------------------------

  function checkPlateSoon(): void {
    clearTimeout(plateTimer)
    plateAbort?.abort()
    const input = form.plate
    if (!isValidPlate(input)) {
      plate.checking = false
      plate.result = null
      plate.forInput = ''
      return
    }
    const normalized = normalizePlateInput(input)
    if (plate.result && plate.forInput === normalized) return
    plate.checking = true
    plateTimer = setTimeout(async () => {
      const ctrl = new AbortController()
      plateAbort = ctrl
      try {
        plate.result = await api.checkPlate(input, ctrl.signal)
        plate.forInput = normalized
      } catch (err) {
        // Advisory only: rate limits and network errors leave the final check to save/submit.
        if (!isAbortError(err)) plate.result = null
      } finally {
        if (plateAbort === ctrl) plate.checking = false
      }
    }, PLATE_CHECK_MS)
  }

  watch(
    () => form.plate,
    () => checkPlateSoon(),
  )

  // --- Consent and submission ---------------------------------------------------

  function setConsent(v: boolean): void {
    consentGiven.value = v
    writeConsent(v)
  }

  /** Submits for moderation; on failure returns where the driver must look. */
  async function submit(): Promise<{ ok: true; registration: DriverRegistration } | { ok: false; problem: SubmitProblem }> {
    submitting.value = true
    try {
      await save(false, true)
      const r = await api.submitRegistration(PD_CONSENT_VERSION)
      dirty = false
      adopt(r, true)
      return { ok: true, registration: r }
    } catch (err) {
      return { ok: false, problem: submitProblem(err, registration.value) }
    } finally {
      submitting.value = false
    }
  }

  function reset(): void {
    stopPhonePolling()
    clearTimeout(autosaveTimer)
    registration.value = null
    dirty = false
  }

  return {
    registration,
    loading,
    loadError,
    form,
    saving,
    saveError,
    consentGiven,
    consentRecorded,
    phoneFlow,
    plate,
    submitting,
    editable,
    load,
    refresh,
    save,
    touch,
    chooseClass,
    prefillNames,
    upload,
    removeExtraPhoto,
    requestPhone,
    onPhoneVerified,
    setConsent,
    submit,
    reset,
  }
})

/** Maps a submit error to the steps that need attention. */
export function submitProblem(err: unknown, r: DriverRegistration | null): SubmitProblem {
  const fields: RegistrationField[] = []
  if (isApiError(err)) {
    switch (err.code) {
      case 'registration_incomplete': {
        const list = Array.isArray(err.details.missing_fields) ? err.details.missing_fields : r?.missing_fields ?? []
        for (const f of list) if (typeof f === 'string' && isField(f)) fields.push(f)
        break
      }
      case 'phone_not_verified':
      case 'phone_taken':
        fields.push('phone')
        break
      case 'plate_taken':
        fields.push('vehicle_plate')
        break
      case 'validation_failed':
        for (const key of Object.keys(err.fields)) {
          const f = fieldFromRequestKey(key)
          if (f) fields.push(f)
        }
        break
    }
  }
  return { steps: stepsForFields(fields), fields, error: err }
}

const FIELDS = new Set<string>(REGISTRATION_FIELDS)

function isField(f: string): f is RegistrationField {
  return FIELDS.has(f)
}

/** Request field names (vehicle.plate) → registration fields. */
export function fieldFromRequestKey(key: string): RegistrationField | null {
  switch (key) {
    case 'first_name':
    case 'last_name':
      return key
    case 'vehicle.make':
    case 'vehicle.model':
      return 'vehicle_make_model'
    case 'vehicle.color':
      return 'vehicle_color'
    case 'vehicle.plate':
      return 'vehicle_plate'
    case 'vehicle.vehicle_class':
    case 'vehicle.seats':
    case 'vehicle.cargo_volume_l':
      return 'vehicle_class'
    default:
      return isField(key) ? key : null
  }
}
