// The driver's own profile (GET /driver/profile): vehicle, rating, trips.

import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { getProfile } from '../api/driver'
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

  return { profile, loading, error, refresh }
})
