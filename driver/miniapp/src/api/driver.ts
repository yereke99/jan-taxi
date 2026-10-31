// Driver registration, profile, availability and location endpoints.

import { http, isApiError, type RequestOptions } from '../services/http'
import { REQUIRED_FIELDS } from './types'
import type {
  Availability,
  DriverProfile,
  DriverRegistration,
  DriverRegistrationUpdate,
  FileRef,
  LocationInput,
  PlateCheck,
  UploadKind,
} from './types'
import { normalizeRegistration, normalizeVehicleClassFields, rec, str, strOrNull } from './normalize'
import { uploadWithProgress, type UploadProgress } from './upload'

/** A new application: nothing saved yet, everything editable and missing. */
export function emptyRegistration(): DriverRegistration {
  return normalizeRegistration({ status: 'draft', editable: true, missing_fields: [...REQUIRED_FIELDS] })
}

/**
 * The application as saved on the server. Before the first save the API
 * returns an empty draft; a 404 or 403 driver_not_registered from an API
 * that has no profile yet means the same and is not an error.
 */
export async function getRegistration(opts?: RequestOptions): Promise<DriverRegistration> {
  try {
    return normalizeRegistration(await http.get<unknown>('/driver/registration', opts))
  } catch (err) {
    if (isApiError(err) && (err.status === 404 || err.code === 'driver_not_registered')) return emptyRegistration()
    throw err
  }
}

export async function saveRegistration(body: DriverRegistrationUpdate): Promise<DriverRegistration> {
  return normalizeRegistration(await http.put<unknown>('/driver/registration', body))
}

/**
 * Finishes the registration with the personal-data consent the driver ticked
 * on the last page (moderation/submit.go: pd_consent must be true with a
 * version on the first submission). When the automatic checks pass the API
 * approves the driver in the same request (status "approved").
 */
export async function submitRegistration(pdConsentVersion: string): Promise<DriverRegistration> {
  return normalizeRegistration(
    await http.post<unknown>('/driver/register', { pd_consent: true, pd_consent_version: pdConsentVersion }),
  )
}

export async function checkPlate(plate: string, signal?: AbortSignal): Promise<PlateCheck | null> {
  const res = await http.post<{ plate: PlateCheck | null }>('/driver/registration/check', { plate }, { signal })
  return res?.plate ?? null
}

export function uploadRegistrationFile(
  kind: UploadKind,
  file: Blob,
  opts: { onProgress?: (p: UploadProgress) => void; signal?: AbortSignal } = {},
): Promise<FileRef> {
  const form = new FormData()
  form.append('kind', kind)
  form.append('file', file, `${kind}.jpg`)
  return uploadWithProgress<FileRef>('/driver/registration/files', form, opts)
}

/**
 * GET /driver/profile and PUT /driver/profile/iin: the vehicle's class fields
 * (§21) always present (null until chosen), the patronymic and the masked IIN.
 */
function normalizeProfile(raw: DriverProfile): DriverProfile {
  if (!raw || typeof raw !== 'object') return raw
  const p = { ...raw, patronymic: str(raw.patronymic), iin_masked: strOrNull(raw.iin_masked) }
  if (!p.vehicle) return p
  const v = rec(p.vehicle) as unknown as NonNullable<DriverProfile['vehicle']>
  return { ...p, vehicle: { ...v, ...normalizeVehicleClassFields(v) } }
}

export async function getProfile(opts?: RequestOptions): Promise<DriverProfile> {
  return normalizeProfile(await http.get<DriverProfile>('/driver/profile', opts))
}

/**
 * Stores the IIN of an approved driver who has none yet (drivers registered
 * before it was asked for). Set once: the same value again is accepted, another
 * one answers 409 conflict (details.reason iin_already_set); 409 iin_taken
 * when another driver holds it.
 */
export async function saveProfileIin(iin: string): Promise<DriverProfile> {
  return normalizeProfile(await http.put<DriverProfile>('/driver/profile/iin', { iin }))
}

export function setAvailability(online: boolean): Promise<Availability> {
  return http.patch<Availability>('/driver/availability', { online })
}

export function reportLocation(body: LocationInput): Promise<void> {
  return http.post<void>('/driver/location', body, { timeoutMs: 10_000 })
}
