// The driver's active order (GET /driver/orders/active) and its transitions:
// arriving → start → intermediate stops in seq order → complete. Drivers
// cannot cancel (§22.3). Every action returns the updated order from the server.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import {
  acceptOrder,
  completeStop,
  completeTrip,
  getActive,
  getOrder,
  markArriving,
  startTrip,
} from '../api/orders'
import type { DriverOrderDetail } from '../api/types'
import { isActiveTripStatus, type TripAction } from '../features/trip/sequence'
import { useSessionStore } from './session'

export const useTripStore = defineStore('trip', () => {
  const active = shallowRef<DriverOrderDetail | null>(null)
  /** The order that just finished (completed or cancelled) for the summary screen. */
  const finished = shallowRef<DriverOrderDetail | null>(null)
  const loaded = ref(false)
  const busy = ref(false)
  const error = shallowRef<unknown>(null)
  let inFlight: Promise<void> | null = null
  let again = false

  const hasActive = computed(() => active.value !== null)

  function apply(o: DriverOrderDetail | null): void {
    if (o && isActiveTripStatus(o.status)) {
      active.value = o
      return
    }
    if (o && active.value && o.id === active.value.id) finished.value = o
    active.value = null
  }

  async function load(): Promise<void> {
    try {
      const prev = active.value
      const o = await getActive()
      if (!o && prev) {
        // The order ended elsewhere (e.g. the passenger cancelled): show its real final state.
        const last = await getOrder(prev.id).catch(() => null)
        if (last && !isActiveTripStatus(last.status)) finished.value = last
      }
      apply(o)
      error.value = null
      loaded.value = true
    } catch (err) {
      error.value = useSessionStore().handleAccessError(err) ? null : err
    }
  }

  /**
   * Re-fetches the active order. A call made while a request is running gets
   * one follow-up request, so a change committed after that request started
   * (an event that triggered this call) is never lost.
   */
  function refresh(): Promise<void> {
    if (inFlight) {
      again = true
      return inFlight
    }
    inFlight = (async () => {
      do {
        again = false
        await load()
      } while (again)
    })().finally(() => {
      inFlight = null
    })
    return inFlight
  }

  async function guarded(fn: () => Promise<DriverOrderDetail>): Promise<DriverOrderDetail> {
    if (busy.value) throw new Error('trip action already running')
    busy.value = true
    try {
      const o = await fn()
      apply(o)
      return o
    } finally {
      busy.value = false
    }
  }

  /** Orders this app accepted itself (no "order assigned to you" toast for them). */
  const ownAccepts = new Set<string>()

  /** Accepts an available order; on success it becomes the active trip. */
  function accept(orderId: string): Promise<DriverOrderDetail> {
    ownAccepts.add(orderId)
    return guarded(() => acceptOrder(orderId))
  }

  function acceptedHere(orderId: string): boolean {
    return ownAccepts.has(orderId)
  }

  /** Runs the primary action computed by features/trip/sequence. */
  function perform(action: TripAction): Promise<DriverOrderDetail> {
    const o = active.value
    if (!o) return Promise.reject(new Error('no active trip'))
    switch (action.kind) {
      case 'arrive':
        return guarded(() => markArriving(o.id))
      case 'start':
        return guarded(() => startTrip(o.id))
      case 'complete_stop':
        return guarded(() => completeStop(o.id, action.stopId))
      case 'complete':
        return guarded(() => completeTrip(o.id))
    }
  }

  function clearFinished(): void {
    finished.value = null
  }

  return { active, finished, loaded, busy, error, hasActive, refresh, accept, acceptedHere, perform, clearFinished, apply }
})
