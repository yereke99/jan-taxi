// Private files (§12): /api/v1/driver/files/{id} needs the Authorization
// header, so <img src> cannot load them directly. Images are fetched once
// through the API client (token + one re-authentication on 401), kept as
// object URLs in a small LRU cache and revoked when evicted.

import { API_BASE, ApiError, http } from '../services/http'

const MAX_CACHED = 40
const DRIVER_FILES = `${API_BASE}/driver/files/`

const cache = new Map<string, string>()
const inFlight = new Map<string, Promise<string>>()

function remember(url: string, objectUrl: string): void {
  cache.delete(url)
  cache.set(url, objectUrl)
  while (cache.size > MAX_CACHED) {
    const oldest = cache.keys().next().value
    if (oldest === undefined) break
    const obj = cache.get(oldest)
    cache.delete(oldest)
    if (obj) URL.revokeObjectURL(obj)
  }
}

/** Only the driver's own file URLs (/api/v1/driver/files/<id>) are fetched with the token. */
export function isDriverFileUrl(url: string): boolean {
  return url.startsWith(DRIVER_FILES) && /^[0-9a-f-]{36}$/i.test(url.slice(DRIVER_FILES.length))
}

/** Returns an object URL for a private file, fetching it at most once. */
export function privateImageUrl(url: string): Promise<string> {
  const hit = cache.get(url)
  if (hit) {
    remember(url, hit)
    return Promise.resolve(hit)
  }
  if (!isDriverFileUrl(url)) return Promise.reject(new ApiError(400, 'bad_request', 'not a driver file url'))
  let p = inFlight.get(url)
  if (!p) {
    p = http
      .getBlob(url.slice(API_BASE.length))
      .then((blob) => {
        const objectUrl = URL.createObjectURL(blob)
        remember(url, objectUrl)
        return objectUrl
      })
      .finally(() => inFlight.delete(url))
    inFlight.set(url, p)
  }
  return p
}

/** Drops every cached object URL (e.g. on sign-out). */
export function clearImageCache(): void {
  for (const obj of cache.values()) URL.revokeObjectURL(obj)
  cache.clear()
}
