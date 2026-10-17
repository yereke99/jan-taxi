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
// Without ?demo the app starts on home and the whole flow is clickable: the
// in-memory API quotes the route, creates the order and assigns the driver.

import type { Pinia } from 'pinia'
import type { Router } from 'vue-router'
import { useBookingStore } from '../stores/booking'
import {
  APPROACH,
  apiError,
  bboxOf,
  broadcast,
  DemoApi,
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

interface DemoState {
  status: Status | null
  createdAt: string
  fare: number
  comment: string | null
  acceptedAt: string | null
  /** 0..1 progress of the driver along the approach route. */
  approach: number
  version: number
}

const state: DemoState = {
  status: null,
  createdAt: isoAgo(SEARCH_ELAPSED_S),
  fare: FARE.recommended,
  comment: COMMENT,
  acceptedAt: null,
  approach: 0,
  version: 1,
}

const city = { id: 'c0a80001-0000-4000-8000-000000000001', code: 'EMB', name: { kk: 'Ембі', ru: 'Эмба', en: 'Emba' } }

function fareOptions() {
  const options: number[] = []
  for (let v = FARE.min; v <= FARE.max; v += FARE.step) options.push(v)
  return { ...FARE, options }
}

function stopKind(i: number, n: number): 'pickup' | 'intermediate' | 'destination' {
  return i === 0 ? 'pickup' : i === n - 1 ? 'destination' : 'intermediate'
}

function quote(stops: Array<{ lat: number; lon: number; address: string; address_details?: string }>) {
  return {
    quote_id: 'b5f1c2d4-9e8a-4f3b-8c7d-6a5e4f3b2c1d',
    expires_at: isoIn(600),
    category: 'taxi',
    city,
    stops: stops.map((s, i) => ({ seq: i, kind: stopKind(i, stops.length), lat: s.lat, lon: s.lon, address: s.address, address_details: s.address_details ?? null })),
    distance_m: ROUTE.distance_m,
    duration_s: ROUTE.duration_s,
    legs: ROUTE.legs.slice(0, Math.max(1, stops.length - 1)),
    geometry: ROUTE.geometry,
    bbox: bboxOf(ROUTE.geometry.coordinates),
    fare: fareOptions(),
    commission_bps: 1000,
    payment_methods: ['cash', 'bank_transfer'],
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

const vehicle = { make: 'Toyota', model: 'Camry', color: 'ақ', plate: '123 ABC 04' }

function orderDto() {
  const s = state.status ?? 'searching'
  const n = STOPS.length
  const assigned = s === 'accepted'
  const loc = assigned ? pointAlong(APPROACH.geometry, state.approach) : null
  return {
    id: ORDER_ID,
    number: 1024,
    status: s,
    category: 'taxi',
    cargo_description: null,
    recipient_phone: null,
    comment: state.comment,
    fare: state.fare,
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
    cancel_reason: s === 'cancelled' ? 'passenger_changed_mind' : null,
    cancelled_by: s === 'cancelled' ? 'passenger' : null,
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
    fare: o.fare,
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

/** Two finished trips: their destinations are the "recent addresses" on home. */
function history() {
  const done = (id: string, dest: { lat: number; lon: number; title: string }, daysAgo: number) => ({
    id,
    number: 1000 + daysAgo,
    status: 'completed',
    category: 'taxi',
    fare: 600,
    payment_method: 'cash',
    pickup: { seq: 0, kind: 'pickup', lat: STOPS[0].lat, lon: STOPS[0].lon, address: STOPS[0].title },
    destination: { seq: 1, kind: 'destination', lat: dest.lat, lon: dest.lon, address: dest.title },
    stop_count: 2,
    distance_m: 2400,
    driver_first_name: 'Ерлан',
    rating: 5,
    can_rate: false,
    created_at: isoAgo(daysAgo * 86_400),
    completed_at: isoAgo(daysAgo * 86_400 - 900),
    cancelled_at: null,
  })
  return [done('0f1e2d3c-0000-4000-8000-0000000000a1', STOPS[3], 1), done('0f1e2d3c-0000-4000-8000-0000000000a2', PLACES[0], 3)]
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
    broadcast('driver.location_updated', { order_id: ORDER_ID, driver_id: driver.id, lat: p.lat, lon: p.lon, heading: 120, recorded_at: new Date().toISOString() })
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
    phone_masked: '+7 701 ••• •• 45',
    phone_verified: true,
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
    }))
    .on('GET', '/me', () => me)
    .on('PATCH', '/me/preferences', (r) => ({ ...me, language: (r.body as { language?: string })?.language ?? 'kk' }))
    .on('GET', '/geo/reverse', (r) => {
      const p = { lat: Number(r.query.get('lat')), lon: Number(r.query.get('lon')) }
      return { ...p, ...reverse(p), source: 'demo', in_service_area: true }
    })
    .on('GET', '/geo/search', (r) => search(r.query.get('q') ?? '').map((p) => ({ ...p, source: 'demo' })))
    .on('POST', '/routes/preview', (r) => {
      const body = r.body as { stops?: Array<{ lat: number; lon: number; address: string }> }
      return quote(body.stops ?? [])
    })
    .on('POST', '/orders', (r) => {
      const body = r.body as { fare?: number; comment?: string }
      state.fare = body.fare ?? FARE.recommended
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
    .on('POST', '/orders/:id/cancel', () => {
      state.status = 'cancelled'
      clearTimeout(searchTimer)
      clearInterval(driverTimer)
      notify('order.cancelled')
      return orderDto()
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
  }
}
