// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Public runtime configuration: GET /api/v1/public/config?app=passenger|driver
// (ARCHITECTURE §14). The shape follows the API's PublicConfig
// (admin/backend/internal/cities/publicconfig.go). The payload is normalised
// defensively so a missing optional field degrades to a documented default
// instead of breaking the app. Map defaults follow §3/§14 (OpenFreeMap
// Positron, Embi as the default map centre only).

import { DEFAULT_MAP_CENTER, DEFAULT_MAP_STYLE_URL, DEFAULT_MAP_ZOOM } from '../maps/constants'
import type { Locale } from '../utils/format'
import { isValidLatLon, type LatLon } from '../utils/geo'
import { http } from './http'

export interface LocalizedText {
  kk: string
  ru: string
  en: string
}

export interface PublicCity {
  code: string
  name: LocalizedText
  center: LatLon
  zoom: number
}

export interface PublicBots {
  passenger_username: string | null
  driver_username: string | null
  /** Direct links (https://t.me/<bot>?startapp=…&mode=fullscreen). */
  passenger_link: string | null
  driver_link: string | null
  driver_register_link: string | null
}

export interface SupportContacts {
  telegram_username: string | null
  /** https://t.me/<support username> */
  telegram_link: string | null
  phone: string | null
  hours: LocalizedText | null
}

export interface FareRange {
  min: number
  max: number
  step: number
}

export interface KaspiPublic {
  payment_url: string | null
  recipient_name: string | null
  instructions: LocalizedText | null
}

export interface TopUpLimits {
  min: number
  max: number
  presets: number[]
}

export interface PublicConfig {
  app: string
  default_language: Locale
  languages: Locale[]
  /** Default map city; null when no city is active. */
  city: PublicCity | null
  /** default_center/default_zoom come from `city` (or the Embi defaults). */
  map: { style_url: string; default_center: LatLon; default_zoom: number }
  /** Stops per order including pickup and destination. */
  routing: { max_points: number }
  bots: PublicBots
  support: SupportContacts
  /** Prohibited item codes; the apps translate them. */
  delivery: { prohibited_items: string[] }
  navigation: { twogis_base_url: string | null }
  /** Default city's taxi fare range (passenger app only). */
  fare: FareRange | null
  /** Kaspi public payment settings (driver app only). */
  kaspi: KaspiPublic | null
  /** Top-up limits (driver app only). */
  topup: TopUpLimits | null
}

export const DEFAULT_MAX_ROUTE_POINTS = 5
const LOCALES: readonly Locale[] = ['kk', 'ru', 'en']

function rec(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
}

function strOrNull(v: unknown): string | null {
  return typeof v === 'string' && v.trim() !== '' ? v.trim() : null
}

function intOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) ? v : null
}

function locale(v: unknown): Locale | null {
  return typeof v === 'string' && (LOCALES as readonly string[]).includes(v) ? (v as Locale) : null
}

/** A localized text with all three languages, or null when empty/absent. */
export function localizedOrNull(v: unknown): LocalizedText | null {
  const r = rec(v)
  const t = { kk: strOrNull(r.kk) ?? '', ru: strOrNull(r.ru) ?? '', en: strOrNull(r.en) ?? '' }
  return t.kk || t.ru || t.en ? t : null
}

/** Picks the text for a language, falling back to Kazakh, then any filled value. */
export function pickLocalized(t: LocalizedText | null | undefined, lang: Locale): string {
  if (!t) return ''
  return t[lang] || t.kk || t.ru || t.en
}

function httpsUrlOrNull(v: unknown): string | null {
  const s = strOrNull(v)
  if (!s) return null
  try {
    return new URL(s).protocol === 'https:' ? s : null
  } catch {
    return null
  }
}

export function defaultPublicConfig(): PublicConfig {
  return {
    app: '',
    default_language: 'kk',
    languages: [...LOCALES],
    city: null,
    map: { style_url: DEFAULT_MAP_STYLE_URL, default_center: { ...DEFAULT_MAP_CENTER }, default_zoom: DEFAULT_MAP_ZOOM },
    routing: { max_points: DEFAULT_MAX_ROUTE_POINTS },
    bots: { passenger_username: null, driver_username: null, passenger_link: null, driver_link: null, driver_register_link: null },
    support: { telegram_username: null, telegram_link: null, phone: null, hours: null },
    delivery: { prohibited_items: [] },
    navigation: { twogis_base_url: null },
    fare: null,
    kaspi: null,
    topup: null,
  }
}

