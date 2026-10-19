// Vehicle classes (body types, ARCHITECTURE §21): admin list/create/update
// (config.manage) and the public list every operator can read.

import { api } from '../services/api'
import type { LocalizedText } from './common'

/** Icons the apps can draw (vehicleclasses.Icons). */
export const VEHICLE_CLASS_ICONS = ['sedan', 'universal', 'minivan', 'microbus', 'cargo_van', 'truck'] as const
export type VehicleClassIcon = (typeof VEHICLE_CLASS_ICONS)[number]

/** Cargo sizes of parcel and other orders with their volume (vehicleclasses.CargoSizes). */
export const CARGO_SIZES = [
  { code: 's', volume_l: 10 },
  { code: 'm', volume_l: 60 },
  { code: 'l', volume_l: 400 },
  { code: 'xl', volume_l: 1500 },
] as const
export type CargoSize = (typeof CARGO_SIZES)[number]['code']

/** The class every legacy vehicle, tariff and order has. */
export const DEFAULT_VEHICLE_CLASS = 'sedan'

/** OpenAPI VehicleClass. */
export interface VehicleClass {
  code: string
  name: LocalizedText
  examples: string
  seats_default: number
  seats_max: number
  cargo_volume_l_default: number
  passenger_capable: boolean
  cargo_capable: boolean
  /** Class codes whose orders this class may take; the class itself first. */
  serves: string[]
  icon: VehicleClassIcon | (string & {})
  sort: number
  is_active: boolean
  created_at: string
  updated_at: string
}

/** Active class as published in GET /public/config (no serves, sort or timestamps). */
export type PublicVehicleClass = Pick<
  VehicleClass,
  'code' | 'name' | 'examples' | 'icon' | 'seats_default' | 'seats_max' | 'cargo_volume_l_default' | 'passenger_capable' | 'cargo_capable'
>

export interface VehicleClassInput {
  code?: string
  name: LocalizedText
  examples: string
  seats_default: number
  seats_max: number
  cargo_volume_l_default: number
  passenger_capable: boolean
  cargo_capable: boolean
  serves: string[]
  icon: string
  sort: number
  is_active: boolean
}

export const vehicleClassesApi = {
  /** Every class, inactive ones included, ordered by sort then code. */
  list(signal?: AbortSignal): Promise<{ items: VehicleClass[] }> {
    return api.get('/admin/vehicle-classes', { signal })
  },
  create(body: VehicleClassInput): Promise<VehicleClass> {
    return api.post('/admin/vehicle-classes', body)
  },
  /** Partial update; the code never changes. */
  update(code: string, body: Partial<Omit<VehicleClassInput, 'code'>>): Promise<VehicleClass> {
    return api.patch(`/admin/vehicle-classes/${encodeURIComponent(code)}`, body)
  },
}

/** Whether a class may carry orders of a category (taxi needs passengers, parcel/other cargo). */
export function classServesCategory(c: Pick<VehicleClass, 'passenger_capable' | 'cargo_capable'>, category: string): boolean {
  return category === 'taxi' ? c.passenger_capable : c.cargo_capable
}
