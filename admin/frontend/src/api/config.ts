// Cities, service zones, places, tariffs and runtime settings
// (shapes follow admin/backend/internal/{cities,pricing,settings}).

import { api } from '../services/api'
import type { Category } from '../utils/status'
import type { AdminRef, LatLon, LocalizedText, Page } from './common'
import { opt } from './common'

export interface City {
  id: string
  code: string
  name: LocalizedText
  center: LatLon
  default_zoom: number
  timezone: string
  is_active: boolean
  /**
   * The nationwide default region (code KAZ): its tariffs apply wherever no
   * city zone covers pickup A; it cannot be switched off (409 default_region_locked).
   */
  is_default_region?: boolean
  /** Legacy per-city dispatch columns: no longer read (dispatch.* settings, ARCHITECTURE §7). */
  dispatch_radius_m?: number
  require_driver_location?: boolean
  driver_location_max_age_s?: number
  search_timeout_s: number
  created_at: string
  updated_at: string
}

/** City fields an admin edits; dispatch is configured globally in the settings. */
export interface CityInput {
  code?: string
  name: LocalizedText
  center: LatLon
  default_zoom: number
  timezone: string
  is_active: boolean
  search_timeout_s: number
}

/** IANA zones the API accepts for a Kazakhstan city (cities.Timezones). */
export const CITY_TIMEZONES = ['Asia/Almaty', 'Asia/Aqtau', 'Asia/Aqtobe', 'Asia/Atyrau', 'Asia/Oral', 'Asia/Qostanay', 'Asia/Qyzylorda'] as const

export type Position = [number, number]

export interface PolygonGeometry {
  type: 'Polygon'
  coordinates: Position[][]
}

export interface MultiPolygonGeometry {
  type: 'MultiPolygon'
  coordinates: Position[][][]
}

export interface Zone {
  id: string
  city_id: string
  name: string
  is_active: boolean
  geometry: MultiPolygonGeometry | PolygonGeometry
  area_km2?: number
  created_at: string
  updated_at: string
}

export interface Place {
  id: string
  city_id: string | null
  name: LocalizedText
  aliases: string[]
  lat: number
  lon: number
  is_active: boolean
  created_at: string
  updated_at: string
}

export interface PlaceInput {
  city_id: string | null
  name: LocalizedText
  aliases: string[]
  lat: number
  lon: number
  is_active: boolean
}

export interface ExtraFee {
  code: string
  name: LocalizedText
  amount: number
}

export interface Tariff {
  id: string
  city_id: string
  category: Category
  /** Vehicle class the version prices (§21); versions are per city × category × class. */
  vehicle_class: string
  version: number
  is_active: boolean
  min_fare: number
  max_fare: number
  fare_step: number
  base_fare: number
  per_km: number
  per_minute: number
  extra_stop_fee: number
  extra_fees: ExtraFee[]
  commission_bps: number
  created_by: AdminRef | null
  created_at: string
}

export interface TariffInput {
  city_id: string
  category: Category
  vehicle_class: string
  min_fare: number
  max_fare: number
  fare_step: number
  base_fare: number
  per_km: number
  per_minute: number
  extra_stop_fee: number
  extra_fees: ExtraFee[]
  commission_bps: number
}

export type SettingKind = 'int' | 'bool' | 'string' | 'url' | 'localized_text' | 'int_list' | 'code_list' | 'string_list'

export interface SettingItem {
  key: string
  type: SettingKind
  value: unknown
  default: unknown
  source: 'database' | 'env' | 'default'
  has_env_default: boolean
  overrides_env: boolean
  description: LocalizedText
  constraints: {
    min?: number
    max?: number
    min_length?: number
    max_length?: number
    max_items?: number
    required?: boolean
  }
  /** Bounds such as commission.fixed_min/max: shown, never changed through PATCH. */
  read_only?: boolean
  updated_at: string | null
  updated_by: string | null
}

export const citiesApi = {
  /** All cities (the admin list is paged; 100 covers every realistic deployment). */
  list(signal?: AbortSignal): Promise<Page<City>> {
    return api.get('/admin/cities', { query: { page: 1, page_size: 100 }, signal })
  },
  create(body: CityInput): Promise<City> {
    return api.post('/admin/cities', body)
  },
  update(id: string, body: Partial<CityInput>): Promise<City> {
    return api.patch(`/admin/cities/${encodeURIComponent(id)}`, body)
  },
  zones(cityId: string, signal?: AbortSignal): Promise<{ items: Zone[] }> {
    return api.get(`/admin/cities/${encodeURIComponent(cityId)}/zones`, { signal })
  },
  createZone(cityId: string, body: { name: string; geometry: PolygonGeometry | MultiPolygonGeometry; is_active: boolean }): Promise<Zone> {
    return api.post(`/admin/cities/${encodeURIComponent(cityId)}/zones`, body)
  },
  updateZone(id: string, body: Partial<{ name: string; geometry: PolygonGeometry | MultiPolygonGeometry; is_active: boolean }>): Promise<Zone> {
    return api.patch(`/admin/zones/${encodeURIComponent(id)}`, body)
  },
  deleteZone(id: string): Promise<void> {
    return api.delete(`/admin/zones/${encodeURIComponent(id)}`)
  },
}

export const placesApi = {
  list(f: { city_id?: string; q?: string }, page: number, pageSize: number, signal?: AbortSignal): Promise<Page<Place>> {
    return api.get('/admin/places', { query: { city_id: opt(f.city_id), q: opt(f.q?.trim()), page, page_size: pageSize }, signal })
  },
  create(body: PlaceInput): Promise<Place> {
    return api.post('/admin/places', body)
  },
  update(id: string, body: Partial<PlaceInput>): Promise<Place> {
    return api.patch(`/admin/places/${encodeURIComponent(id)}`, body)
  },
  remove(id: string): Promise<void> {
    return api.delete(`/admin/places/${encodeURIComponent(id)}`)
  },
}

export const tariffsApi = {
  list(f: { city_id?: string; category?: string; vehicle_class?: string; include_inactive?: boolean }, signal?: AbortSignal): Promise<{ items: Tariff[] }> {
    return api.get('/admin/tariffs', {
      query: {
        city_id: opt(f.city_id),
        category: opt(f.category),
        vehicle_class: opt(f.vehicle_class),
        include_inactive: f.include_inactive ? 'true' : undefined,
      },
      signal,
    })
  },
  create(body: TariffInput): Promise<Tariff> {
    return api.post('/admin/tariffs', body)
  },
}

export const settingsApi = {
  list(signal?: AbortSignal): Promise<{ items: SettingItem[] }> {
    return api.get('/admin/settings', { signal })
  },
  /** `null` restores the env/built-in default for that key. */
  update(values: Record<string, unknown>): Promise<{ items: SettingItem[] }> {
    return api.patch('/admin/settings', values)
  },
}
