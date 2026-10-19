// Vehicle classes (ARCHITECTURE §21) and the driver-only parts of the public
// config (§22): commission policy and welcome bonus. The shared config module
// keeps the common fields; this reads the extra ones from the same payload
// (GET /public/config?app=driver) so the app makes one request.

import type { LocalizedText } from '../../api/types'
import { intOrNull, localizedOrNull, rec, str } from '../../api/normalize'

export const VEHICLE_ICONS = ['sedan', 'universal', 'minivan', 'microbus', 'cargo_van', 'truck'] as const
export type VehicleIcon = (typeof VEHICLE_ICONS)[number]

/** An active vehicle class as the apps see it (vehicleclasses.PublicClass). */
export interface VehicleClassOption {
  code: string
  name: LocalizedText
  /** Short text such as "Toyota Camry"; may be empty. */
  examples: string
  icon: VehicleIcon
  seats_default: number
  seats_max: number
  cargo_volume_l_default: number
  passenger_capable: boolean
  cargo_capable: boolean
}

export interface CommissionPolicy {
  mode: 'fixed' | 'percent'
  /** KZT charged when a driver takes an order (fixed mode). */
  fixed_amount: number
}

export interface DriverConfigExtras {
  /** Active classes in display order; empty when the config is unavailable. */
  vehicleClasses: VehicleClassOption[]
  /** Null when unknown (config unavailable). */
  commission: CommissionPolicy | null
  /** KZT credited once on first approval; 0 = off, null = unknown. */
  welcomeBonus: number | null
}

/** Upper bounds of the API (vehicleclasses.MaxSeats / MaxCargoVolumeL). */
export const MAX_SEATS = 60
export const MAX_CARGO_VOLUME_L = 100_000

const CLASS_CODE_RE = /^[a-z_]{2,20}$/

function isIcon(v: unknown): v is VehicleIcon {
  return (VEHICLE_ICONS as readonly string[]).includes(str(v))
}

function normalizeClass(v: unknown): VehicleClassOption | null {
  const r = rec(v)
  const code = str(r.code)
  const seatsMax = intOrNull(r.seats_max)
  if (!CLASS_CODE_RE.test(code) || seatsMax === null || seatsMax < 1 || seatsMax > MAX_SEATS) return null
  const seatsDefault = intOrNull(r.seats_default)
  const cargo = intOrNull(r.cargo_volume_l_default)
  return {
    code,
    name: localizedOrNull(r.name) ?? { kk: code, ru: code, en: code },
    examples: str(r.examples).trim(),
    icon: isIcon(r.icon) ? r.icon : isIcon(code) ? code : 'sedan',
    seats_default: seatsDefault !== null && seatsDefault >= 1 && seatsDefault <= seatsMax ? seatsDefault : seatsMax,
    seats_max: seatsMax,
    cargo_volume_l_default: cargo !== null && cargo <= MAX_CARGO_VOLUME_L ? cargo : 0,
    passenger_capable: r.passenger_capable === true,
    cargo_capable: r.cargo_capable === true,
  }
}

export function normalizeVehicleClasses(v: unknown): VehicleClassOption[] {
  if (!Array.isArray(v)) return []
  const seen = new Set<string>()
  const out: VehicleClassOption[] = []
  for (const raw of v) {
    const c = normalizeClass(raw)
    if (c && !seen.has(c.code)) {
      seen.add(c.code)
      out.push(c)
    }
  }
  return out
}

function normalizeCommission(v: unknown): CommissionPolicy | null {
  if (!v || typeof v !== 'object') return null
  const r = rec(v)
  const amount = intOrNull(r.fixed_amount)
  if (r.mode === 'percent') return { mode: 'percent', fixed_amount: amount ?? 0 }
  if (r.mode === 'fixed' && amount !== null && amount > 0) return { mode: 'fixed', fixed_amount: amount }
  return null
}

/** Reads the driver-only fields of a public config payload. */
export function normalizeDriverExtras(raw: unknown): DriverConfigExtras {
  const r = rec(raw)
  return {
    vehicleClasses: normalizeVehicleClasses(r.vehicle_classes),
    commission: normalizeCommission(r.commission),
    welcomeBonus: intOrNull(r.welcome_bonus),
  }
}

export function emptyDriverExtras(): DriverConfigExtras {
  return { vehicleClasses: [], commission: null, welcomeBonus: null }
}

export function findClass(classes: readonly VehicleClassOption[], code: string | null | undefined): VehicleClassOption | null {
  if (!code) return null
  return classes.find((c) => c.code === code) ?? null
}

/**
 * The commission a new order costs when it is known in advance (fixed mode);
 * null in percent mode, where it depends on the fare.
 */
export function fixedCommission(policy: CommissionPolicy | null): number | null {
  return policy && policy.mode === 'fixed' && policy.fixed_amount > 0 ? policy.fixed_amount : null
}
