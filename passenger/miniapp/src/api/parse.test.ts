import { describe, expect, it } from 'vitest'
import { parseBonusPage, parseOrder, parseOrderSummary, parsePromotions, parseQuote, parseReferral } from './parse'

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

  it('reads which stops the passenger wrote (address_typed); an older payload without it reads false', () => {
    const typedA = { seq: 0, kind: 'pickup', lat: 48.82, lon: 58.14, address: 'Мешіттің жанында, көк қақпа', address_typed: true }
    const manualB = { seq: 1, kind: 'destination', mode: 'manual', lat: null, lon: null, address: 'Ескі мәдениет үйі', address_typed: false }
    const q = parseQuote({ ...preview, stops: [typedA, { ...manualB, address_typed: undefined }] })
    expect(q.stops.map((x) => [x.mode, x.address_typed])).toEqual([['geocoded', true], ['manual', false]])

    const o = parseOrder({ ...order, stops: [{ ...typedA, id: 'a' }, { ...manualB, id: 'b' }] })
    expect(o.stops.map((x) => x.address_typed)).toEqual([true, false])
    expect(parseOrder({ ...order, stops: [{ id: 'a', seq: 0, lat: 48.82, lon: 58.14, address: 'A' }] }).stops[0]!.address_typed).toBe(false)

    const sum = parseOrderSummary({ id: 's1', status: 'completed', category: 'taxi', pickup: typedA, destination: { ...manualB, address_typed: 'yes' } })!
    expect(sum.pickup.address_typed).toBe(true)
    expect(sum.destination.address_typed).toBe(false)
  })

  it("keeps only a Telegram chat link for the driver's telegram_url", () => {
    const driver = { id: 'd1', code: 'EMB001', first_name: 'Ерлан', last_name: 'С.', phone: null, rating: 4.9 }
    const url = (telegram_url: unknown) => parseOrder({ ...order, status: 'accepted', driver: { ...driver, telegram_url } }).driver?.telegram_url
    expect(url('https://t.me/erlan_driver')).toBe('https://t.me/erlan_driver')
    expect(url('tg://user?id=700000001')).toBe('tg://user?id=700000001')
    for (const bad of ['javascript:alert(1)', 'https://evil.example/erlan', 'https://t.me/erlan_driver?start=x', 'tg://resolve?domain=x', 'tg://user?id=0', 42, null, undefined]) {
      expect(url(bad)).toBeNull()
    }
  })

  it('maps the load of a list row', () => {
    const s = parseOrderSummary({ id: 's1', status: 'completed', category: 'taxi', vehicle_class: 'minivan', passenger_count: 6, cargo_size: null, cargo_volume_l: 0 })
    expect(s).toMatchObject({ vehicle_class: 'minivan', passenger_count: 6, cargo_size: null, cargo_volume_l: 0 })
  })
})

describe('order promotion (§23.6)', () => {
  it('maps the discount, the cash due and the promotion of an order', () => {
    const promotion = { kind: 'coupon', title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }, discount: 400, gross_fare: 1500, cash_due: 1100, status: 'reserved', compensation: null, settled_at: null }
    const o = parseOrder({ ...order, status: 'searching', fare: 1500, promo_discount: 400, cash_due: 1100, promotion })
    expect(o).toMatchObject({ fare: 1500, promo_discount: 400, cash_due: 1100 })
    expect(o.promotion).toEqual(promotion)
    const s = parseOrderSummary({ id: 's1', status: 'completed', category: 'taxi', fare: 1500, promo_discount: 400, cash_due: 1100 })
    expect(s).toMatchObject({ fare: 1500, promo_discount: 400, cash_due: 1100 })
  })

  it('reads an order without a promotion, or from an API without them, as the full fare', () => {
    const o = parseOrder({ ...order, fare: 700 })
    expect(o).toMatchObject({ fare: 700, promo_discount: 0, cash_due: 700, promotion: null })
    expect(parseOrderSummary({ id: 's1', status: 'completed', category: 'taxi', fare: 700 })).toMatchObject({ promo_discount: 0, cash_due: 700 })
    // A cash due missing from the payload is the fare less the discount; an unknown kind is no promotion.
    expect(parseOrder({ ...order, fare: 1500, promo_discount: 300, promotion: { kind: 'voucher' } })).toMatchObject({ cash_due: 1200, promotion: null })
  })
})

// Shape of admin/backend/internal/marketing/passenger.go PassengerPromotions.
const promotions = {
  server_time: '2026-11-02T10:00:00Z',
  redemptions_enabled: true,
  coupons: [
    { id: 'c1', title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }, amount: 400, min_order_fare: 400, categories: ['taxi'], status: 'issued', issued_at: '2026-11-01T10:00:00Z', expires_at: '2026-11-04T10:00:00Z', reserved_order_id: null, redeemed_at: null },
    { id: 'c2', title: null, amount: 400, min_order_fare: 400, categories: ['taxi', 'rocket'], status: 'redeemed', issued_at: '2026-10-01T10:00:00Z', expires_at: '2026-10-04T10:00:00Z', reserved_order_id: null, redeemed_at: '2026-10-02T10:00:00Z' },
    { id: 'c3', amount: 400, status: 'lost' },
    { id: 'c4', amount: 0, status: 'issued' },
  ],
  bonus: { balance: 300, reserved: 100, available: 200 },
  bonus_categories: ['taxi'],
  offers: [
    { kind: 'first_ride_coupon', title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }, description: { kk: 'Сипаттама', ru: 'Описание', en: 'Text' }, amount: 400, validity_hours: 72, categories: ['taxi'] },
    { kind: 'lottery', amount: 1000 },
  ],
  banners: [{ id: 'b1', title: { kk: 'Жаңалық', ru: 'Новость', en: 'News' }, description: { kk: '', ru: '', en: '' } }, { id: 'b2' }],
  referral: { state: 'locked', lock_reason: 'coupon_not_redeemed', reward_amount: 300, rewarded: false },
  marketing_subscribed: false,
}

