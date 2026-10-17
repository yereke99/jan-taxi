// Private files (ID documents, vehicle photos, receipt images and PDFs) are
// fetched through the API client with the session cookie and shown from a
// blob: URL that is revoked when no longer needed. File URLs are never put
// into <img src> or <iframe src> directly, so nothing is cached by the
// browser or leaked through history.

import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { api, apiPath, isAbortError } from '../services/api'

export interface SecureFile {
  src: Ref<string | null>
  /** Media type of the loaded file ("image/jpeg", "application/pdf"), empty until loaded. */
  type: Ref<string>
  loading: Ref<boolean>
  error: Ref<unknown>
  reload: () => void
}

export type SecureImage = SecureFile

/** Media type without parameters, lower case. */
export function baseMediaType(contentType: string): string {
  return (contentType.split(';')[0] ?? '').trim().toLowerCase()
}

export function useSecureFile(url: Ref<string | null | undefined>): SecureFile {
  const src = ref<string | null>(null)
  const type = ref('')
  const loading = ref(false)
  const error = ref<unknown>(null)
  let ctrl: AbortController | null = null

  function release(): void {
    if (src.value) URL.revokeObjectURL(src.value)
    src.value = null
    type.value = ''
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
      const mediaType = baseMediaType(res.contentType || res.blob.type)
      // The blob keeps the served type so the browser's PDF viewer accepts it.
      const blob = res.blob.type === mediaType || !mediaType ? res.blob : new Blob([res.blob], { type: mediaType })
      type.value = mediaType
      src.value = URL.createObjectURL(blob)
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

  return { src, type, loading, error, reload: () => void load() }
}

export const useSecureImage = useSecureFile
