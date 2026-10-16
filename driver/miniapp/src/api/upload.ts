// multipart/form-data uploads with progress. fetch() cannot report upload
// progress, so this uses XMLHttpRequest with the same auth and error model as
// services/http (Bearer token, one re-authentication on 401, ApiError).

import { getToken, reauthenticate } from '../services/auth'
import { API_BASE, ApiError, toApiError } from '../services/http'

export interface UploadProgress {
  loaded: number
  total: number
  /** 0..1, or null while the size is unknown. */
  fraction: number | null
}

export interface UploadOptions {
  onProgress?: (p: UploadProgress) => void
  signal?: AbortSignal
  /** Milliseconds; default 120 s (slow mobile uplinks). */
  timeoutMs?: number
}

interface XhrResult {
  status: number
  body: string
  headers: Headers
}

function parseHeaders(raw: string): Headers {
  const h = new Headers()
  for (const line of raw.trim().split(/[\r\n]+/)) {
    const i = line.indexOf(':')
    if (i > 0) {
      try {
        h.append(line.slice(0, i).trim(), line.slice(i + 1).trim())
      } catch {
        // Ignore malformed header lines.
      }
    }
  }
  return h
}

function send(path: string, form: FormData, opts: UploadOptions): Promise<XhrResult> {
  return new Promise((resolve, reject) => {
    if (opts.signal?.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${API_BASE}${path}`)
    xhr.timeout = opts.timeoutMs ?? 120_000
    xhr.setRequestHeader('Accept', 'application/json')
    const token = getToken()
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    xhr.upload.onprogress = (e) => {
      opts.onProgress?.({ loaded: e.loaded, total: e.total, fraction: e.lengthComputable && e.total > 0 ? e.loaded / e.total : null })
    }
    const onAbort = (): void => xhr.abort()
    opts.signal?.addEventListener('abort', onAbort, { once: true })
    const done = (): void => opts.signal?.removeEventListener('abort', onAbort)
    xhr.onload = () => {
      done()
      resolve({ status: xhr.status, body: xhr.responseText, headers: parseHeaders(xhr.getAllResponseHeaders()) })
    }
    xhr.onerror = () => {
      done()
      reject(new ApiError(0, 'network_error', `POST ${path} failed: network error`))
    }
    xhr.ontimeout = () => {
      done()
      reject(new ApiError(0, 'timeout', `POST ${path} timed out`))
    }
    xhr.onabort = () => {
      done()
      reject(new DOMException('Aborted', 'AbortError'))
    }
    xhr.send(form)
  })
}

/** Uploads `form` to `path` (relative to /api/v1) and parses the JSON response. */
export async function uploadWithProgress<T>(path: string, form: FormData, opts: UploadOptions = {}): Promise<T> {
  let res = await send(path, form, opts)
  if (res.status === 401 && (await reauthenticate())) res = await send(path, form, opts)
  if (res.status === 0) throw new ApiError(0, 'network_error', `POST ${path} failed: network error`)
  if (res.status < 200 || res.status >= 300) {
    throw await toApiError(new Response(res.body || null, { status: res.status, headers: res.headers }))
  }
  if (!res.body) return undefined as T
  try {
    return JSON.parse(res.body) as T
  } catch {
    throw new ApiError(res.status, 'bad_response', 'response is not valid JSON')
  }
}