describe('parsePromotions', () => {
  it('maps coupons, the bonus, offers, banners and the invitation state', () => {
    const p = parsePromotions(promotions)
    expect(p.server_time).toBe('2026-11-02T10:00:00Z')
    expect(p.redemptions_enabled).toBe(true)
    expect(p.coupons.map((c) => c.id)).toEqual(['c1', 'c2'])
    expect(p.coupons[0]).toEqual({ ...promotions.coupons[0] })
    expect(p.coupons[1]!.categories).toEqual(['taxi'])
    expect(p.bonus).toEqual({ balance: 300, reserved: 100, available: 200 })
    expect(p.bonus_categories).toEqual(['taxi'])
    expect(p.offers).toEqual([{ ...promotions.offers[0] }])
    expect(p.banners.map((b) => b.id)).toEqual(['b1'])
    expect(p.referral).toEqual({ state: 'locked', lock_reason: 'coupon_not_redeemed', reward_amount: 300, rewarded: false })
    expect(p.marketing_subscribed).toBe(false)
  })

  it('reads a missing part as nothing to offer and keeps marketing messages on by default', () => {
    const p = parsePromotions({ server_time: '2026-11-02T10:00:00Z', bonus: { balance: 500, reserved: 200 }, referral: { state: 'paused', lock_reason: 'later' } })
    expect(p).toMatchObject({ redemptions_enabled: false, coupons: [], offers: [], banners: [], bonus_categories: [], marketing_subscribed: true })
    expect(p.bonus).toEqual({ balance: 500, reserved: 200, available: 300 })
    expect(p.referral).toEqual({ state: 'unavailable', lock_reason: null, reward_amount: 0, rewarded: false })
  })
})

describe('parseReferral / parseBonusPage', () => {
  it('keeps only the invitation links of the passenger bot and a well-formed code', () => {
    const v = parseReferral({
      state: 'unlocked',
      reward_amount: 300,
      rewarded: true,
      title: { kk: 'Досыңды шақыр', ru: 'Пригласи друга', en: 'Invite a friend' },
      description: { kk: 'Сипаттама', ru: 'Описание', en: 'Text' },
      code: 'AB2C3D4E',
      link: 'https://t.me/jantaxi_bot?start=ref_AB2C3D4E',
      app_link: 'https://evil.example/jantaxi_bot?startapp=ref_AB2C3D4E',
      earned: 300,
      friends: [
        { name: 'Дана Қ.', status: 'rewarded', attributed_at: '2026-10-30T10:00:00Z' },
        { name: '', status: 'not_counted', attributed_at: '2026-10-31T10:00:00Z' },
        { name: 'X', status: 'review', attributed_at: '2026-10-31T10:00:00Z' },
      ],
    })
    expect(v).toMatchObject({ state: 'unlocked', lock_reason: null, reward_amount: 300, rewarded: true, code: 'AB2C3D4E', earned: 300 })
    expect(v.link).toBe('https://t.me/jantaxi_bot?start=ref_AB2C3D4E')
    expect(v.app_link).toBeNull()
    expect(v.friends).toEqual([
      { name: 'Дана Қ.', status: 'rewarded', attributed_at: '2026-10-30T10:00:00Z' },
      { name: 'Telegram', status: 'not_counted', attributed_at: '2026-10-31T10:00:00Z' },
    ])
    expect(parseReferral({ state: 'unlocked', code: 'a b', link: 'javascript:alert(1)' })).toMatchObject({ code: null, link: null, friends: [] })
  })

  it('maps the bonus history with signed amounts and the cursor', () => {
    const page = parseBonusPage({
      items: [
        { id: 12, type: 'grant', amount: 300, order_number: null, created_at: '2026-11-01T10:00:00Z' },
        { id: 11, type: 'spend', amount: -200, order_number: 1024, created_at: '2026-10-30T10:00:00Z' },
        { id: 10, type: 'reserve', amount: 0, order_number: 1024, created_at: '2026-10-30T09:00:00Z' },
      ],
      next_cursor: '10',
    })
    expect(page.items).toEqual([
      { id: 12, type: 'grant', amount: 300, order_number: null, created_at: '2026-11-01T10:00:00Z' },
      { id: 11, type: 'spend', amount: -200, order_number: 1024, created_at: '2026-10-30T10:00:00Z' },
    ])
    expect(page.next_cursor).toBe('10')
  })
})
