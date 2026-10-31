// The driver's own profile (GET /driver/profile): vehicle, rating, trips and
// the masked IIN, which a driver registered before it was asked for adds once
// (PUT /driver/profile/iin).

import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { getProfile, saveProfileIin } from '../api/driver'
import type { DriverProfile } from '../api/types'
import { useSessionStore } from './session'

export const useProfileStore = defineStore('profile', () => {
  const profile = shallowRef<DriverProfile | null>(null)
  const loading = ref(false)
  const error = shallowRef<unknown>(null)
  let inFlight: Promise<void> | null = null

  function refresh(): Promise<void> {
    inFlight ??= (async () => {
      loading.value = true
      try {
        profile.value = await getProfile()
        error.value = null
      } catch (err) {
        error.value = useSessionStore().handleAccessError(err) ? null : err
      } finally {
        loading.value = false
      }
    })().finally(() => {
      inFlight = null
    })
    return inFlight
  }

  /** Stores the IIN (twelve valid digits); the answer is the updated profile. Throws the API error. */
  async function saveIin(iin: string): Promise<void> {
    profile.value = await saveProfileIin(iin)
  }

  return { profile, loading, error, refresh, saveIin }
})
