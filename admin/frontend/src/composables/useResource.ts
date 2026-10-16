// Loads one REST resource with cancellation, loading and error state.
// A newer load aborts the previous one, so fast filter changes never show
// stale results.

import { onBeforeUnmount, ref, shallowRef, type Ref, type ShallowRef } from 'vue'
import { isAbortError } from '../services/api'

export interface Resource<T> {
  data: ShallowRef<T | null>
  loading: Ref<boolean>
  error: ShallowRef<unknown>
  /** Loads again; `silent` keeps the current data visible without a spinner. */
  reload: (opts?: { silent?: boolean }) => Promise<void>
  set: (value: T | null) => void
}

export function useResource<T>(loader: (signal: AbortSignal) => Promise<T>): Resource<T> {
  const data = shallowRef<T | null>(null)
  const loading = ref(false)
  const error = shallowRef<unknown>(null)
  let ctrl: AbortController | null = null
  let disposed = false

  async function reload(opts: { silent?: boolean } = {}): Promise<void> {
    ctrl?.abort()
    const mine = new AbortController()
    ctrl = mine
    if (!opts.silent) loading.value = true
    try {
      const value = await loader(mine.signal)
      if (ctrl !== mine || disposed) return
      data.value = value
      error.value = null
    } catch (err) {
      if (isAbortError(err) || ctrl !== mine || disposed) return
      // Silent refreshes keep showing the last good data.
      if (!opts.silent || data.value === null) error.value = err
    } finally {
      if (ctrl === mine) {
        loading.value = false
        ctrl = null
      }
    }
  }

  onBeforeUnmount(() => {
    disposed = true
    ctrl?.abort()
  })

  return { data, loading, error, reload, set: (v) => (data.value = v) }
}
