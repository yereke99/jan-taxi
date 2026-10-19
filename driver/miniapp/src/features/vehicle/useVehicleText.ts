// Texts for vehicle classes and order loads (§21): class names, "4 орын ·
// 400 л" capacities, passenger counts and cargo sizes, in the UI language.

import { useI18n } from 'vue-i18n'
import type { CargoSize, LocalizedText, OrderLoad } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { pickLocalized } from '../../services/config'
import type { Locale } from '../../utils/format'
import { useSessionStore } from '../../stores/session'
import { findClass } from './classes'

const NBSP = String.fromCharCode(0xa0)

/** "15 000" (kk/ru) or "15,000" (en). */
export function groupDigits(n: number, locale: Locale): string {
  const digits = Math.abs(Math.trunc(n)).toString()
  const sep = locale === 'en' ? ',' : NBSP
  let out = ''
  for (let i = 0; i < digits.length; i++) {
    if (i > 0 && (digits.length - i) % 3 === 0) out += sep
    out += digits[i]
  }
  return out
}

export interface VehicleText {
  /** The class name: from the order/vehicle payload, else the config, else the code. */
  className: (code: string | null | undefined, name?: LocalizedText | null) => string
  seats: (n: number) => string
  litres: (n: number) => string
  /** "4 орын · 400 л"; parts that are unknown are left out. */
  capacity: (seats: number | null, litres: number | null) => string
  cargoSize: (size: CargoSize) => string
  /** What the order needs besides the class: "3 жолаушы" or "Орташа (M) · 60 л"; '' when nothing. */
  load: (o: Pick<OrderLoad, 'passenger_count' | 'cargo_size' | 'cargo_volume_l'>) => string
}

export function useVehicleText(): VehicleText {
  const { t } = useI18n()
  const fmt = useFormat()
  const session = useSessionStore()

  const litres = (n: number): string => t('vehicle.litres', { n: groupDigits(n, fmt.locale.value) })
  const seats = (n: number): string => t('vehicle.seats', { n })

  return {
    className(code, name) {
      const fromPayload = pickLocalized(name ?? null, fmt.locale.value)
      if (fromPayload) return fromPayload
      const c = findClass(session.extras.vehicleClasses, code)
      return c ? pickLocalized(c.name, fmt.locale.value) : (code ?? '')
    },
    seats,
    litres,
    capacity(s, l) {
      const parts: string[] = []
      if (s !== null && s > 0) parts.push(seats(s))
      if (l !== null && l > 0) parts.push(litres(l))
      return parts.join(' · ')
    },
    cargoSize: (size) => t(`vehicle.cargo_size.${size}`),
    load(o) {
      if (o.passenger_count !== null && o.passenger_count > 0) return t('vehicle.passengers', { n: o.passenger_count })
      if (o.cargo_size) {
        const size = t(`vehicle.cargo_size.${o.cargo_size}`)
        return o.cargo_volume_l > 0 ? `${size} · ${litres(o.cargo_volume_l)}` : size
      }
      return ''
    },
  }
}
