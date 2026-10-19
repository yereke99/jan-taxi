// Shapes shared by several admin endpoints (admin/backend/api/openapi.yaml,
// checked against the Go handlers).

import type { OrderStatus } from '../utils/status'

export type { Page } from '../services/api'

export type Language = 'kk' | 'ru' | 'en'

export interface LocalizedText {
  kk: string
  ru: string
  en: string
}

export interface LatLon {
  lat: number
  lon: number
}

export interface CityRef {
  id: string
  code: string
  name: LocalizedText
}

/** Code of the nationwide default region: its tariffs apply wherever no city zone covers pickup A. */
export const DEFAULT_REGION_CODE = 'KAZ'

/** The city or region whose tariff priced an order. */
export interface TariffRegion {
  code: string
  name: LocalizedText
}

/** True for the nationwide default region (it cannot be switched off). */
export function isDefaultRegion(c: { code?: string | null; is_default_region?: boolean | null } | null | undefined): boolean {
  if (!c) return false
  return c.is_default_region === true || (c.code ?? '').toUpperCase() === DEFAULT_REGION_CODE
}

/** "48.826400, 58.144200"-like text: coordinates are never shown as a place name. */
const COORDINATES_RE = /^\s*-?\d{1,3}(?:[.,]\d+)?\s*[,;]\s*-?\d{1,3}(?:[.,]\d+)?\s*$/

/**
 * The settlement of pickup A as the API reverse-geocoded it, trimmed; null
 * when unknown or when the text is only coordinates.
 */
export function localityText(v: string | null | undefined): string | null {
  const s = typeof v === 'string' ? v.trim() : ''
  return s && !COORDINATES_RE.test(s) ? s : null
}

export interface AdminRef {
  id: string
  email: string
  full_name: string
}

export interface DriverRef {
  id: string
  code: string
  first_name: string
  last_name: string
  phone: string | null
}

export interface OrderRef {
  id: string
  number: number
  status: OrderStatus
}

export interface FileRef {
  id: string
  kind: string
  url: string
  width: number
  height: number
  created_at: string
}

/** Vehicle class (body type), seats and cargo space of a vehicle (§21); null until a class is chosen. */
export interface VehicleClassFields {
  vehicle_class: string | null
  vehicle_class_name: LocalizedText | null
  seats: number | null
  cargo_volume_l: number | null
}

export interface Vehicle extends Partial<VehicleClassFields> {
  make: string
  model: string
  color: string
  plate: string
}

export type ActorType = 'passenger' | 'driver' | 'admin' | 'system'

/** Query value helpers: empty strings are omitted from the request. */
export function opt(v: string | null | undefined): string | undefined {
  return v === null || v === undefined || v === '' ? undefined : v
}

/** Local calendar date (YYYY-MM-DD) → RFC 3339 instant of its local midnight. */
export function dayStart(date: string | null | undefined): string | undefined {
  if (!date) return undefined
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return undefined
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])).toISOString()
}

/** Local calendar date → RFC 3339 instant of the following local midnight (exclusive end). */
export function dayEnd(date: string | null | undefined): string | undefined {
  if (!date) return undefined
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!m) return undefined
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + 1).toISOString()
}

/** Today's local date as YYYY-MM-DD. */
export function localDate(d: Date = new Date()): string {
  const p = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}
