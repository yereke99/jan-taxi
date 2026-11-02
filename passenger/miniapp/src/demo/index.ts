// Passenger presentation build (VITE_DEMO=1): fixtures and screen switches.
// Imported only by main.ts behind `import.meta.env.VITE_DEMO === '1'`.
//
// Every screen of the presentation is reachable by URL (base /passenger/):
//   ?demo=home        map, live location, pickup A filled from GPS
//   ?demo=route       route editor with stops A–D
//   ?demo=confirm     order confirmation, 700 ₸, comment filled in
//   ?demo=searching   live order, searching (turns into "driver found" after
//                     &advance=<seconds>, default 25; advance=0 keeps searching)
//   ?demo=found       live order, driver on the way
//   &phone=unverified the passenger has not shared a phone (the first order
//                     asks for it)
//   ?demo=coupons     «Менің купоным»: the first-ride coupon, 400 ₸, counting
//                     down (~2 days)
//   ?demo=profile     the profile with the promotion rows (inviting locked
//                     until the coupon is used)
//   ?demo=bonus       «Бонус балансы» a week on: 300 ₸ from a friend's first ride
//   ?demo=invite      «Досыңды шақыр» a week on: open, with a code and two friends
// The passenger is new (the first ride, yesterday, earned the coupon);
// ?demo=bonus and ?demo=invite show them a week on, after the coupon was
// used, as the customer's rules require (see Promotions below).
// Without ?demo the app starts on home and the whole flow is clickable: the
// in-memory API quotes the route, creates the order (the coupon applies to a
// taxi by itself; the bonus pays when there is no coupon) and assigns the
// driver; cancelling returns the coupon or bonus.

import type { Pinia } from 'pinia'
import type { Router } from 'vue-router'
import { useBookingStore } from '../stores/booking'
import {
  APPROACH,
  apiError,
  bboxOf,
  broadcast,
  DemoApi,
  DemoResponse,
  installDemoRuntime,
  isoAgo,
  isoIn,
  params,
  PLACES,
  pointAlong,
  reverse,
  ROUTE,
  search,
  STOPS,
  type DemoRuntime,
} from './core'

const ORDER_ID = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
const FARE = { recommended: 700, min: 500, max: 1500, step: 50 }
const COMMENT = '2-кіреберіс, сыртта күтемін'
const SEARCH_ELAPSED_S = 42

type Status = 'searching' | 'accepted' | 'cancelled'

/** A promotion on the demo order (§23.6). */
interface DemoPromo {
  kind: 'coupon' | 'bonus'
  discount: number
  status: 'reserved' | 'released'
}

interface DemoClass {
  code: string
  name: { kk: string; ru: string; en: string }
  examples: string
  icon: string
  seats_max: number
  cargo_volume_l: number
  passengers: boolean
  /** Fare range relative to the sedan's. */
  factor: number
}

