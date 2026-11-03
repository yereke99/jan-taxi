// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { retrieveRawInitData } from '@tma.js/sdk'

/**
 * Cosmetic launch photo for the user already authenticated by the API. This
 * never establishes identity or authorizes an upload: the server separately
 * validates signed initData before importing a driver's Telegram photo.
 */
export function getTelegramPhotoUrl(userId: number | undefined): string | null {
  if (!Number.isSafeInteger(userId) || !userId || userId < 0) return null
  try {
    const raw = retrieveRawInitData()
    if (!raw) return null
    const user: unknown = JSON.parse(new URLSearchParams(raw).get('user') ?? 'null')
    if (!user || typeof user !== 'object') return null
    const { id, photo_url: photo } = user as Record<string, unknown>
    if (id !== userId || typeof photo !== 'string' || !photo.trim()) return null
    const url = new URL(photo)
    if (url.protocol !== 'https:' || url.username || url.password) return null
    return url.href
  } catch {
    // No launch data outside Telegram, or no accessible profile photo.
    return null
  }
}
