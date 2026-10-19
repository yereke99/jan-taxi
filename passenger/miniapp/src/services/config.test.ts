// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { DEFAULT_MAP_CENTER, DEFAULT_MAP_STYLE_URL } from '../maps/constants'
import { defaultPublicConfig, normalizePublicConfig, pickLocalized } from './config'

// Shape served by admin/backend/internal/cities/publicconfig.go.
const passenger = {
  app: 'passenger',
  default_language: 'kk',
  languages: ['kk', 'ru', 'en'],
  city: { code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Embi' }, center: { lat: 48.8264, lon: 58.1442 }, zoom: 14 },
  map: { style_url: 'https://tiles.openfreemap.org/styles/positron' },
  routing: { max_points: 4 },
  bots: {
    passenger_username: 'jantaxi_bot',
    driver_username: 'jantaxi_driver_bot',
    passenger_link: 'https://t.me/jantaxi_bot?startapp=home&mode=fullscreen',
    driver_link: 'https://t.me/jantaxi_driver_bot?startapp=home&mode=fullscreen',
    driver_register_link: 'https://t.me/jantaxi_driver_bot?startapp=register&mode=fullscreen',
  },
  support: { telegram_username: 'jantaxi_support', telegram_link: 'https://t.me/jantaxi_support', phone: '+77001234567', hours: { kk: '09:00–21:00', ru: '09:00–21:00', en: '9am–9pm' } },
  delivery: { prohibited_items: ['weapons', 'drugs'] },
  navigation: { twogis_base_url: 'https://2gis.kz' },
  fare: { min: 500, max: 1500, step: 50 },
  vehicle_classes: [
    { code: 'sedan', name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, examples: 'Toyota Camry', icon: 'sedan', seats_default: 4, seats_max: 4, cargo_volume_l_default: 400, passenger_capable: true, cargo_capable: true },
    { code: 'minivan', name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' }, examples: 'Alphard', icon: 'minivan', seats_default: 7, seats_max: 8, cargo_volume_l_default: 800, passenger_capable: true, cargo_capable: true },
    { code: 'truck', name: { kk: 'Жүк көлігі', ru: 'Грузовик', en: 'Truck' }, examples: '', icon: 'truck', seats_default: 2, seats_max: 2, cargo_volume_l_default: 15000, passenger_capable: false, cargo_capable: true },
  ],
  cargo_sizes: [{ code: 's', volume_l: 10 }, { code: 'm', volume_l: 60 }, { code: 'l', volume_l: 400 }, { code: 'xl', volume_l: 1500 }],
  max_passenger_count: 15,
  cancel_reasons: [
    { code: 'long_wait', label: { kk: 'Ұзақ күттім', ru: 'Долго ждать', en: 'Waited too long' }, comment_required: false },
    { code: 'other', label: { kk: 'Басқа себеп', ru: 'Другое', en: 'Other' }, comment_required: true },
  ],
}

describe('normalizePublicConfig', () => {
  it('maps the passenger payload', () => {
    const c = normalizePublicConfig(passenger)
    expect(c.city?.code).toBe('EMB')
    expect(c.map).toEqual({ style_url: passenger.map.style_url, default_center: { lat: 48.8264, lon: 58.1442 }, default_zoom: 14 })
    expect(c.routing.max_points).toBe(4)
    expect(c.bots.driver_register_link).toBe(passenger.bots.driver_register_link)
    expect(c.support.telegram_link).toBe('https://t.me/jantaxi_support')
    expect(pickLocalized(c.support.hours, 'en')).toBe('9am–9pm')
    expect(c.delivery.prohibited_items).toEqual(['weapons', 'drugs'])
    expect(c.fare).toEqual({ min: 500, max: 1500, step: 50 })
    expect(c.kaspi).toBeNull()
    expect(c.topup).toBeNull()
    expect(c.vehicle_classes.map((v) => v.code)).toEqual(['sedan', 'minivan', 'truck'])
    expect(c.vehicle_classes[1]).toMatchObject({ icon: 'minivan', seats_default: 7, seats_max: 8, passenger_capable: true })
    expect(c.vehicle_classes[2]?.passenger_capable).toBe(false)
    expect(c.cargo_sizes.map((s) => s.volume_l)).toEqual([10, 60, 400, 1500])
    expect(c.max_passenger_count).toBe(15)
    expect(c.cancel_reasons.map((r) => [r.code, r.comment_required])).toEqual([
      ['long_wait', false],
      ['other', true],
    ])
    expect(pickLocalized(c.cancel_reasons[0]?.label, 'ru')).toBe('Долго ждать')
    expect(c.commission).toBeNull()
    expect(c.welcome_bonus).toBeNull()
  })

  it('maps the driver sections', () => {
    const c = normalizePublicConfig({
      ...passenger,
      app: 'driver',
      fare: undefined,
      kaspi: { payment_url: 'https://pay.kaspi.kz/pay/x', recipient_name: 'Jan Taxi', instructions: { kk: 'Төлеңіз', ru: 'Оплатите', en: 'Pay' } },
      topup: { min: 500, max: 100000, presets: [1000, 2000, 200000] },
      cancel_reasons: undefined,
      commission: { mode: 'fixed', fixed_amount: 50 },
      welcome_bonus: 500,
    })
    expect(c.fare).toBeNull()
    expect(c.kaspi?.recipient_name).toBe('Jan Taxi')
    expect(pickLocalized(c.kaspi?.instructions, 'ru')).toBe('Оплатите')
    expect(c.topup).toEqual({ min: 500, max: 100000, presets: [1000, 2000] })
    expect(c.cancel_reasons).toEqual([])
    expect(c.commission).toEqual({ mode: 'fixed', fixed_amount: 50 })
    expect(c.welcome_bonus).toBe(500)
  })

  it('keeps only well-formed vehicle classes, cargo sizes and reasons', () => {
    const c = normalizePublicConfig({
      vehicle_classes: [
        { code: 'sedan', seats_max: 4, seats_default: 9, icon: 'rocket' },
        { code: 'sedan', seats_max: 5 },
        { code: '', seats_max: 4 },
        { code: 'van', seats_max: -1 },
      ],
      cargo_sizes: [{ code: 'm', volume_l: 80 }, { code: 'xl', volume_l: 0 }],
      max_passenger_count: 0,
      cancel_reasons: [{ code: 'x' }, { code: 'other', label: { ru: 'Другое' }, comment_required: true }],
      welcome_bonus: -5,
    })
    expect(c.vehicle_classes).toEqual([
      {
        code: 'sedan',
        name: { kk: 'sedan', ru: 'sedan', en: 'sedan' },
        examples: '',
        icon: 'sedan',
        seats_default: 4,
        seats_max: 4,
        cargo_volume_l_default: 0,
        passenger_capable: false,
        cargo_capable: false,
      },
    ])
    expect(c.cargo_sizes).toEqual([
      { code: 's', volume_l: 10 },
      { code: 'm', volume_l: 80 },
      { code: 'l', volume_l: 400 },
      { code: 'xl', volume_l: 1500 },
    ])
    expect(c.max_passenger_count).toBe(15)
    expect(c.cancel_reasons.map((r) => r.code)).toEqual(['other'])
    expect(pickLocalized(c.cancel_reasons[0]?.label, 'kk')).toBe('Другое')
    expect(c.welcome_bonus).toBeNull()
  })

  it('falls back to safe defaults for missing or invalid fields', () => {
    const c = normalizePublicConfig({ city: null, map: { style_url: 'javascript:alert(1)' }, routing: { max_points: 9 }, bots: { driver_link: 'http://t.me/x' } })
    const d = defaultPublicConfig()
    expect(c.city).toBeNull()
    expect(c.map.style_url).toBe(DEFAULT_MAP_STYLE_URL)
    expect(c.map.default_center).toEqual(DEFAULT_MAP_CENTER)
    expect(c.routing.max_points).toBe(d.routing.max_points)
    expect(c.bots.driver_link).toBeNull()
    expect(normalizePublicConfig(null)).toEqual(d)
  })
})
