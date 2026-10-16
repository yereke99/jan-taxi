// One photo upload with a local preview, progress and retry. The prepared
// (downscaled) blob is kept so "retry" resends it without asking for the photo
// again.

import { onScopeDispose, ref, shallowRef, type Ref, type ShallowRef } from 'vue'
import type { UploadProgress } from '../../api/upload'
import { isAbortError } from '../../services/http'
import { prepareImage } from './image'

export type UploadState = 'idle' | 'preparing' | 'uploading' | 'error' | 'done'

export interface UploadTask<T> {
  state: Ref<UploadState>
  /** 0..1 during the upload. */
  progress: Ref<number>
  /** Object URL of the picked photo, shown until the server copy is loaded. */
  previewUrl: Ref<string | null>
  error: ShallowRef<unknown>
  run(file: Blob): Promise<T | null>
  retry(): Promise<T | null>
  reset(): void
}

export function useUploadTask<T>(
  upload: (blob: Blob, onProgress: (p: UploadProgress) => void, signal: AbortSignal) => Promise<T>,
  onDone?: (result: T) => void,
): UploadTask<T> {
  const state = ref<UploadState>('idle')
  const progress = ref(0)
  const previewUrl = ref<string | null>(null)
  const error = shallowRef<unknown>(null)
  let prepared: Blob | null = null
  let controller: AbortController | null = null

  function setPreview(blob: Blob | null): void {
    if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
    previewUrl.value = blob ? URL.createObjectURL(blob) : null
  }

  async function send(): Promise<T | null> {
    if (!prepared) return null
    controller?.abort()
    controller = new AbortController()
    state.value = 'uploading'
    progress.value = 0
    error.value = null
    try {
      const result = await upload(prepared, (p) => (progress.value = p.fraction ?? progress.value), controller.signal)
      progress.value = 1
      state.value = 'done'
      onDone?.(result)
      return result
    } catch (err) {
      if (isAbortError(err)) return null
      error.value = err
      state.value = 'error'
      return null
    }
  }

  async function run(file: Blob): Promise<T | null> {
    controller?.abort()
    state.value = 'preparing'
    error.value = null
    try {
      prepared = await prepareImage(file)
    } catch (err) {
      prepared = null
      error.value = err
      state.value = 'error'
      return null
    }
    setPreview(prepared)
    return send()
  }

  function reset(): void {
    controller?.abort()
    controller = null
    prepared = null
    setPreview(null)
    state.value = 'idle'
    progress.value = 0
    error.value = null
  }

  onScopeDispose(reset)

  return { state, progress, previewUrl, error, run, retry: send, reset }
}
