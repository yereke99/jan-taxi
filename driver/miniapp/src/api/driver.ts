// Driver registration, profile, availability and location endpoints.

import { http, type RequestOptions } from '../services/http'
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
import { normalizeRegistration } from './normalize'
import { uploadWithProgress, type UploadProgress } from './upload'

export async function getRegistration(opts?: RequestOptions): Promise<DriverRegistration> {
  return normalizeRegistration(await http.get<unknown>('/driver/registration', opts))
}

export async function saveRegistration(body: DriverRegistrationUpdate): Promise<DriverRegistration> {
  return normalizeRegistration(await http.put<unknown>('/driver/registration', body))
}

/**
 * Submits (or resubmits) the application with the personal-data consent the
 * driver gave on the consent step (moderation/submit.go: pd_consent must be
 * true with a version on the first submission).
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

export function deleteRegistrationFile(fileId: string): Promise<void> {
  return http.delete<void>(`/driver/registration/files/${encodeURIComponent(fileId)}`)
}

export function getProfile(opts?: RequestOptions): Promise<DriverProfile> {
  return http.get<DriverProfile>('/driver/profile', opts)
}

export function setAvailability(online: boolean): Promise<Availability> {
  return http.patch<Availability>('/driver/availability', { online })
}

export function reportLocation(body: LocationInput): Promise<void> {
  return http.post<void>('/driver/location', body, { timeoutMs: 10_000 })
}
