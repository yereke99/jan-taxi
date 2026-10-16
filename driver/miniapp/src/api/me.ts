// GET /api/v1/me and PATCH /me/preferences (users/handler.go): a flat user
// object with the driver summary for driver tokens.

import { http } from '../services/http'
import { normalizeLocale, type Locale } from '../i18n/core'
import type { CityRef, ModerationStatus } from './types'
import { moderationStatus, normalizeCity, rec, str, strOrNull } from './normalize'

export interface MeDriver {
  id: string
  /** Human driver code, e.g. EMB001. */
  code: string
  status: ModerationStatus
  is_online: boolean
  city: CityRef | null
}

export interface Me {
  id: string
  telegram_user_id: number
  first_name: string
  last_name: string
  username: string | null
  language: Locale
  /** Masked phone ("+7 707 *** ** 67") or null. */
  phone_masked: string | null
  phone_verified: boolean
  app: 'passenger' | 'driver'
  driver: MeDriver | null
}

function normalizeDriver(raw: unknown): MeDriver | null {
  const d = rec(raw)
  if (!str(d.id)) return null
  return {
    id: str(d.id),
    code: str(d.code),
    status: moderationStatus(d.status),
    is_online: d.is_online === true,
    city: normalizeCity(d.city),
  }
}

export function normalizeMe(raw: unknown): Me {
  const r = rec(raw)
  return {
    id: str(r.id),
    telegram_user_id: typeof r.telegram_user_id === 'number' ? r.telegram_user_id : 0,
    first_name: str(r.first_name),
    last_name: str(r.last_name),
    username: strOrNull(r.username),
    language: normalizeLocale(str(r.language)),
    phone_masked: strOrNull(r.phone_masked),
    phone_verified: r.phone_verified === true,
    app: r.app === 'passenger' ? 'passenger' : 'driver',
    driver: normalizeDriver(r.driver),
  }
}

export async function getMe(): Promise<Me> {
  return normalizeMe(await http.get<unknown>('/me'))
}

export async function updateLanguage(language: Locale): Promise<Me> {
  return normalizeMe(await http.patch<unknown>('/me/preferences', { language }))
}