/** The seeded classes of ARCHITECTURE §21.1. */
const CLASSES: DemoClass[] = [
  { code: 'sedan', name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, examples: 'Toyota Camry', icon: 'sedan', seats_max: 4, cargo_volume_l: 400, passengers: true, factor: 1 },
  { code: 'universal', name: { kk: 'Универсал', ru: 'Универсал', en: 'Estate' }, examples: 'Lada Largus', icon: 'universal', seats_max: 4, cargo_volume_l: 600, passengers: true, factor: 1.2 },
  { code: 'minivan', name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' }, examples: 'Toyota Alphard, Sienna', icon: 'minivan', seats_max: 8, cargo_volume_l: 800, passengers: true, factor: 2 },
  { code: 'microbus', name: { kk: 'Микроавтобус', ru: 'Микроавтобус', en: 'Minibus' }, examples: 'Mercedes Sprinter', icon: 'microbus', seats_max: 20, cargo_volume_l: 1500, passengers: true, factor: 4 },
  { code: 'cargo_van', name: { kk: 'Жүк фургоны', ru: 'Грузовой фургон', en: 'Cargo van' }, examples: 'Ford Transit', icon: 'cargo_van', seats_max: 2, cargo_volume_l: 6000, passengers: false, factor: 4 },
  { code: 'truck', name: { kk: 'Жүк көлігі', ru: 'Грузовик', en: 'Truck' }, examples: 'ГАЗель Next', icon: 'truck', seats_max: 2, cargo_volume_l: 15000, passengers: false, factor: 10 },
]
const CARGO_SIZES = [
  { code: 's', volume_l: 10 },
  { code: 'm', volume_l: 60 },
  { code: 'l', volume_l: 400 },
  { code: 'xl', volume_l: 1500 },
]

interface DemoLoad {
  category: string
  vehicleClass: string
  passengerCount: number | null
  cargoSize: string | null
  cargoVolumeL: number
}

interface DemoState {
  status: Status | null
  createdAt: string
  fare: number
  comment: string | null
  load: DemoLoad
  cancelReasonCode: string | null
  cancelReason: string | null
  acceptedAt: string | null
  /** 0..1 progress of the driver along the approach route. */
  approach: number
  promo: DemoPromo | null
  version: number
}

const state: DemoState = {
  status: null,
  createdAt: isoAgo(SEARCH_ELAPSED_S),
  fare: FARE.recommended,
  comment: COMMENT,
  load: { category: 'taxi', vehicleClass: 'sedan', passengerCount: 1, cargoSize: null, cargoVolumeL: 0 },
  cancelReasonCode: null,
  cancelReason: null,
  acceptedAt: null,
  approach: 0,
  promo: null,
  version: 1,
}

/** 422 validation_failed with per-field reasons, as the API sends it. */
function invalid(fields: Record<string, string>): DemoResponse {
  return new DemoResponse(422, { error: { code: 'validation_failed', message: 'validation failed', fields } })
}

/** Quote id → the load it was quoted for (POST /orders takes the chosen option's id). */
const quoted = new Map<string, DemoLoad>()

const city = { id: 'c0a80001-0000-4000-8000-000000000001', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Emba' } }
/** Where the demo orders happen: the settlement of pickup A and the tariff that priced them. */
const place = { locality: 'Ембі', tariff_region: { code: city.code, name: city.name } }

function fareOptions(factor = 1) {
  const step = FARE.step * Math.max(1, Math.round(factor))
  const min = Math.round((FARE.min * factor) / step) * step
  const max = Math.round((FARE.max * factor) / step) * step
  const recommended = Math.round((FARE.recommended * factor) / step) * step
  const options: number[] = []
  for (let v = recommended; v <= max; v += step) options.push(v)
  return { recommended, min, max, step, options }
}

function stopKind(i: number, n: number): 'pickup' | 'intermediate' | 'destination' {
  return i === 0 ? 'pickup' : i === n - 1 ? 'destination' : 'intermediate'
}

interface PreviewBody {
  category?: string
  stops?: Array<{ lat: number; lon: number; address: string; address_details?: string }>
  passenger_count?: number
  cargo_size?: string
}

let quoteSeq = 0

/** One option per class able to carry the load (§21.3), like the API. */
function quote(body: PreviewBody) {
  const stops = body.stops ?? []
  const category = body.category === 'parcel' || body.category === 'other' ? body.category : 'taxi'
  const taxi = category === 'taxi'
  const passengers = taxi ? Math.max(1, Math.min(15, Math.round(body.passenger_count ?? 1))) : null
  const size = CARGO_SIZES.find((c) => c.code === body.cargo_size) ?? CARGO_SIZES[0]!
  const volume = taxi ? 0 : size.volume_l
  const capable = CLASSES.filter((c) => (taxi ? c.passengers && c.seats_max >= (passengers ?? 1) : c.cargo_volume_l >= volume))
  if (capable.length === 0) return invalid(taxi ? { passenger_count: 'too_large' } : { cargo_size: 'too_large' })
  const options = capable.map((c, i) => {
    const id = `b5f1c2d4-9e8a-4f3b-8c7d-${String(++quoteSeq).padStart(12, '0')}`
    quoted.set(id, { category, vehicleClass: c.code, passengerCount: passengers, cargoSize: taxi ? null : size.code, cargoVolumeL: volume })
    return {
      vehicle_class: c.code,
      name: c.name,
      examples: c.examples,
      icon: c.icon,
      seats_max: c.seats_max,
      cargo_volume_l: c.cargo_volume_l,
      quote_id: id,
      expires_at: isoIn(600),
      fare: fareOptions(c.factor),
      commission_bps: 1000,
      eta_s: i < 3 ? 120 + i * 180 : null,
    }
  })
  const first = options[0]!
  return {
    quote_id: first.quote_id,
    expires_at: first.expires_at,
    category,
    vehicle_class: first.vehicle_class,
    passenger_count: passengers,
    cargo_size: taxi ? null : size.code,
    options,
    city,
    stops: stops.map((s, i) => ({ seq: i, kind: stopKind(i, stops.length), lat: s.lat, lon: s.lon, address: s.address, address_details: s.address_details ?? null })),
    distance_m: ROUTE.distance_m,
    duration_s: ROUTE.duration_s,
    legs: ROUTE.legs.slice(0, Math.max(1, stops.length - 1)),
    geometry: ROUTE.geometry,
    bbox: bboxOf(ROUTE.geometry.coordinates),
    fare: first.fare,
    commission_bps: 1000,
    payment_methods: ['cash', 'bank_transfer'],
  }
}

function loadDto(l: DemoLoad) {
  const c = CLASSES.find((x) => x.code === l.vehicleClass) ?? CLASSES[0]!
  return {
    vehicle_class: c.code,
    vehicle_class_name: c.name,
    passenger_count: l.passengerCount,
    cargo_size: l.cargoSize,
    cargo_volume_l: l.cargoVolumeL,
  }
}

// --- Promotions (§23) -------------------------------------------------------------

/**
 * Follows the customer's rules (§23.4, §23.5, unlock coupon_redeemed): the
 * passenger is new, and the first ride (yesterday) earned the 400 ₸
 * first-ride coupon for 72 hours, so inviting stays locked until it is used.
 * ?demo=invite and ?demo=bonus show the same passenger a week on: the coupon
 * was used on the next ride, which opened inviting, and the first ride of
 * Арман, invited with the code, paid the 300 ₸ reward (once).
 */
const WEEK_ON = params.get('demo') === 'invite' || params.get('demo') === 'bonus'
/** Days ago of the passenger's first completed ride, which earned the coupon. */
const FIRST_RIDE_DAYS = WEEK_ON ? 7 : 1
/** A week on: days ago of the ride the coupon paid part of. */
const COUPON_RIDE_DAYS = 6

/** Seconds ago a ride of `daysAgo` completed (15 minutes after it was ordered). */
function completedAgo(daysAgo: number): number {
  return daysAgo * 86_400 - 900
}

/** Issued when the first ride completed, valid 72 hours. */
const couponIssuedAt = Date.now() - completedAgo(FIRST_RIDE_DAYS) * 1000

const promos = {
  coupon: {
    id: 'e4b1c8a2-6d3f-4a9e-8b7c-1f2e3d4c5b6a',
    title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' },
    amount: 400,
    min_order_fare: 400,
    categories: ['taxi'],
    status: (WEEK_ON ? 'redeemed' : 'issued') as 'issued' | 'reserved' | 'redeemed',
    issued_at: new Date(couponIssuedAt).toISOString(),
    expires_at: new Date(couponIssuedAt + 72 * 3_600_000).toISOString(),
    reserved_order_id: null as string | null,
    redeemed_at: WEEK_ON ? isoAgo(completedAgo(COUPON_RIDE_DAYS)) : null,
  },
  bonus: { balance: WEEK_ON ? 300 : 0, reserved: 0 },
  subscribed: true,
}

const REFERRAL_CODE = 'JT7K2M9Q'

/** The invitation as GET /promotions sums it up: open once the first-ride coupon was used. */
function referralSummary() {
  return promos.coupon.status === 'redeemed'
    ? { state: 'unlocked', reward_amount: 300, rewarded: true }
    : { state: 'locked', lock_reason: 'coupon_not_redeemed', reward_amount: 300, rewarded: false }
}

function promotionsDto() {
  return {
    server_time: new Date().toISOString(),
    redemptions_enabled: true,
    coupons: [{ ...promos.coupon }],
    bonus: { ...promos.bonus, available: promos.bonus.balance - promos.bonus.reserved },
    bonus_categories: ['taxi'],
    offers: [],
    banners: [
      {
        id: 'b7a6c5d4-3e2f-4a1b-9c8d-7e6f5a4b3c2d',
        title: { kk: 'Сәлемдеме жеткізу', ru: 'Доставка посылок', en: 'Parcel delivery' },
        description: {
          kk: 'Құжат пен шағын жүкті қала ішінде жеткіземіз. Тапсырысты басты беттен беріңіз.',
          ru: 'Доставим документы и небольшие посылки по городу. Закажите на главном экране.',
          en: 'We deliver documents and small parcels across town. Order from the home screen.',
        },
      },
    ],
    referral: referralSummary(),
    marketing_subscribed: promos.subscribed,
  }
}

/** Locked: the campaign text alone. Open: the code, the links and the friends (Арман joined after it opened). */
function referralDto() {
  const summary = referralSummary()
  const campaign = {
    title: { kk: 'Досыңды шақыр', ru: 'Пригласи друга', en: 'Invite a friend' },
    description: {
      kk: 'Досыңды Jan Taxi-ге шақыр! Ол алғашқы сапарын аяқтаған соң, бонус балансыңа 300 ₸ түседі.',
      ru: 'Пригласите друга в Jan Taxi! Когда он завершит первую поездку, на ваш бонусный баланс поступит 300 ₸.',
      en: 'Invite a friend to Jan Taxi! When they finish their first ride, 300 ₸ goes to your bonus balance.',
    },
  }
  if (summary.state !== 'unlocked') return { ...summary, ...campaign, earned: 0, friends: [] }
  return {
    ...summary,
    ...campaign,
    code: REFERRAL_CODE,
    link: `https://t.me/jantaxi_bot?start=ref_${REFERRAL_CODE}`,
    app_link: `https://t.me/jantaxi_bot?startapp=ref_${REFERRAL_CODE}`,
    earned: 300,
    friends: [
      { name: 'Дана Қ.', status: 'attributed', attributed_at: isoAgo(5 * 3600) },
      { name: 'Арман Б.', status: 'rewarded', attributed_at: isoAgo(5 * 86_400) },
    ],
  }
}

/** The bonus history: a week on, the reward for Арман's first ride (4 days ago); a new passenger has none. */
function bonusHistory() {
  return WEEK_ON ? [{ id: 1, type: 'grant', amount: 300, order_number: null, created_at: isoAgo(4 * 86_400) }] : []
}

function promotionChanged(reason: string, discount = 0): DemoResponse {
  return new DemoResponse(409, { error: { code: 'promotion_changed', message: 'the promotion changed; review the price', details: { reason, discount } } })
}

/**
 * Checks and reserves the promotion of a new order like the server (§23.6):
 * the coupon or the bonus, at exactly the discount the app showed.
 */
function reservePromotion(kind: string | undefined, shown: number | undefined, category: string, fare: number): DemoPromo | null | DemoResponse {
  if (kind !== 'coupon' && kind !== 'bonus') return null
  const c = promos.coupon
  const available = promos.bonus.balance - promos.bonus.reserved
  if (kind === 'coupon' && (c.status !== 'issued' || Date.parse(c.expires_at) <= Date.now() || category !== 'taxi' || fare < c.min_order_fare)) {
    return promotionChanged('no_coupon')
  }
  if (kind === 'bonus' && (category !== 'taxi' || available <= 0)) return promotionChanged('no_bonus')
  const discount = kind === 'coupon' ? c.amount : Math.min(available, fare)
  if (shown !== undefined && shown !== discount) return promotionChanged('discount_changed', discount)
  if (kind === 'coupon') {
    c.status = 'reserved'
    c.reserved_order_id = ORDER_ID
  } else promos.bonus.reserved += discount
  broadcast('promotions.updated', {})
  return { kind, discount, status: 'reserved' }
}

/** A cancelled order gives its coupon or bonus back. */
function releasePromotion(): void {
  const p = state.promo
  if (!p || p.status !== 'reserved') return
  if (p.kind === 'coupon') {
    promos.coupon.status = 'issued'
    promos.coupon.reserved_order_id = null
  } else promos.bonus.reserved = Math.max(0, promos.bonus.reserved - p.discount)
  p.status = 'released'
  broadcast('promotions.updated', {})
}

function orderPromotion() {
  const p = state.promo
  if (!p) return { promo_discount: 0, cash_due: state.fare, promotion: null }
  const cash = state.fare - p.discount
  return {
    promo_discount: p.discount,
    cash_due: cash,
    promotion: {
      kind: p.kind,
      title: p.kind === 'coupon' ? promos.coupon.title : null,
      discount: p.discount,
      gross_fare: state.fare,
      cash_due: cash,
      status: p.status,
      compensation: null,
      settled_at: null,
    },
  }
}

const driver = {
  id: 'a1b2c3d4-0000-4000-8000-00000000e001',
  code: 'EMB001',
  first_name: 'Ерлан',
  last_name: 'С.',
  phone: '+77011234567',
  rating: 4.9,
  rating_count: 287,
  completed_trips: 312,
  photo_url: null,
}

const vehicle = {
  make: 'Toyota',
  model: 'Camry',
  color: 'ақ',
  plate: '123 ABC 04',
  vehicle_class: 'sedan',
  vehicle_class_name: CLASSES[0]!.name,
  seats: 4,
  cargo_volume_l: 400,
}

function orderDto() {
  const s = state.status ?? 'searching'
  const n = STOPS.length
  const assigned = s === 'accepted'
  const loc = assigned ? pointAlong(APPROACH.geometry, state.approach) : null
  return {
    id: ORDER_ID,
    number: 1024,
    status: s,
    category: state.load.category,
    ...loadDto(state.load),
    ...place,
    cargo_description: null,
    recipient_phone: null,
    comment: state.comment,
    fare: state.fare,
    ...orderPromotion(),
    payment_method: 'cash',
    distance_m: ROUTE.distance_m,
    duration_s: ROUTE.duration_s,
    city,
    stops: STOPS.map((p, i) => ({
      id: `5e0d0000-0000-4000-8000-00000000000${i + 1}`,
      seq: i,
      kind: stopKind(i, n),
      lat: p.lat,
      lon: p.lon,
      address: p.title,
      address_details: null,
      completed_at: null,
    })),
    route: { geometry: ROUTE.geometry, legs: ROUTE.legs, bbox: bboxOf(ROUTE.geometry.coordinates), distance_m: ROUTE.distance_m, duration_s: ROUTE.duration_s },
    driver: assigned ? driver : null,
    vehicle: assigned ? vehicle : null,
    driver_location: loc ? { ...loc, accuracy_m: 8, heading: 120, recorded_at: new Date().toISOString() } : null,
    events: [{ from_status: null, to_status: 'searching', actor_type: 'passenger', reason: null, created_at: state.createdAt }],
    rating: null,
    dispute: null,
    cancel_reason: s === 'cancelled' ? state.cancelReason : null,
    cancel_reason_code: s === 'cancelled' ? state.cancelReasonCode : null,
    cancelled_by: s === 'cancelled' ? 'passenger' : null,
    // The driver drives the approach route in about six minutes (§22.6).
    driver_eta_s: assigned ? Math.max(60, Math.round((1 - state.approach) * 360)) : null,
    driver_eta_at: assigned ? new Date().toISOString() : null,
    can_cancel: s !== 'cancelled',
    can_rate: false,
    can_dispute: false,
    version: state.version,
    created_at: state.createdAt,
    updated_at: new Date().toISOString(),
    search_expires_at: s === 'searching' ? isoIn(300 - SEARCH_ELAPSED_S) : null,
    accepted_at: state.acceptedAt,
    arrived_at: null,
    started_at: null,
    completed_at: null,
    cancelled_at: s === 'cancelled' ? new Date().toISOString() : null,
  }
}

function summary(o: ReturnType<typeof orderDto>) {
  const first = o.stops[0] ?? null
  const last = o.stops[o.stops.length - 1] ?? null
  if (!first || !last) throw new Error('order without stops')
  return {
    id: o.id,
    number: o.number,
    status: o.status,
    category: o.category,
    ...loadDto(state.load),
    ...place,
    fare: o.fare,
    promo_discount: o.promo_discount,
    cash_due: o.cash_due,
    payment_method: o.payment_method,
    pickup: { seq: first.seq, kind: first.kind, lat: first.lat, lon: first.lon, address: first.address },
    destination: { seq: last.seq, kind: last.kind, lat: last.lat, lon: last.lon, address: last.address },
    stop_count: o.stops.length,
    distance_m: o.distance_m,
    driver_first_name: o.driver?.first_name ?? null,
    rating: null,
    can_rate: false,
    created_at: o.created_at,
    completed_at: null,
    cancelled_at: o.cancelled_at,
  }
}

/**
 * Two finished orders; their destinations are the "recent addresses" on
 * home. A new passenger: the first ride (yesterday), which earned the coupon,
 * and an order cancelled before it (a cancelled order is not a ride). A week
 * on: the first ride and the next one, which the coupon paid 400 ₸ of.
 */
function history() {
  const row = (id: string, number: number, dest: { lat: number; lon: number; title: string }, daysAgo: number, coupon = 0) => ({
    id,
    number,
    status: 'completed',
    category: 'taxi',
    ...loadDto({ category: 'taxi', vehicleClass: 'sedan', passengerCount: 1, cargoSize: null, cargoVolumeL: 0 }),
    ...place,
    fare: 600,
    promo_discount: coupon,
    cash_due: 600 - coupon,
    payment_method: 'cash',
    pickup: { seq: 0, kind: 'pickup', lat: STOPS[0].lat, lon: STOPS[0].lon, address: STOPS[0].title },
    destination: { seq: 1, kind: 'destination', lat: dest.lat, lon: dest.lon, address: dest.title },
    stop_count: 2,
    distance_m: 2400,
    driver_first_name: 'Ерлан' as string | null,
    rating: 5 as number | null,
    can_rate: false,
    created_at: isoAgo(daysAgo * 86_400),
    completed_at: isoAgo(completedAgo(daysAgo)) as string | null,
    cancelled_at: null as string | null,
  })
  const [a1, a2] = ['0f1e2d3c-0000-4000-8000-0000000000a1', '0f1e2d3c-0000-4000-8000-0000000000a2']
  if (WEEK_ON) return [row(a1, 1015, STOPS[3], COUPON_RIDE_DAYS, promos.coupon.amount), row(a2, 1009, PLACES[0], FIRST_RIDE_DAYS)]
  // Cancelled by the passenger while a driver was being searched for.
  const cancelled = { ...row(a2, 1012, PLACES[0], 3), status: 'cancelled', driver_first_name: null, rating: null, completed_at: null, cancelled_at: isoAgo(3 * 86_400 - 120) }
  return [row(a1, 1019, STOPS[3], FIRST_RIDE_DAYS), cancelled]
}

function notify(type: string): void {
  state.version += 1
  broadcast(type, { order_id: ORDER_ID })
}

let driverTimer: ReturnType<typeof setInterval> | undefined

function assignDriver(): void {
  if (state.status !== 'searching') return
  state.status = 'accepted'
  state.acceptedAt = new Date().toISOString()
  state.approach = 0
  notify('order.accepted')
  // The car drives along its approach route; the passenger sees it move.
  clearInterval(driverTimer)
  driverTimer = setInterval(() => {
    if (state.status !== 'accepted') return clearInterval(driverTimer)
    state.approach = Math.min(0.9, state.approach + 0.01)
    const p = pointAlong(APPROACH.geometry, state.approach)
    broadcast('driver.location_updated', {
      order_id: ORDER_ID,
      driver_id: driver.id,
      lat: p.lat,
      lon: p.lon,
      heading: 120,
      recorded_at: new Date().toISOString(),
      driver_eta_s: Math.max(60, Math.round((1 - state.approach) * 360)),
    })
  }, 2000)
}

let searchTimer: ReturnType<typeof setTimeout> | undefined

function startSearching(): void {
  state.status = 'searching'
  state.version += 1
  const advance = Number(params.get('advance') ?? '25')
  clearTimeout(searchTimer)
  if (Number.isFinite(advance) && advance > 0) searchTimer = setTimeout(assignDriver, advance * 1000)
}

function api(): DemoApi {
  const me = {
    id: 'ab12cd34-0000-4000-8000-0000000000p1',
    telegram_user_id: 700000001,
    first_name: 'Айгерім',
    last_name: '',
    username: null,
    language: 'kk',
    phone_masked: params.get('phone') === 'unverified' ? null : '+7 701 ••• •• 45',
    phone_verified: params.get('phone') !== 'unverified',
  }
  return new DemoApi()
    .on('POST', '/auth/telegram/:app', () => ({ token: 'demo-token', expires_at: isoIn(86_400) }))
    .on('GET', '/public/config', () => ({
      app: 'passenger',
      default_language: 'kk',
      languages: ['kk', 'ru', 'en'],
      city: { code: 'EMB', name: city.name, center: { lat: 48.8275, lon: 58.1442 }, zoom: 14 },
      map: { style_url: 'https://tiles.openfreemap.org/styles/positron' },
      routing: { max_points: 5 },
      bots: { passenger_username: 'jantaxi_bot', driver_username: 'jantaxi_driver_bot' },
      support: { telegram_username: 'jantaxi_support', phone: '+77001234567', hours: { kk: 'Күн сайын 08:00–22:00', ru: 'Ежедневно 08:00–22:00', en: 'Daily 08:00–22:00' } },
      delivery: { prohibited_items: ['weapons', 'drugs', 'alcohol'] },
      fare: { min: FARE.min, max: FARE.max, step: FARE.step },
      vehicle_classes: CLASSES.map((c) => ({
        code: c.code,
        name: c.name,
        examples: c.examples,
        icon: c.icon,
        seats_default: Math.min(c.seats_max, c.code === 'minivan' ? 7 : c.code === 'microbus' ? 15 : c.seats_max),
        seats_max: c.seats_max,
        cargo_volume_l_default: c.cargo_volume_l,
        passenger_capable: c.passengers,
        cargo_capable: true,
      })),
      cargo_sizes: CARGO_SIZES,
      max_passenger_count: 15,
      cancel_reasons: [
        { code: 'long_wait', label: { kk: 'Ұзақ күттім', ru: 'Долго ждать', en: 'Waited too long' }, comment_required: false },
        { code: 'driver_issue', label: { kk: 'Жүргізуші ұнамады', ru: 'Не понравился водитель', en: "Didn't like the driver" }, comment_required: false },
        { code: 'vehicle_issue', label: { kk: 'Көлік ұнамады', ru: 'Не понравилась машина', en: "Didn't like the car" }, comment_required: false },
        { code: 'changed_plans', label: { kk: 'Жоспарым өзгерді', ru: 'Планы изменились', en: 'My plans changed' }, comment_required: false },
        { code: 'other', label: { kk: 'Басқа себеп', ru: 'Другое', en: 'Other' }, comment_required: true },
      ],
    }))
    .on('GET', '/me', () => me)
    .on('PATCH', '/me/preferences', (r) => ({ ...me, language: (r.body as { language?: string })?.language ?? 'kk' }))
    .on('GET', '/geo/reverse', (r) => {
      const p = { lat: Number(r.query.get('lat')), lon: Number(r.query.get('lon')) }
      return { ...p, ...reverse(p), source: 'demo', in_service_area: true }
    })
    .on('GET', '/geo/search', (r) => search(r.query.get('q') ?? '').map((p) => ({ ...p, source: 'demo' })))
    .on('POST', '/routes/preview', (r) => quote(r.body as PreviewBody))
    .on('POST', '/orders', (r) => {
      const body = r.body as { quote_id?: string; fare?: number; comment?: string; promotion?: string; promotion_discount?: number }
      const load = quoted.get(body.quote_id ?? '')
      if (!load) return apiError(404, 'not_found')
      const fare = body.fare ?? FARE.recommended
      const promo = reservePromotion(body.promotion, body.promotion_discount, load.category, fare)
      if (promo instanceof DemoResponse) return promo
      state.promo = promo
      state.load = load
      state.cancelReasonCode = null
      state.cancelReason = null
      state.fare = fare
      state.comment = body.comment?.trim() || null
      state.createdAt = new Date().toISOString()
      startSearching()
      return orderDto()
    })
    .on('GET', '/orders', (r) => {
      if (r.query.get('scope') === 'active') return { items: state.status && state.status !== 'cancelled' ? [summary(orderDto())] : [], next_cursor: null }
      return { items: history(), next_cursor: null }
    })
    .on('GET', '/orders/:id', (r) => (r.params.id === ORDER_ID && state.status ? orderDto() : apiError(404, 'not_found')))
    .on('POST', '/orders/:id/cancel', (r) => {
      const body = r.body as { reason_code?: string; comment?: string }
      if (!body?.reason_code) return invalid({ reason_code: 'required' })
      if (body.reason_code === 'other' && !body.comment?.trim()) return invalid({ comment: 'required' })
      state.cancelReasonCode = body.reason_code
      state.cancelReason = body.comment?.trim() || null
      state.status = 'cancelled'
      clearTimeout(searchTimer)
      clearInterval(driverTimer)
      releasePromotion()
      notify('order.cancelled')
      return orderDto()
    })
    .on('GET', '/promotions', () => promotionsDto())
    .on('GET', '/promotions/referral', () => referralDto())
    .on('GET', '/promotions/bonus/history', () => ({ items: bonusHistory(), next_cursor: null }))
    .on('PUT', '/promotions/subscription', (r) => {
      promos.subscribed = (r.body as { subscribed?: boolean } | undefined)?.subscribed !== false
      return { marketing_subscribed: promos.subscribed }
    })
}

/** Installs the demo runtime; call before bootTelegram(). */
export function installDemo(): DemoRuntime {
  const screen = params.get('demo')
  if (screen === 'searching') startSearching()
  if (screen === 'found') {
    state.status = 'searching'
    assignDriver()
    state.approach = 0.2
  }
  return installDemoRuntime({
    api: api(),
    user: { id: 700000001, first_name: 'Айгерім', last_name: '', username: 'aigerim_demo', language_code: 'kk' },
  })
}

/** Applies the requested screen once the app's stores and router exist. */
export async function openDemoScreen(pinia: Pinia, router: Router): Promise<void> {
  // Lets the screenshot script's map walk-through navigate like a user.
  ;(window as unknown as { __jtRouter?: Router }).__jtRouter = router
  const screen = params.get('demo')
  const booking = useBookingStore(pinia)
  if (screen === 'route' || screen === 'confirm') {
    booking.prefill(
      'taxi',
      STOPS.map((p) => ({ lat: p.lat, lon: p.lon, address: p.title, subtitle: p.subtitle, source: 'search' as const, inServiceArea: true })),
    )
    if (screen === 'confirm') booking.comment = COMMENT
    await router.replace({ name: screen === 'route' ? 'book' : 'confirm', query: { category: 'taxi' } })
  } else if (screen === 'searching' || screen === 'found') {
    await router.replace({ name: 'order', params: { id: ORDER_ID } })
  } else if (screen === 'coupons' || screen === 'bonus' || screen === 'invite' || screen === 'profile') {
    await router.replace({ name: screen })
  }
}
