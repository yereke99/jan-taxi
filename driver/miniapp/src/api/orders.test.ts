import { describe, expect, it } from 'vitest'
import { normalizeOrderLoad, normalizePassenger, normalizeRegistration, safeTelegramUrl } from './normalize'
import { normalizeCard, normalizeDetail, normalizeSummary } from './orders'

describe('order load (§21)', () => {
  it('keeps the class, passenger count and cargo size of an order', () => {
    expect(
      normalizeOrderLoad({
        vehicle_class: 'minivan',
        vehicle_class_name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' },
        passenger_count: 6,
        cargo_size: null,
        cargo_volume_l: 0,
      }),
    ).toEqual({ vehicle_class: 'minivan', vehicle_class_name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' }, passenger_count: 6, cargo_size: null, cargo_volume_l: 0 })
    expect(normalizeOrderLoad({ vehicle_class: 'truck', cargo_size: 'xl', cargo_volume_l: 1500 })).toMatchObject({ cargo_size: 'xl', cargo_volume_l: 1500, passenger_count: null })
  })

  it('reads missing or unknown values as "not given"', () => {
    expect(normalizeOrderLoad({ cargo_size: 'huge', passenger_count: -1, cargo_volume_l: 'x' })).toEqual({
      vehicle_class: '',
      vehicle_class_name: null,
      passenger_count: null,
      cargo_size: null,
      cargo_volume_l: 0,
    })
  })

  it('normalises cards and details', () => {
    const card = normalizeCard({ id: 'o1', number: 7, fare: 900, vehicle_class: 'sedan', passenger_count: 2 })
    expect(card).toMatchObject({ id: 'o1', number: 7, fare: 900, vehicle_class: 'sedan', passenger_count: 2, cargo_size: null })
  })

  it('keeps the commission a cancelled order cost the driver (§22.2)', () => {
    expect(normalizeSummary({ id: 'o2', status: 'cancelled', fare: 1000, commission_amount: 50, commission_charged: 50 })).toMatchObject({ commission_charged: 50 })
    expect(normalizeDetail({ id: 'o2', status: 'cancelled', commission_charged: 50 })).toMatchObject({ commission_charged: 50 })
    expect(normalizeSummary({ id: 'o3', status: 'completed', commission_amount: 50 })).toMatchObject({ commission_charged: 0 })
  })
})

describe('passenger-written addresses (§3, §5)', () => {
  const typedA = { seq: 0, kind: 'pickup', mode: 'geocoded', lat: 48.8264, lon: 58.1442, address: 'Мешіттің жанында, көк қақпа', address_typed: true }
  const manualB = { seq: 1, kind: 'destination', mode: 'manual', lat: null, lon: null, address: 'Әженің үйі', address_typed: false }

  it('keeps address_typed on the stops of cards, details and history rows', () => {
    expect(normalizeCard({ id: 'o1', stops: [typedA, manualB] }).stops.map((s) => s.address_typed)).toEqual([true, false])
    const detail = normalizeDetail({ id: 'o1', stops: [{ ...typedA, id: 's1', address_details: null, completed_at: null }, { ...manualB, id: 's2' }] })
    expect(detail.stops.map((s) => [s.id, s.address, s.address_typed])).toEqual([
      ['s1', 'Мешіттің жанында, көк қақпа', true],
      ['s2', 'Әженің үйі', false],
    ])
    const row = normalizeSummary({ id: 'o1', pickup: typedA, destination: manualB })
    expect(row.pickup).toEqual(typedA)
    expect(row.destination).toEqual(manualB)
  })

  it('reads a missing or malformed flag as false', () => {
    const old = { seq: 0, kind: 'pickup', lat: 48.8264, lon: 58.1442, address: 'Абай көшесі, 12' }
    const card = normalizeCard({ id: 'o1', stops: [old, { ...old, seq: 1, kind: 'destination', address_typed: 'true' }] })
    expect(card.stops.map((s) => s.address_typed)).toEqual([false, false])
    expect(normalizeSummary({ id: 'o1', pickup: old, destination: { ...old, seq: 1, address_typed: 1 } })).toMatchObject({
      pickup: { address: 'Абай көшесі, 12', address_typed: false },
      destination: { address_typed: false },
    })
    expect(normalizeDetail({ id: 'o1' }).stops).toEqual([])
  })
})

describe('passenger contact (§22.4/§22.5)', () => {
  it('opens only Telegram chat links', () => {
    expect(safeTelegramUrl('https://t.me/aigerim_k')).toBe('https://t.me/aigerim_k')
    expect(safeTelegramUrl('tg://user?id=700000001')).toBe('tg://user?id=700000001')
    expect(safeTelegramUrl('https://t.me/aigerim_k?start=x')).toBeNull()
    expect(safeTelegramUrl('https://evil.example/aigerim')).toBeNull()
    expect(safeTelegramUrl('javascript:alert(1)')).toBeNull()
    expect(safeTelegramUrl('tg://resolve?domain=x')).toBeNull()
    expect(safeTelegramUrl(null)).toBeNull()
  })

  it('shows the phone only when it was verified through Telegram', () => {
    expect(
      normalizePassenger({ first_name: 'Айгерім', phone: '+77017654321', phone_verified: true, telegram_username: 'aigerim_k', telegram_url: 'https://t.me/aigerim_k' }),
    ).toEqual({ first_name: 'Айгерім', phone: '+77017654321', phone_verified: true, telegram_username: 'aigerim_k', telegram_url: 'https://t.me/aigerim_k' })
    expect(normalizePassenger({ first_name: 'Бауыржан', phone: '+77011112233', phone_verified: false, telegram_url: 'tg://user?id=42' })).toEqual({
      first_name: 'Бауыржан',
      phone: null,
      phone_verified: false,
      telegram_username: null,
      telegram_url: 'tg://user?id=42',
    })
    expect(normalizePassenger(null)).toBeNull()
  })

  it('a driver can never cancel, whatever the payload says', () => {
    const d = normalizeDetail({ id: 'o1', status: 'accepted', can_cancel: true, passenger: { first_name: 'А', telegram_url: 'tg://user?id=1' } })
    expect(d.can_cancel).toBe(false)
    expect(d.passenger?.telegram_url).toBe('tg://user?id=1')
  })
})

describe('registration vehicle class', () => {
  it('reads the class fields, null until a class is chosen', () => {
    const r = normalizeRegistration({
      vehicle: { make: 'Toyota', vehicle_class: 'universal', vehicle_class_name: { kk: 'Универсал', ru: 'Универсал', en: 'Estate' }, seats: 4, cargo_volume_l: 600 },
    })
    expect(r.vehicle).toMatchObject({ vehicle_class: 'universal', seats: 4, cargo_volume_l: 600 })
    expect(normalizeRegistration({ vehicle: { make: 'Toyota' } }).vehicle).toMatchObject({
      vehicle_class: null,
      vehicle_class_name: null,
      seats: null,
      cargo_volume_l: null,
    })
  })
})
