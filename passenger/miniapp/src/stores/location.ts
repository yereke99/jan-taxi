// The passenger's device position (Telegram LocationManager first, browser
// geolocation otherwise). There is no default position: until a fix arrives
// the app only knows the map centre, which is never shown as "you are here".

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getLocation, LocationError, type DeviceLocation } from '../telegram'
import { isInKazakhstanBBox } from '../utils/geo'

/** Accuracy radius above which the user is asked to check the point (§ product rule). */
export const POOR_ACCURACY_M = 100

export const useLocationStore = defineStore('location', () => {
  const current = ref<DeviceLocation | null>(null)
  const locating = ref(false)
  const error = shallowRef<unknown>(null)
  let inFlight: Promise<DeviceLocation | null> | null = null

  const accuracy = computed(() => current.value?.accuracy_m ?? null)
  const inaccurate = computed(() => accuracy.value !== null && accuracy.value > POOR_ACCURACY_M)

  /**
   * Resolves with a fresh position inside Kazakhstan, or null (error holds the
   * reason: a LocationError, or 'outside_kz' for a point outside the country).
   */
  function locate(): Promise<DeviceLocation | null> {
    inFlight ??= (async () => {
      locating.value = true
      error.value = null
      try {
        const loc = await getLocation()
        if (!isInKazakhstanBBox(loc)) {
          error.value = 'outside_kz'
          return null
        }
        current.value = loc
        return loc
      } catch (err) {
        error.value = err instanceof LocationError ? err : new LocationError('unavailable')
        return null
      } finally {
        locating.value = false
        inFlight = null
      }
    })()
    return inFlight
  }

  return { current, locating, error, accuracy, inaccurate, locate }
})
