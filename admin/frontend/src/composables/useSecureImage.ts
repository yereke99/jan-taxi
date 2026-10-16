// Private images (ID documents, vehicle photos, receipts) are fetched through
// the API client with the session cookie and shown from a blob: URL that is
// revoked when no longer needed. File URLs are never put into <img src>
// directly, so nothing is cached by the browser or leaked through history.

import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { api, apiPath, isAbortError } from '../services/api'

export interface SecureImage {
  src: Ref<string | null>
  loading: Ref<boolean>
  error: Ref<unknown>
  reload: () => void
}

export function useSecureImage(url: Ref<string | null | undefined>): SecureImage {
  const src = ref<string | null>(null)
  const loading = ref(false)
  const error = ref<unknown>(null)
  let ctrl: AbortController | null = null

  function release(): void {
    if (src.value) URL.revokeObjectURL(src.value)
    src.value = null
  }

  async function load(): Promise<void> {
    ctrl?.abort()
    release()
    error.value = null
    const u = url.value
    if (!u) return
    const mine = new AbortController()
    ctrl = mine
    loading.value = true
    try {
      const res = await api.blob(apiPath(u), { signal: mine.signal })
      if (ctrl !== mine) return
      src.value = URL.createObjectURL(res.blob)
    } catch (err) {
      if (!isAbortError(err) && ctrl === mine) error.value = err
    } finally {
      if (ctrl === mine) {
        loading.value = false
        ctrl = null
      }
    }
  }

  watch(url, () => void load(), { immediate: true })
  onBeforeUnmount(() => {
    ctrl?.abort()
    release()
  })

  return { src, loading, error, reload: () => void load() }
}
