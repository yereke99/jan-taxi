// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Public runtime configuration: GET /api/v1/public/config?app=passenger|driver
// (ARCHITECTURE §14). The shape follows the API's PublicConfig
// (admin/backend/internal/cities/publicconfig.go). The payload is normalised
// defensively so a missing optional field degrades to a documented default
// instead of breaking the app. Map defaults follow §3/§14 (OpenFreeMap
// Positron, Embi as the default map centre only). Vehicle classes, cargo sizes
// and the passenger limit follow §21; cancel reasons, commission and the
// welcome bonus follow §22.

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

/** Icon names the apps draw for a vehicle class (ARCHITECTURE §21.1). */
export const VEHICLE_ICONS = ['sedan', 'universal', 'minivan', 'microbus', 'cargo_van', 'truck'] as const
export type VehicleIcon = (typeof VEHICLE_ICONS)[number]

/** An active vehicle class (body type) as GET /public/config lists it (§21). */
export interface VehicleClassInfo {
  code: string
  name: LocalizedText
  examples: string
  icon: VehicleIcon
  seats_default: number
  seats_max: number
  cargo_volume_l_default: number
  passenger_capable: boolean
  cargo_capable: boolean
}

export const CARGO_SIZE_CODES = ['s', 'm', 'l', 'xl'] as const
export type CargoSize = (typeof CARGO_SIZE_CODES)[number]

/** A parcel/other cargo size and its volume in litres (§21.2). */
export interface CargoSizeInfo {
  code: CargoSize
  volume_l: number
}

/** Passenger cancellation reason (§22.3); a comment is required when comment_required. */
export interface CancelReasonInfo {
  code: string
  label: LocalizedText
  comment_required: boolean
}

/** How the driver's commission is charged at acceptance (§22.2). */
export interface CommissionPolicy {
  mode: 'fixed' | 'percent'
  /** KZT charged per accepted order in 'fixed' mode. */
  fixed_amount: number
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
  /** Active vehicle classes in display order (empty when the API sends none). */
  vehicle_classes: VehicleClassInfo[]
  /** Cargo sizes from small to extra large. */
  cargo_sizes: CargoSizeInfo[]
  /** Most passengers one taxi order may carry. */
  max_passenger_count: number
  /** Passenger cancellation reasons in display order (passenger app only). */
  cancel_reasons: CancelReasonInfo[]
  /** Commission charged when a driver takes an order (driver app only). */
  commission: CommissionPolicy | null
  /** One-time balance credited to a newly approved driver, KZT; 0 = off (driver app only). */
  welcome_bonus: number | null
}

export const DEFAULT_MAX_ROUTE_POINTS = 5
export const DEFAULT_MAX_PASSENGER_COUNT = 15
/** Volumes of ARCHITECTURE §21.2, used when the API does not send cargo_sizes. */
export const DEFAULT_CARGO_SIZES: readonly CargoSizeInfo[] = [
  { code: 's', volume_l: 10 },
  { code: 'm', volume_l: 60 },
  { code: 'l', volume_l: 400 },
  { code: 'xl', volume_l: 1500 },
]
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
    vehicle_classes: [],
    cargo_sizes: DEFAULT_CARGO_SIZES.map((c) => ({ ...c })),
    max_passenger_count: DEFAULT_MAX_PASSENGER_COUNT,
    cancel_reasons: [],
    commission: null,
    welcome_bonus: null,
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

function nonNegativeInt(v: unknown): number | null {
  const n = intOrNull(v)
  return n !== null && n >= 0 ? n : null
}

function normalizeVehicleClass(v: unknown): VehicleClassInfo | null {
  const r = rec(v)
  const code = strOrNull(r.code)
  const seatsMax = nonNegativeInt(r.seats_max)
  if (!code || seatsMax === null) return null
  const icon = VEHICLE_ICONS.find((i) => i === r.icon) ?? 'sedan'
  const seatsDefault = nonNegativeInt(r.seats_default)
  return {
    code,
    name: localizedOrNull(r.name) ?? { kk: code, ru: code, en: code },
    examples: strOrNull(r.examples) ?? '',
    icon,
    seats_default: seatsDefault !== null && seatsDefault <= seatsMax ? seatsDefault : seatsMax,
    seats_max: seatsMax,
    cargo_volume_l_default: nonNegativeInt(r.cargo_volume_l_default) ?? 0,
    passenger_capable: r.passenger_capable === true,
    cargo_capable: r.cargo_capable === true,
  }
}

function normalizeCargoSizes(v: unknown): CargoSizeInfo[] {
  const list = Array.isArray(v) ? v : []
  return DEFAULT_CARGO_SIZES.map((d) => {
    const found = rec(list.find((x) => rec(x).code === d.code))
    const volume = intOrNull(found.volume_l)
    return { code: d.code, volume_l: volume !== null && volume > 0 ? volume : d.volume_l }
  })
}

function normalizeCancelReason(v: unknown): CancelReasonInfo | null {
  const r = rec(v)
  const code = strOrNull(r.code)
  const label = localizedOrNull(r.label)
  if (!code || !label) return null
  return { code, label, comment_required: r.comment_required === true }
}

function normalizeCommission(v: unknown): CommissionPolicy | null {
  if (!v || typeof v !== 'object') return null
  const r = rec(v)
  const amount = nonNegativeInt(r.fixed_amount)
  return { mode: r.mode === 'percent' ? 'percent' : 'fixed', fixed_amount: amount ?? 0 }
}

function uniqueBy<T>(items: Array<T | null>, key: (t: T) => string): T[] {
  const seen = new Set<string>()
  return items.filter((t): t is T => {
    if (t === null || seen.has(key(t))) return false
    seen.add(key(t))
    return true
  })
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
  const maxPassengers = intOrNull(r.max_passenger_count)
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
    vehicle_classes: uniqueBy(Array.isArray(r.vehicle_classes) ? r.vehicle_classes.map(normalizeVehicleClass) : [], (c) => c.code),
    cargo_sizes: normalizeCargoSizes(r.cargo_sizes),
    max_passenger_count: maxPassengers !== null && maxPassengers >= 1 && maxPassengers <= 60 ? maxPassengers : d.max_passenger_count,
    cancel_reasons: uniqueBy(Array.isArray(r.cancel_reasons) ? r.cancel_reasons.map(normalizeCancelReason) : [], (c) => c.code),
    commission: normalizeCommission(r.commission),
    welcome_bonus: nonNegativeInt(r.welcome_bonus),
  }
}

export async function fetchPublicConfig(app: 'passenger' | 'driver', signal?: AbortSignal): Promise<PublicConfig> {
  const raw = await http.get<unknown>('/public/config', { query: { app }, signal })
  return normalizePublicConfig(raw)
}
