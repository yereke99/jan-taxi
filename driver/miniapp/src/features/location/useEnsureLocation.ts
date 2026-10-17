// Gets the driver's GPS to the API when dispatch needs it (ARCHITECTURE §7):
// the API answers location_required / location_stale when it has no recent
// fix, so the screen reads one now, sends it and says what went wrong when it
// cannot (no permission, no signal, outside Kazakhstan).

import { ref, type Ref } from 'vue'
import { isApiError } from '../../services/http'
import { useLocationStore } from '../../stores/location'
import { canOpenLocationSettings, LocationError, openLocationSettings } from '../../telegram'

export type EnsureLocationState = 'idle' | 'locating' | 'denied' | 'failed' | 'outside'

export interface EnsureLocation {
  state: Ref<EnsureLocationState>
  /** Reads a fresh fix and reports it; true when the API now has it. */
  ensure(): Promise<boolean>
  /** After a denial: Telegram's location settings when available, else another try. */
  fix(): Promise<boolean>
}

export function useEnsureLocation(): EnsureLocation {
  const location = useLocationStore()
  const state = ref<EnsureLocationState>('idle')
  let running: Promise<boolean> | null = null

  function ensure(): Promise<boolean> {
    running ??= (async () => {
      state.value = 'locating'
      try {
        await location.sendNow(await location.locateOnce())
        state.value = 'idle'
        return true
      } catch (err) {
        // Rate limited: a fix reached the API within the last two seconds.
        if (isApiError(err) && err.status === 429) {
          state.value = 'idle'
          return true
        }
        if (err instanceof LocationError) state.value = err.code === 'denied' ? 'denied' : 'failed'
        else if (isApiError(err) && err.code === 'outside_service_area') state.value = 'outside'
        else state.value = 'failed'
        return false
      }
    })().finally(() => {
      running = null
    })
    return running
  }

  async function fix(): Promise<boolean> {
    if (state.value === 'denied' && canOpenLocationSettings()) {
      openLocationSettings()
      return false
    }
    return ensure()
  }

  return { state, ensure, fix }
}
