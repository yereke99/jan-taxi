// Sends a Kaspi receipt for a top-up with progress: a PDF goes as it is (the
// server checks it and may credit the wallet at once), a screenshot is
// downscaled and attached as evidence, then the top-up is sent for review.

import { onScopeDispose, ref, shallowRef, type Ref, type ShallowRef } from 'vue'
import type { TopUp } from '../../api/types'
import { isAbortError } from '../../services/http'
import { ImageError, prepareImage } from '../media/image'
import { hasPdfHeader, MAX_RECEIPT_PDF_BYTES, receiptFileKind, receiptUploadName } from './receipt'

export type ReceiptUploadState = 'idle' | 'preparing' | 'uploading' | 'checking' | 'done' | 'error'

/** Client-side refusal before anything is sent. */
export class ReceiptFileError extends Error {
  override readonly name = 'ReceiptFileError'
  constructor(readonly code: 'not_pdf' | 'too_large') {
    super(code)
  }
}

export interface ReceiptUploadDeps {
  upload: (blob: Blob, fileName: string, onProgress: (fraction: number | null) => void, signal: AbortSignal) => Promise<TopUp>
  /** Moves a top-up that only got an image to manual review. */
  submit: (topup: TopUp) => Promise<TopUp>
}

export interface ReceiptUpload {
  state: Ref<ReceiptUploadState>
  /** 0..1 while uploading. */
  progress: Ref<number>
  error: ShallowRef<unknown>
  /** Name of the file being sent (for the progress row). */
  fileName: Ref<string>
  send(file: File): Promise<TopUp | null>
  retry(): Promise<TopUp | null>
  reset(): void
}

export function useReceiptUpload(deps: ReceiptUploadDeps): ReceiptUpload {
  const state = ref<ReceiptUploadState>('idle')
  const progress = ref(0)
  const error = shallowRef<unknown>(null)
  const fileName = ref('')
  let last: File | null = null
  let controller: AbortController | null = null

  async function prepare(file: File): Promise<{ blob: Blob; name: string; image: boolean }> {
    const kind = receiptFileKind(file)
    if (kind === 'unsupported') throw new ReceiptFileError('not_pdf')
    if (kind === 'image') {
      try {
        return { blob: await prepareImage(file), name: 'receipt.jpg', image: true }
      } catch (err) {
        throw err instanceof ImageError && err.code === 'too_large' ? new ReceiptFileError('too_large') : new ReceiptFileError('not_pdf')
      }
    }
    if (file.size > MAX_RECEIPT_PDF_BYTES) throw new ReceiptFileError('too_large')
    if (!(await hasPdfHeader(file))) throw new ReceiptFileError('not_pdf')
    return { blob: file, name: receiptUploadName(file.name), image: false }
  }

  async function send(file: File): Promise<TopUp | null> {
    controller?.abort()
    const ctrl = new AbortController()
    controller = ctrl
    last = file
    fileName.value = file.name || 'receipt.pdf'
    error.value = null
    progress.value = 0
    state.value = 'preparing'
    try {
      const prepared = await prepare(file)
      if (ctrl.signal.aborted) return null
      state.value = 'uploading'
      let result = await deps.upload(
        prepared.blob,
        prepared.name,
        (f) => {
          if (f === null) return
          progress.value = f
          // The bytes are sent; the server now reads and checks the receipt.
          if (f >= 1 && state.value === 'uploading') state.value = 'checking'
        },
        ctrl.signal,
      )
      if (prepared.image && result.status === 'created') result = await deps.submit(result)
      progress.value = 1
      state.value = 'done'
      return result
    } catch (err) {
      if (isAbortError(err)) return null
      error.value = err
      state.value = 'error'
      return null
    } finally {
      if (controller === ctrl) controller = null
    }
  }

  function retry(): Promise<TopUp | null> {
    return last ? send(last) : Promise.resolve(null)
  }

  function reset(): void {
    controller?.abort()
    controller = null
    state.value = 'idle'
    progress.value = 0
    error.value = null
  }

  onScopeDispose(() => controller?.abort())

  return { state, progress, error, fileName, send, retry, reset }
}
