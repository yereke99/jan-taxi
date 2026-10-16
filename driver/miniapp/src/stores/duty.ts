// Online / offline (PATCH /driver/availability). Going online first sends a
// fresh real location when one can be read, so dispatch sees the driver at
// once; the API decides whether a location is mandatory in this city.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { setAvailability } from '../api/driver'
import type { Availability } from '../api/types'
import { isApiError } from '../services/http'
import { useLocationStore } from './location'
import { useSessionStore } from './session'

export type OnlineOutcome =
  | { ok: true; online: boolean; withoutLocation: boolean }
  | { ok: false; reason: 'insufficient_balance'; required: number | null; available: number | null; topupMin: number | null }
  | { ok: false; reason: 'location_required' }
  | { ok: false; reason: 'outside_service_area' }
  | { ok: false; reason: 'not_approved' }
  | { ok: false; reason: 'error'; error: unknown }

function int(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

export function classifyAvailabilityError(err: unknown): OnlineOutcome {
  if (!isApiError(err)) return { ok: false, reason: 'error', error: err }
  const reason = typeof err.details.reason === 'string' ? err.details.reason : ''
  if (err.code === 'insufficient_balance') {
    return {
      ok: false,
      reason: 'insufficient_balance',
      required: int(err.details.required),
      available: int(err.details.available),
      topupMin: int(err.details.topup_min_amount),
    }
  }
  if (err.code === 'location_required' || reason === 'location_required' || reason === 'location_stale') {
    return { ok: false, reason: 'location_required' }
  }
  if (err.code === 'outside_service_area') return { ok: false, reason: 'outside_service_area' }
  if (err.code === 'driver_not_approved' || err.code === 'driver_not_registered') return { ok: false, reason: 'not_approved' }
  return { ok: false, reason: 'error', error: err }
}

export const useDutyStore = defineStore('duty', () => {
  const session = useSessionStore()
  const location = useLocationStore()
  const availability = shallowRef<Availability | null>(null)
  const switching = ref(false)

  const isOnline = computed(() => session.driver?.is_online === true)

  function apply(a: Availability): void {
    availability.value = a
    session.setOnline(a.is_online)
  }

  async function goOnline(): Promise<OnlineOutcome> {
    let withoutLocation = false
    try {
      // A recent real fix is enough; otherwise read one now.
      const fix = location.current ?? (await location.locateOnce())
      try {
        await location.sendNow(fix)
      } catch (err) {
        if (isApiError(err) && err.code === 'outside_service_area') return { ok: false, reason: 'outside_service_area' }
        // Rate limit or network: the regular reporting retries in a few seconds.
      }
    } catch {
      withoutLocation = true
    }
    try {
      apply(await setAvailability(true))
      return { ok: true, online: true, withoutLocation }
    } catch (err) {
      return classifyAvailabilityError(err)
    }
  }

  /** Switches online/offline; the result tells the screen what to show. */
  async function setOnline(on: boolean): Promise<OnlineOutcome> {
    if (switching.value) return { ok: false, reason: 'error', error: null }
    switching.value = true
    try {
      if (on) return await goOnline()
      apply(await setAvailability(false))
      return { ok: true, online: false, withoutLocation: false }
    } catch (err) {
      return classifyAvailabilityError(err)
    } finally {
      switching.value = false
    }
  }

  return { availability, switching, isOnline, setOnline }
})
