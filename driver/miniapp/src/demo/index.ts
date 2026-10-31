// Driver presentation build (VITE_DEMO=1): fixtures and screen switches.
// Imported only by main.ts behind `import.meta.env.VITE_DEMO === '1'`.
//
// Screens reachable by URL (base /driver/):
//   ?demo=orders      available orders (online, balance 4 350 ₸)
//   ?demo=order       order detail: map with the route A–D and the car's approach
//   ?demo=topup       Kaspi top-up of 5 000 ₸ waiting for payment
//   ?demo=home        driver home
//   ?demo=register    registration, the vehicle page (body type preset to Sedan)
//   ?demo=wallet      wallet with the welcome bonus in the journal
// Without ?demo the app opens on home; accepting an order and the trip
// actions work against the in-memory API.

import type { Pinia } from 'pinia'
import type { Router } from 'vue-router'
import {
  APPROACH,
  apiError,
  bboxOf,
  DemoApi,
  installDemoRuntime,
  isoAgo,
  isoIn,
  params,
  PLACES,
  ROUTE,
  STOPS,
  type DemoPlace,
  type DemoRuntime,
} from './core'

const city = { id: 'c0a80001-0000-4000-8000-000000000001', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Emba' } }
const ORDER_ID = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
const TOPUP_ID = '3e9b7a10-5c2d-4f8e-9a1b-000000000123'
/** Real payee of driver top-ups (Kaspi Pay link of the operator). */
const PAYEE = 'ИП "JAN-TAXI", БИН 000809500289'
const KASPI_URL = 'https://pay.kaspi.kz/pay/xbp1imhd'

/** The car's simulated position: start of its approach route, ~1,2 km from A. */
const [carLon, carLat] = APPROACH.geometry.coordinates[0] ?? [STOPS[0].lon, STOPS[0].lat]
const CAR = { lat: carLat, lon: carLon }

let accepted = false
let online = true
/** ?demo=register: a new driver filling in the application (no profile yet). */
const registering = params.get('demo') === 'register'
let draft: Record<string, unknown> | null = null

/** The application as GET/PUT /driver/registration return it, built from the last PUT. */
function registration() {
  const body = (draft ?? {}) as { first_name?: string; last_name?: string; patronymic?: string; iin?: string; vehicle?: Record<string, unknown> }
  const v = body.vehicle ?? {}
  // The API gives the profile it creates the default body type (Sedan).
  const code = typeof v.vehicle_class === 'string' ? v.vehicle_class : draft ? 'sedan' : null
  const cls = VEHICLE_CLASSES.find((c) => c.code === code) ?? null
  const missing = [
    'avatar',
    ...(body.iin ? [] : ['identity']),
    ...(cls ? [] : ['vehicle_class']),
    ...(v.make && v.model ? [] : ['vehicle_make_model']),
    ...(v.color ? [] : ['vehicle_color']),
    ...(v.plate ? [] : ['vehicle_plate']),
  ]
  return {
    id: draft ? 'a1b2c3d4-0000-4000-8000-00000000e002' : null,
    status: 'draft',
    code: null,
    city,
    first_name: body.first_name ?? '',
    last_name: body.last_name ?? '',
    patronymic: body.patronymic ?? '',
    iin: body.iin ?? null,
    phone: '+77011234567',
    phone_verified: true,
    avatar: null,
    documents: { id_front: null, id_back: null },
    vehicle: {
      make: v.make ?? '',
      model: v.model ?? '',
      color: v.color ?? '',
      plate: v.plate ?? '',
      plate_normalized: null,
      vehicle_class: cls?.code ?? null,
      vehicle_class_name: cls?.name ?? null,
      seats: cls?.seats_default ?? null,
      cargo_volume_l: cls?.cargo_volume_l_default ?? null,
      photos: [],
    },
    corrections: [],
    correction_comment: null,
    rejection_reason: null,
    missing_fields: missing,
    editable: true,
    can_submit: false,
    submitted_at: null,
    history: [],
    pd_consent_at: null,
    pd_consent_version: null,
  }
}

function kind(i: number, n: number): 'pickup' | 'intermediate' | 'destination' {
  return i === 0 ? 'pickup' : i === n - 1 ? 'destination' : 'intermediate'
}

function stopsOf(places: readonly DemoPlace[]) {
  return places.map((p, i) => ({ seq: i, kind: kind(i, places.length), lat: p.lat, lon: p.lon, address: p.title }))
}

/** Active vehicle classes as GET /public/config returns them (§21.1 seed). */
const VEHICLE_CLASSES = [
  { code: 'sedan', name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, examples: 'Toyota Camry, Hyundai Elantra', icon: 'sedan', seats_default: 4, seats_max: 4, cargo_volume_l_default: 400, passenger_capable: true, cargo_capable: true },
  { code: 'universal', name: { kk: 'Универсал', ru: 'Универсал', en: 'Estate' }, examples: 'Lada Largus, Skoda Octavia Combi', icon: 'universal', seats_default: 4, seats_max: 4, cargo_volume_l_default: 600, passenger_capable: true, cargo_capable: true },
  { code: 'minivan', name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' }, examples: 'Toyota Alphard, Sienna', icon: 'minivan', seats_default: 7, seats_max: 8, cargo_volume_l_default: 800, passenger_capable: true, cargo_capable: true },
  { code: 'microbus', name: { kk: 'Микроавтобус', ru: 'Микроавтобус', en: 'Minibus' }, examples: 'Hyundai H-1, Mercedes Sprinter', icon: 'microbus', seats_default: 15, seats_max: 20, cargo_volume_l_default: 1500, passenger_capable: true, cargo_capable: true },
  { code: 'cargo_van', name: { kk: 'Жүк фургоны', ru: 'Грузовой фургон', en: 'Cargo van' }, examples: 'Ford Transit, ГАЗель', icon: 'cargo_van', seats_default: 2, seats_max: 2, cargo_volume_l_default: 6000, passenger_capable: false, cargo_capable: true },
  { code: 'truck', name: { kk: 'Жүк көлігі', ru: 'Грузовик', en: 'Truck' }, examples: 'Isuzu, Hyundai HD', icon: 'truck', seats_default: 2, seats_max: 2, cargo_volume_l_default: 15000, passenger_capable: false, cargo_capable: true },
] as const

const COMMISSION = 50

function className(code: string) {
  return VEHICLE_CLASSES.find((c) => c.code === code)?.name ?? null
}

interface CardSpec {
  id: string
  number: number
  category: 'taxi' | 'parcel'
  vehicle_class: string
  passengers: number | null
  cargo_size: 's' | 'm' | 'l' | 'xl' | null
  places: readonly DemoPlace[]
  fare: number
  distance_m: number
  duration_s: number
  to_pickup_m: number
  age_s: number
  comment: string | null
  cargo: string | null
}

const CARDS: readonly [CardSpec, CardSpec, CardSpec] = [
  { id: ORDER_ID, number: 1024, category: 'taxi', vehicle_class: 'sedan', passengers: 3, cargo_size: null, places: STOPS, fare: 700, distance_m: ROUTE.distance_m, duration_s: ROUTE.duration_s, to_pickup_m: 1200, age_s: 120, comment: '2-кіреберіс, сыртта күтемін', cargo: null },
  { id: '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d35', number: 1025, category: 'parcel', vehicle_class: 'sedan', passengers: null, cargo_size: 'm', places: [STOPS[1], PLACES[1]], fare: 900, distance_m: 4100, duration_s: 540, to_pickup_m: 2500, age_s: 300, comment: null, cargo: 'Конверттегі құжаттар' },
  { id: '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d36', number: 1026, category: 'taxi', vehicle_class: 'sedan', passengers: 1, cargo_size: null, places: [PLACES[0], PLACES[2]], fare: 550, distance_m: 2300, duration_s: 360, to_pickup_m: 800, age_s: 480, comment: null, cargo: null },
]

function card(c: CardSpec) {
  return {
    id: c.id,
    number: c.number,
    category: c.category,
    service_type: c.category === 'parcel' ? 'delivery' : 'passenger',
    cargo_type: c.category === 'parcel' ? 'parcel' : 'none',
    stops: stopsOf(c.places),
    stop_count: c.places.length,
    comment: c.comment,
    cargo_description: c.cargo,
    fare: c.fare,
    currency: 'KZT',
    commission_amount: COMMISSION,
    vehicle_class: c.vehicle_class,
    vehicle_class_name: className(c.vehicle_class),
    passenger_count: c.passengers,
    cargo_size: c.cargo_size,
    cargo_volume_l: c.cargo_size === 'm' ? 60 : 0,
    payment_method: 'cash',
    distance_m: c.distance_m,
    duration_s: c.duration_s,
    distance_to_pickup_m: c.to_pickup_m,
    locality: city.name.kk,
    tariff_region: { code: city.code, name: city.name },
    created_at: isoAgo(c.age_s),
    search_expires_at: isoIn(300 - c.age_s),
  }
}

function detail(c: CardSpec) {
  const mine = accepted && c.id === ORDER_ID
  const geometry = c.id === ORDER_ID ? ROUTE.geometry : { type: 'LineString' as const, coordinates: c.places.map((p) => [p.lon, p.lat] as [number, number]) }
  return {
    ...card(c),
    status: mine ? 'accepted' : 'searching',
    is_mine: mine,
    recipient_phone: null,
    commission_bps: 1000,
    stops: c.places.map((p, i) => ({
      id: `5e0d0000-0000-4000-8000-00000000000${i + 1}`,
      seq: i,
      kind: kind(i, c.places.length),
      lat: p.lat,
      lon: p.lon,
      address: p.title,
      address_details: null,
      completed_at: null,
    })),
    route: { geometry, legs: ROUTE.legs, bbox: bboxOf(geometry.coordinates), distance_m: c.distance_m, duration_s: c.duration_s },
    passenger: mine
      ? { first_name: 'Айгерім', phone: '+77017654321', phone_verified: true, telegram_username: 'aigerim_demo', telegram_url: 'https://t.me/aigerim_demo' }
      : null,
    next_stop_id: mine ? '5e0d0000-0000-4000-8000-000000000001' : null,
    events: [],
    rating: null,
    cancel_reason: null,
    cancelled_by: null,
    can_accept: !mine,
    can_arrive: mine,
    can_start: false,
    can_complete: false,
    can_cancel: false,
    version: mine ? 2 : 1,
    accepted_at: mine ? new Date().toISOString() : null,
    arrived_at: null,
    started_at: null,
    completed_at: null,
    cancelled_at: null,
  }
}

const wallet = () => {
  const balance = 4400 - (accepted ? COMMISSION : 0)
  return { balance, reserved: 0, available: balance, currency: 'KZT', min_online_balance: 200, updated_at: isoAgo(600) }
}

const ledger = () => {
  const welcome = { id: 1, entry_type: 'bonus_credit', balance_delta: 500, reserved_delta: 0, balance_after: 500, reserved_after: 0, currency: 'KZT', order_id: null, order_number: null, topup_id: null, topup_reference: null, description: 'welcome bonus', created_at: isoAgo(30 * 86_400) }
  const topped = { ...welcome, id: 2, entry_type: 'topup_credit', balance_delta: 3900, balance_after: 4400, topup_id: TOPUP_ID, topup_reference: 'EMB001-TOPUP-000122', description: null, created_at: isoAgo(2 * 86_400) }
  const charge = { ...welcome, id: 3, entry_type: 'commission_charge', balance_delta: -COMMISSION, balance_after: 4400 - COMMISSION, order_id: ORDER_ID, order_number: 1024, description: null, created_at: new Date().toISOString() }
  return (accepted ? [charge, topped, welcome] : [topped, welcome])
}

const topup = () => ({
  id: TOPUP_ID,
  number: 123,
  reference: 'EMB001-TOPUP-000123',
  amount: 5000,
  verified_amount: null,
  currency: 'KZT',
  status: 'created',
  provider: 'kaspi_manual',
  payment: {
    method: 'kaspi_manual',
    url: KASPI_URL,
    recipient_name: PAYEE,
    recipient_phone: null,
    instructions: '',
    reference: 'EMB001-TOPUP-000123',
    amount: 5000,
    currency: 'KZT',
  },
  driver_payment_reference: null,
  driver_note: null,
  has_receipt: false,
  can_upload_receipt: true,
  can_submit: true,
  can_cancel: true,
  created_at: isoAgo(60),
  driver_reported_paid_at: null,
  expires_at: isoIn(86_400),
  confirmed_at: null,
  rejected_at: null,
  rejected_reason: null,
  refunded_at: null,
  receipt: null,
})

const profile = () => ({
  id: 'a1b2c3d4-0000-4000-8000-00000000e001',
  code: 'EMB001',
  status: 'approved',
  first_name: 'Ерлан',
  last_name: 'Сапаров',
  patronymic: '',
  iin_masked: '********0017',
  phone: '+77011234567',
  phone_verified: true,
  avatar: null,
  city,
  vehicle: {
    make: 'Toyota',
    model: 'Camry',
    color: 'ақ',
    plate: '123 ABC 04',
    vehicle_class: 'sedan',
    vehicle_class_name: className('sedan'),
    seats: 4,
    cargo_volume_l: 400,
  },
  rating: 4.9,
  rating_count: 287,
  completed_trips: 312,
  is_online: online,
  online_since: online ? isoAgo(3600) : null,
  approved_at: isoAgo(90 * 86_400),
  suspended_reason: null,
  created_at: isoAgo(120 * 86_400),
})

const availability = () => ({
  is_online: online,
  online_since: online ? isoAgo(3600) : null,
  can_go_online: true,
  blocked_reason: null,
  available: 4400,
  min_online_balance: 200,
  currency: 'KZT',
})

function api(): DemoApi {
  const me = () => ({
    id: 'ab12cd34-0000-4000-8000-0000000000d1',
    telegram_user_id: 700000002,
    first_name: 'Ерлан',
    last_name: 'Сапаров',
    username: null,
    language: 'kk',
    phone_masked: '+7 701 ••• •• 67',
    phone_verified: true,
    app: 'driver',
    driver: registering ? null : { id: profile().id, code: 'EMB001', status: 'approved', is_online: online, city },
  })
  const find = (id: string | undefined) => CARDS.find((c) => c.id === id)
  return new DemoApi()
    .on('POST', '/auth/telegram/:app', () => ({ token: 'demo-token', expires_at: isoIn(86_400) }))
    .on('GET', '/public/config', () => ({
      app: 'driver',
      default_language: 'kk',
      languages: ['kk', 'ru', 'en'],
      city: { code: 'EMB', name: city.name, center: { lat: 48.8275, lon: 58.1442 }, zoom: 14 },
      map: { style_url: 'https://tiles.openfreemap.org/styles/positron' },
      routing: { max_points: 5 },
      bots: { passenger_username: 'jantaxi_bot', driver_username: 'jantaxi_driver_bot' },
      support: { telegram_username: 'jantaxi_support', phone: '+77001234567' },
      navigation: { twogis_base_url: 'https://2gis.kz' },
      kaspi: { payment_url: KASPI_URL, recipient_name: PAYEE, instructions: null },
      topup: { min: 1000, max: 50000, presets: [2000, 5000, 10000] },
      vehicle_classes: VEHICLE_CLASSES,
      cargo_sizes: [
        { code: 's', volume_l: 10 },
        { code: 'm', volume_l: 60 },
        { code: 'l', volume_l: 400 },
        { code: 'xl', volume_l: 1500 },
      ],
      max_passenger_count: 15,
      commission: { mode: 'fixed', fixed_amount: COMMISSION },
      welcome_bonus: 500,
    }))
    .on('GET', '/me', me)
    .on('PATCH', '/me/preferences', me)
    .on('GET', '/driver/profile', profile)
    .on('GET', '/driver/registration', () => (registering ? registration() : apiError(404, 'not_found')))
    .on('PUT', '/driver/registration', (r) => {
      draft = (r.body as Record<string, unknown>) ?? {}
      return registration()
    })
    .on('POST', '/driver/registration/check', (r) => {
      const input = String((r.body as { plate?: string })?.plate ?? '')
      return { plate: { input, normalized: input.replace(/\s+/g, ''), display: input, valid: true, available: true } }
    })
    .on('PATCH', '/driver/availability', (r) => {
      online = (r.body as { online?: boolean })?.online === true
      return availability()
    })
    .on('GET', '/driver/availability', availability)
    .on('POST', '/driver/location', () => undefined)
    .on('GET', '/driver/orders', () => ({ items: accepted ? [] : CARDS.map(card), eligibility: { eligible: online && !accepted, reason: online ? null : 'offline' } }))
    .on('GET', '/driver/orders/active', () => (accepted ? detail(CARDS[0]) : undefined))
    .on('GET', '/driver/orders/history', () => ({ items: [], next_cursor: null }))
    .on('GET', '/driver/orders/:id', (r) => {
      const c = find(r.params.id)
      return c ? detail(c) : apiError(404, 'not_found')
    })
    .on('POST', '/driver/orders/:id/accept', (r) => {
      const c = find(r.params.id)
      if (!c) return apiError(404, 'not_found')
      accepted = c.id === ORDER_ID
      return detail(c)
    })
    .on('GET', '/driver/wallet', wallet)
    .on('GET', '/driver/wallet/transactions', () => ({ items: ledger(), next_cursor: null }))
    .on('GET', '/driver/wallet/topups', () => ({ items: [topup()], next_cursor: null }))
    .on('POST', '/driver/wallet/topups', topup)
    .on('GET', '/driver/wallet/topups/:id', topup)
    .on('POST', '/driver/wallet/topups/:id/submit', () => ({ ...topup(), status: 'pending_verification', can_submit: false, can_cancel: false, driver_reported_paid_at: new Date().toISOString() }))
    .on('POST', '/driver/wallet/topups/:id/cancel', () => ({ ...topup(), status: 'expired', can_submit: false, can_cancel: false, can_upload_receipt: false }))
}

/** Installs the demo runtime; call before bootTelegram(). */
export function installDemo(): DemoRuntime {
  return installDemoRuntime({
    api: api(),
    device: CAR,
    user: { id: 700000002, first_name: 'Ерлан', last_name: 'Сапаров', username: 'erlan_demo', language_code: 'kk' },
  })
}

/** Applies the requested screen once the app's stores and router exist. */
export async function openDemoScreen(_pinia: Pinia, router: Router): Promise<void> {
  // Lets the screenshot script's map walk-through navigate like a user.
  ;(window as unknown as { __jtRouter?: Router }).__jtRouter = router
  switch (params.get('demo')) {
    case 'orders':
      await router.replace({ name: 'orders' })
      break
    case 'order':
      await router.replace({ name: 'order', params: { id: ORDER_ID } })
      break
    case 'topup':
      await router.replace({ name: 'topup-detail', params: { id: TOPUP_ID } })
      break
    case 'wallet':
      await router.replace({ name: 'wallet' })
      break
    case 'register':
      await router.replace({ name: 'register', query: { step: 'vehicle' } })
      break
    default:
      break
  }
}