function normalizeCity(v: unknown): PublicCity | null {
  const r = rec(v)
  const center = rec(r.center)
  const point = { lat: Number(center.lat), lon: Number(center.lon) }
  const code = strOrNull(r.code)
  if (!code || !isValidLatLon(point)) return null
  const zoom = Number(r.zoom)
  return {
    code,
    name: localizedOrNull(r.name) ?? { kk: code, ru: code, en: code },
    center: point,
    zoom: Number.isFinite(zoom) && zoom >= 3 && zoom <= 20 ? zoom : DEFAULT_MAP_ZOOM,
  }
}

function normalizeFare(v: unknown): FareRange | null {
  const r = rec(v)
  const min = intOrNull(r.min)
  const max = intOrNull(r.max)
  const step = intOrNull(r.step)
  return min !== null && max !== null && step !== null && min > 0 && max >= min && step > 0 ? { min, max, step } : null
}

function normalizeKaspi(v: unknown): KaspiPublic | null {
  if (!v || typeof v !== 'object') return null
  const r = rec(v)
  return {
    payment_url: httpsUrlOrNull(r.payment_url),
    recipient_name: strOrNull(r.recipient_name),
    instructions: localizedOrNull(r.instructions),
  }
}

function normalizeTopUp(v: unknown): TopUpLimits | null {
  const r = rec(v)
  const min = intOrNull(r.min)
  const max = intOrNull(r.max)
  if (min === null || max === null || min <= 0 || max < min) return null
  const presets = Array.isArray(r.presets)
    ? r.presets.filter((x): x is number => typeof x === 'number' && Number.isInteger(x) && x >= min && x <= max)
    : []
  return { min, max, presets }
}

/** Maps an arbitrary payload onto PublicConfig, filling defaults. */
export function normalizePublicConfig(raw: unknown): PublicConfig {
  const d = defaultPublicConfig()
  const r = rec(raw)
  const city = normalizeCity(r.city)
  const map = rec(r.map)
  const routing = rec(r.routing)
  const bots = rec(r.bots)
  const support = rec(r.support)
  const delivery = rec(r.delivery)
  const navigation = rec(r.navigation)
  const maxPoints = intOrNull(routing.max_points)
  const languages = Array.isArray(r.languages) ? r.languages.map(locale).filter((l): l is Locale => l !== null) : []
  return {
    app: strOrNull(r.app) ?? d.app,
    default_language: locale(r.default_language) ?? d.default_language,
    languages: languages.length > 0 ? languages : d.languages,
    city,
    map: {
      style_url: httpsUrlOrNull(map.style_url) ?? d.map.style_url,
      default_center: city ? { ...city.center } : d.map.default_center,
      default_zoom: city ? city.zoom : d.map.default_zoom,
    },
    routing: { max_points: maxPoints !== null && maxPoints >= 2 && maxPoints <= 5 ? maxPoints : d.routing.max_points },
    bots: {
      passenger_username: strOrNull(bots.passenger_username),
      driver_username: strOrNull(bots.driver_username),
      passenger_link: httpsUrlOrNull(bots.passenger_link),
      driver_link: httpsUrlOrNull(bots.driver_link),
      driver_register_link: httpsUrlOrNull(bots.driver_register_link),
    },
    support: {
      telegram_username: strOrNull(support.telegram_username),
      telegram_link: httpsUrlOrNull(support.telegram_link),
      phone: strOrNull(support.phone),
      hours: localizedOrNull(support.hours),
    },
    delivery: {
      prohibited_items: Array.isArray(delivery.prohibited_items)
        ? delivery.prohibited_items.filter((x): x is string => typeof x === 'string' && x.trim() !== '')
        : [],
    },
    navigation: { twogis_base_url: httpsUrlOrNull(navigation.twogis_base_url) },
    fare: normalizeFare(r.fare),
    kaspi: normalizeKaspi(r.kaspi),
    topup: r.topup ? normalizeTopUp(r.topup) : null,
  }
}

export async function fetchPublicConfig(app: 'passenger' | 'driver', signal?: AbortSignal): Promise<PublicConfig> {
  const raw = await http.get<unknown>('/public/config', { query: { app }, signal })
  return normalizePublicConfig(raw)
}
