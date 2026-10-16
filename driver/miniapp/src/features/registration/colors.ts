// Common car colours offered in the vehicle step. The colour is stored as free
// text (vehicles.color) in the language the driver picked it in; a value in
// any of the three languages is recognised as the same colour.

import type { Locale } from '../../utils/format'

export interface VehicleColor {
  id: string
  /** The car colour itself, shown as a small swatch. */
  swatch: string
  name: Record<Locale, string>
}

export const VEHICLE_COLORS: readonly VehicleColor[] = [
  { id: 'white', swatch: '#FFFFFF', name: { kk: 'Ақ', ru: 'Белый', en: 'White' } },
  { id: 'black', swatch: '#000000', name: { kk: 'Қара', ru: 'Чёрный', en: 'Black' } },
  { id: 'silver', swatch: '#C7C7CC', name: { kk: 'Күміс түсті', ru: 'Серебристый', en: 'Silver' } },
  { id: 'gray', swatch: '#8E8E93', name: { kk: 'Сұр', ru: 'Серый', en: 'Grey' } },
  { id: 'blue', swatch: '#1D4ED8', name: { kk: 'Көк', ru: 'Синий', en: 'Blue' } },
  { id: 'red', swatch: '#C62828', name: { kk: 'Қызыл', ru: 'Красный', en: 'Red' } },
  { id: 'brown', swatch: '#6D4C41', name: { kk: 'Қоңыр', ru: 'Коричневый', en: 'Brown' } },
  { id: 'beige', swatch: '#D9C8A9', name: { kk: 'Беж', ru: 'Бежевый', en: 'Beige' } },
  { id: 'green', swatch: '#2E7D32', name: { kk: 'Жасыл', ru: 'Зелёный', en: 'Green' } },
  { id: 'yellow', swatch: '#F5C400', name: { kk: 'Сары', ru: 'Жёлтый', en: 'Yellow' } },
]

function key(s: string): string {
  return s.trim().toLocaleLowerCase().replace(/ё/g, 'е')
}

/** The listed colour a stored value names (in any language), or null for a custom colour. */
export function matchColor(value: string): VehicleColor | null {
  const k = key(value)
  if (!k) return null
  return VEHICLE_COLORS.find((c) => Object.values(c.name).some((n) => key(n) === k)) ?? null
}
