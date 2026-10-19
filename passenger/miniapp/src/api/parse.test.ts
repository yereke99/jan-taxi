import { describe, expect, it } from 'vitest'
import { parseOrder, parseOrderSummary, parseQuote } from './parse'

const fare = { recommended: 700, min: 700, max: 900, step: 50, options: [700, 750, 800, 850, 900] }

// Shape of admin/backend/internal/routing/preview.go routeQuote.
const preview = {
  quote_id: 'q-sedan',
  expires_at: '2026-10-19T10:10:00Z',
  category: 'taxi',
  vehicle_class: 'sedan',
  passenger_count: 3,
  cargo_size: null,
  city: { id: 'c1', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Emba' } },
  stops: [],
  distance_m: 3200,
  duration_s: 540,
  legs: [],
  geometry: null,
  bbox: null,
  fare,
  currency: 'KZT',
  commission_bps: 1000,
  payment_methods: ['cash'],
  options: [
    { vehicle_class: 'sedan', name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, examples: 'Toyota Camry', icon: 'sedan', seats_max: 4, cargo_volume_l: 400, quote_id: 'q-sedan', expires_at: '2026-10-19T10:10:00Z', fare, commission_bps: 1000, eta_s: 180 },
    { vehicle_class: 'minivan', name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' }, examples: 'Alphard', icon: 'minivan', seats_max: 8, cargo_volume_l: 800, quote_id: 'q-minivan', expires_at: '2026-10-19T10:10:00Z', fare: { ...fare, recommended: 1200, min: 1200, max: 1300, options: [1200, 1250, 1300] }, commission_bps: 1000, eta_s: null },
    { vehicle_class: 'broken', quote_id: 'q-x', fare: { options: [] } },
    { vehicle_class: 'sedan', quote_id: 'q-dup', fare },
  ],
}

describe('parseQuote', () => {
  it('maps one option per class and skips malformed or repeated ones', () => {
    const q = parseQuote(preview)
    expect(q.vehicle_class).toBe('sedan')
    expect(q.passenger_count).toBe(3)
    expect(q.cargo_size).toBeNull()
    expect(q.options.map((o) => o.quote_id)).toEqual(['q-sedan', 'q-minivan'])
    expect(q.options[0]).toMatchObject({ icon: 'sedan', seats_max: 4, eta_s: 180, examples: 'Toyota Camry' })
    expect(q.options[1]).toMatchObject({ icon: 'minivan', seats_max: 8, eta_s: null, fare: { recommended: 1200 } })
    expect(q.options[1]!.name?.ru).toBe('Минивэн')
  })

  it('makes a single option from the top level when the API sends none', () => {
    const q = parseQuote({ ...preview, options: undefined, vehicle_class: undefined, passenger_count: undefined })
    expect(q.vehicle_class).toBe('sedan')
    expect(q.passenger_count).toBeNull()
    expect(q.options).toEqual([
      { vehicle_class: 'sedan', name: null, examples: '', icon: 'sedan', seats_max: 0, cargo_volume_l: 0, quote_id: 'q-sedan', expires_at: preview.expires_at, fare, commission_bps: 1000, eta_s: null },
    ])
  })
})

const order = {
  id: 'o1',
  number: 1001,
  status: 'cancelled',
  category: 'parcel',
  vehicle_class: 'cargo_van',
  vehicle_class_name: { kk: 'Жүк фургоны', ru: 'Грузовой фургон', en: 'Cargo van' },
  passenger_count: null,
  cargo_size: 'xl',
  cargo_volume_l: 1500,
  stops: [],
  route: {},
  events: [],
  cancel_reason: 'Басқа көлік таптым',
  cancel_reason_code: 'other',
  cancelled_by: 'passenger',
  driver_eta_s: null,
  driver_eta_at: null,
  vehicle: { make: 'Ford', model: 'Transit', color: 'ақ', plate: '123 ABC 04', vehicle_class: 'cargo_van', vehicle_class_name: { kk: 'Жүк фургоны', ru: 'Грузовой фургон', en: 'Cargo van' }, seats: 2, cargo_volume_l: 6000 },
}

describe('parseOrder / parseOrderSummary', () => {
  it('maps the load, the vehicle class and the cancellation reason', () => {
    const o = parseOrder(order)
    expect(o).toMatchObject({
      vehicle_class: 'cargo_van',
      passenger_count: null,
      cargo_size: 'xl',
      cargo_volume_l: 1500,
      cancel_reason_code: 'other',
      driver_eta_s: null,
    })
    expect(o.vehicle_class_name?.en).toBe('Cargo van')
    expect(o.vehicle).toMatchObject({ vehicle_class: 'cargo_van', seats: 2, cargo_volume_l: 6000 })
  })

  it('degrades unknown or missing load fields safely', () => {
    const o = parseOrder({ ...order, status: 'accepted', vehicle_class: undefined, vehicle_class_name: null, cargo_size: 'huge', cancel_reason_code: 'bored', driver_eta_s: 240, driver_eta_at: '2026-10-19T10:00:00Z' })
    expect(o.vehicle_class).toBe('sedan')
    expect(o.vehicle_class_name).toBeNull()
    expect(o.cargo_size).toBeNull()
    expect(o.cancel_reason_code).toBeNull()
    expect(o.driver_eta_s).toBe(240)
    expect(o.driver_eta_at).toBe('2026-10-19T10:00:00Z')
  })

  it('maps the locality of pickup A and the tariff region (nationwide KAZ)', () => {
    const kaz = { code: 'KAZ', name: { kk: 'Қазақстан', ru: 'Казахстан', en: 'Kazakhstan' } }
    const o = parseOrder({ ...order, locality: ' Алматы ', tariff_region: kaz })
    expect(o.locality).toBe('Алматы')
    expect(o.tariff_region).toEqual(kaz)
    const s = parseOrderSummary({ id: 's1', status: 'completed', category: 'taxi', locality: 'Қосшы', tariff_region: { code: 'AST' } })
    expect(s).toMatchObject({ locality: 'Қосшы', tariff_region: { code: 'AST', name: { kk: 'AST', ru: 'AST', en: 'AST' } } })
  })

  it('never takes coordinates for a locality and tolerates an API without the fields', () => {
    expect(parseOrder({ ...order, locality: '43.238900, 76.889700' }).locality).toBeNull()
    const o = parseOrder(order)
    expect(o.locality).toBeNull()
    expect(o.tariff_region).toBeNull()
  })

  it('maps the load of a list row', () => {
    const s = parseOrderSummary({ id: 's1', status: 'completed', category: 'taxi', vehicle_class: 'minivan', passenger_count: 6, cargo_size: null, cargo_volume_l: 0 })
    expect(s).toMatchObject({ vehicle_class: 'minivan', passenger_count: 6, cargo_size: null, cargo_volume_l: 0 })
  })
})
