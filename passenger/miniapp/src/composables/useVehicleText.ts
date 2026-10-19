// Texts about vehicle classes and order loads (ARCHITECTURE §21): the class
// name in the UI language (the API's name first, the app's own name for the
// seeded classes as a fallback), seats / volume captions and the "3
// passengers" / "Cargo: M, ≈ 60 l" line of an order.

import { useI18n } from 'vue-i18n'
import type { OrderLoad } from '../api/types'
import { normalizeLocale } from '../i18n/core'
import { pickLocalized, type CargoSize, type LocalizedText } from '../services/config'
import type { Locale } from '../utils/format'
import type { Category } from '../utils/status'

const NBSP = String.fromCharCode(0xa0)

/** "1 500" (kk/ru) or "1,500" (en). */
export function formatCount(n: number, locale: Locale): string {
  const digits = Math.max(0, Math.round(n)).toString()
  const sep = locale === 'en' ? ',' : NBSP
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, sep)
}

export function cargoSizeLabel(size: CargoSize): string {
  return size.toUpperCase()
}

export function useVehicleText() {
  const { t, te, locale } = useI18n()
  const lang = (): Locale => normalizeLocale(locale.value)

  function className(code: string, name: LocalizedText | null | undefined): string {
    const fromApi = pickLocalized(name, lang())
    if (fromApi) return fromApi
    const key = `vehicle_class.${code}`
    return te(key) ? t(key) : code
  }

  function seatsText(n: number): string {
    return t('confirm.seats_up_to', { n })
  }

  function volumeText(litres: number): string {
    return t('confirm.volume_up_to', { n: formatCount(litres, lang()) })
  }

  /** "M · ≈ 60 l" */
  function cargoText(size: CargoSize, litres: number): string {
    return `${cargoSizeLabel(size)} · ${t('cargo_hint.volume', { n: formatCount(litres, lang()) })}`
  }

  /** "Passengers: 3" for taxi, "Cargo: M, ≈ 60 l" for deliveries; '' when unknown. */
  function loadText(o: Pick<OrderLoad, 'passenger_count' | 'cargo_size' | 'cargo_volume_l'> & { category: Category }): string {
    if (o.category === 'taxi') return o.passenger_count ? t('order.passengers', { n: o.passenger_count }) : ''
    if (!o.cargo_size) return ''
    return t('order.cargo', { size: cargoSizeLabel(o.cargo_size), volume: formatCount(o.cargo_volume_l, lang()) })
  }

  return { className, seatsText, volumeText, cargoText, loadText }
}
