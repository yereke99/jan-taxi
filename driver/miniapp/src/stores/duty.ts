// Online / offline (PATCH /driver/availability). Going online first sends a
// fresh real location: dispatch offers orders by distance, and the API
// rejects going online without a recent fix (422 location_required). While
// online the location store keeps reporting every few seconds.
//
// The state shown is always the one the API confirmed: the PATCH answer,
// GET /me, or driver.status_changed (the bot, the idle timeout). There is no
// optimistic flip; a failed request re-reads the real state (reconcile).

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { setAvailability } from '../api/driver'
import { getMe } from '../api/me'
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
  /** Target of the request in flight (for the "going online…" state), or null. */
  const switchingTo = ref<boolean | null>(null)
  /** Bumped by every confirmed change, so an older read cannot overwrite it. */
  let epoch = 0

  const isOnline = computed(() => session.driver?.is_online === true)

  function apply(a: Availability): void {
    epoch += 1
    availability.value = a
    session.setOnline(a.is_online)
  }

  /**
   * Re-reads the availability from the API (GET /me): it may have changed
   * elsewhere (driver bot, idle timeout) or a failed request may still have
   * been applied. A toggle confirmed meanwhile wins over this read.
   */
  async function reconcile(): Promise<void> {
    if (!session.isReady || switching.value) return
    const started = epoch
    try {
      const me = await getMe()
      if (started !== epoch || switching.value || !me.driver) return
      session.setOnline(me.driver.is_online)
    } catch {
      // Offline network: keep the last confirmed state; resync runs on reconnect.
    }
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
      const outcome = classifyAvailabilityError(err)
      if (outcome.ok || outcome.reason !== 'location_required') return outcome
      // The API needs a fresh fix (dispatch is radius-based): read and send
      // one now, then retry once. A denied or failing GPS keeps the reason,
      // so the screen explains how to enable location.
      try {
        await location.sendNow(await location.locateOnce())
        apply(await setAvailability(true))
        return { ok: true, online: true, withoutLocation: false }
      } catch (retryErr) {
        return isApiError(retryErr) ? classifyAvailabilityError(retryErr) : outcome
      }
    }
  }

  /** Switches online/offline; the result tells the screen what to show. */
  async function setOnline(on: boolean): Promise<OnlineOutcome> {
    if (switching.value) return { ok: false, reason: 'error', error: null }
    switching.value = true
    switchingTo.value = on
    let outcome: OnlineOutcome
    try {
      if (on) outcome = await goOnline()
      else {
        apply(await setAvailability(false))
        outcome = { ok: true, online: false, withoutLocation: false }
      }
    } catch (err) {
      outcome = classifyAvailabilityError(err)
    } finally {
      switching.value = false
      switchingTo.value = null
    }
    // Network failure or timeout: the server may have applied it anyway.
    if (!outcome.ok && outcome.reason === 'error') void reconcile()
    return outcome
  }

  return { availability, switching, switchingTo, isOnline, setOnline, reconcile }
})
