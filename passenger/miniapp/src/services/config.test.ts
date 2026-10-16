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
  })

  it('maps the driver sections', () => {
    const c = normalizePublicConfig({
      ...passenger,
      app: 'driver',
      fare: undefined,
      kaspi: { payment_url: 'https://pay.kaspi.kz/pay/x', recipient_name: 'Jan Taxi', instructions: { kk: 'Төлеңіз', ru: 'Оплатите', en: 'Pay' } },
      topup: { min: 500, max: 100000, presets: [1000, 2000, 200000] },
    })
    expect(c.fare).toBeNull()
    expect(c.kaspi?.recipient_name).toBe('Jan Taxi')
    expect(pickLocalized(c.kaspi?.instructions, 'ru')).toBe('Оплатите')
    expect(c.topup).toEqual({ min: 500, max: 100000, presets: [1000, 2000] })
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
