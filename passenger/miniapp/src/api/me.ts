// GET /me and PATCH /me/preferences (admin/backend/internal/users/handler.go).
// The payload is a flat object; the passenger app ignores the driver summary.

import type { Locale } from '../i18n/core'
import { normalizeLocale } from '../i18n/core'
import { http } from '../services/http'
import { rec } from './parse'

export interface Me {
  id: string
  telegram_user_id: number
  first_name: string
  last_name: string
  username: string | null
  language: Locale
  /** Masked phone ("+7 7•• ••• •• 12") when one is known. */
  phone_masked: string | null
  phone_verified: boolean
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

/** Maps the /me payload; a payload without an id is rejected by the caller. */
export function parseMe(raw: unknown): Me {
  const r = rec(raw)
  return {
    id: str(r.id),
    telegram_user_id: typeof r.telegram_user_id === 'number' ? r.telegram_user_id : 0,
    first_name: str(r.first_name).trim(),
    last_name: str(r.last_name).trim(),
    username: str(r.username) || null,
    language: normalizeLocale(str(r.language)),
    phone_masked: str(r.phone_masked) || null,
    phone_verified: r.phone_verified === true,
  }
}

export async function getMe(signal?: AbortSignal): Promise<Me> {
  return parseMe(await http.get<unknown>('/me', { signal }))
}

/** Saves the UI language; the API answers with the updated /me object. */
export async function updateLanguage(language: Locale): Promise<Me> {
  return parseMe(await http.patch<unknown>('/me/preferences', { language }))
}
