// The passenger's device position (Telegram LocationManager first, browser
// geolocation otherwise). There is no default position: until a fix arrives
// the app only knows the map centre, which is never shown as "you are here".
// After the first fix the position is watched (about once a second) so the
// live dot follows the device; `following` keeps the camera on it until the
// passenger pans the map.

import { defineStore } from 'pinia'
import { computed, ref, shallowRef } from 'vue'
import { getLocation, LocationError, watchLocation, type DeviceLocation } from '../telegram'
import { isInKazakhstanBBox } from '../utils/geo'

/** Accuracy radius above which the user is asked to check the point (§ product rule). */
export const POOR_ACCURACY_M = 100

export const useLocationStore = defineStore('location', () => {
  const current = ref<DeviceLocation | null>(null)
  const locating = ref(false)
  const error = shallowRef<unknown>(null)
  /** True after the locate button until the passenger moves the map. */
  const following = ref(false)
  let inFlight: Promise<DeviceLocation | null> | null = null
  let stopWatch: (() => void) | null = null

  /** Starts streaming fixes from the provider that produced `first`. Idempotent. */
  function watch(first: DeviceLocation): void {
    if (stopWatch) return
    stopWatch = watchLocation(
      (loc) => {
        if (isInKazakhstanBBox(loc)) current.value = loc
      },
      () => {
        // Keep the last good fix; the next sample may succeed.
      },
      { prefer: first.source },
    )
  }

  function stop(): void {
    stopWatch?.()
    stopWatch = null
  }

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
        watch(loc)
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

  return { current, locating, error, accuracy, inaccurate, following, locate, stop }
})
