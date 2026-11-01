// Booking sheets on a stand-in map stage: pickup A written by hand in the
// place search (it keeps A's point on the map, also when the pin moves), the
// place search's manual entry and recent places, the confirmation sheet's
// route summary and fare section, which only deliveries have (a taxi goes at
// the chosen class's price shown on its row and on the button).

import { flushPromises, mount } from '@vue/test-utils'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, nextTick, reactive, ref, shallowRef, type Component } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { reverseGeocode } from '../../api/geo'
import { listOrders } from '../../api/orders'
import { parseOrderSummary, parseQuote } from '../../api/parse'
import { previewRoute } from '../../api/routes'
import type { GeoReverse, OrderSummary, RouteQuote } from '../../api/types'
import type { PlaceValue } from '../../domain/route'
import { i18n } from '../../i18n'
import { ROUTE } from '../../router/names'
import { defaultPublicConfig, fetchPublicConfig } from '../../services/config'
import { ApiError } from '../../services/http'
import { STAGE_KEY, type MapStage, type StageControls } from '../../stage/mapStage'
import { MANUAL_DISABLED, PREVIEW_DEBOUNCE_MS, useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import type { LatLon } from '../../utils/geo'
import type { Category } from '../../utils/status'
import ConfirmSheet from './ConfirmSheet.vue'
import HomeSheet from './HomeSheet.vue'
import PickOnMapSheet from './PickOnMapSheet.vue'
import PlaceSearchSheet from './PlaceSearchSheet.vue'
import RouteEditorSheet from './RouteEditorSheet.vue'

vi.mock('../../api/routes', () => ({ previewRoute: vi.fn() }))
vi.mock('../../api/geo', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/geo')>()),
  searchPlaces: vi.fn().mockResolvedValue([]),
  reverseGeocode: vi.fn(),
}))
vi.mock('../../api/orders', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/orders')>()),
  listOrders: vi.fn(),
}))
vi.mock('../../services/config', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../services/config')>()),
  fetchPublicConfig: vi.fn(),
}))
vi.mock('../../services/realtime', () => ({
  useRealtime: () => ({ on: () => () => undefined, onResync: () => () => undefined }),
}))

const preview = vi.mocked(previewRoute)
const reverse = vi.mocked(reverseGeocode)
/** Money and the submit label use no-break spaces. */
const plain = (s: string) => s.replace(/ /g, ' ')

const TYPED = 'Мешіттің жанында, көк қақпа'
const PIN: PlaceValue = { lat: 48.8264, lon: 58.1442, address: 'Абай көшесі, 5', subtitle: 'Ембі', source: 'map', inServiceArea: true }
const STATION: PlaceValue = { lat: 48.8291, lon: 58.1504, address: 'Теміржол вокзалы', subtitle: null, source: 'search', inServiceArea: true }
const MANUAL_B: PlaceValue = { lat: null, lon: null, address: 'Ескі мәдениет үйі', subtitle: null, source: 'manual', inServiceArea: null }
/** The default map centre (Embi) and a point the passenger dragged the map to. */
const EMBI: LatLon = { lat: 48.8264, lon: 58.1442 }
const DRAGGED: LatLon = { lat: 48.8301, lon: 58.1512 }

function named(p: LatLon, title: string): GeoReverse {
  return { ...p, title, subtitle: 'Ембі', source: 'photon', in_service_area: true }
}

/**
 * A stand-in stage; `withMap` gives it a map whose centre is `centre` and
 * keeps the move listeners so a test can play a drag of the map.
 */
function fakeStage(withMap?: { centre: () => LatLon }): MapStage & { drag(to: LatLon): void } {
  const zero = { top: 0, right: 0, bottom: 0, left: 0 }
  const starts = new Set<(user: boolean) => void>()
  const ends = new Set<(center: LatLon, user: boolean) => void>()
  return {
    map: shallowRef(withMap ? ({} as MapLibreMap) : null),
    gesture: ref(false),
    sheetHeight: ref(0),
    safe: computed(() => zero),
    padding: computed(() => zero),
    stops: { setStops: vi.fn(), setDraggable: vi.fn(), clear: vi.fn() },
    route: { setRoute: vi.fn(), setApproach: vi.fn(), clear: vi.fn() },
    car: { set: vi.fn() },
    user: { set: vi.fn() },
    pin: reactive<MapStage['pin']>({ visible: false, label: '', callout: null, onCallout: null }),
    controls: reactive<StageControls>({ menu: false, back: false, locate: false, recenter: null, accuracyChip: false }),
    setStopDrag: vi.fn(),
    fit: vi.fn(),
    centerOn: vi.fn(),
    center: () => withMap?.centre() ?? null,
    onMoveStart: (cb) => {
      starts.add(cb)
      return () => starts.delete(cb)
    },
    onMoveEnd: (cb) => {
      ends.add(cb)
      return () => ends.delete(cb)
    },
    claim: () => Symbol('screen'),
    release: vi.fn(),
    drag(to) {
      for (const cb of [...starts]) cb(true)
      for (const cb of [...ends]) cb(to, true)
    },
  }
}

const Blank = { template: '<div />' }
const SheetStub = { template: '<section><slot name="header" /><slot /><footer><slot name="footer" /></footer></section>' }

async function mountAt(component: Component, path: string, stage: MapStage = fakeStage()): Promise<{ w: ReturnType<typeof mount>; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: ROUTE.home, component: Blank },
      { path: '/ride', name: ROUTE.book, component: Blank },
      { path: '/ride/search/:index', name: ROUTE.bookSearch, component, meta: { backTo: { name: ROUTE.book } } },
      { path: '/ride/pick/:index', name: ROUTE.bookPick, component: Blank },
      { path: '/ride/confirm', name: ROUTE.confirm, component, meta: { backTo: { name: ROUTE.book } } },
      { path: '/order/:id', name: ROUTE.order, component: Blank },
    ],
  })
  await router.push(path)
  await router.isReady()
  const w = mount(component, {
    global: {
      plugins: [i18n, router],
      provide: { [STAGE_KEY]: stage },
      stubs: { JSheet: SheetStub, PhoneShareSheet: true },
    },
  })
  await flushPromises()
  return { w, router }
}

beforeEach(() => {
  setActivePinia(createPinia())
  preview.mockReset()
  reverse.mockReset()
  vi.mocked(listOrders).mockResolvedValue({ items: [], next_cursor: null })
  vi.mocked(fetchPublicConfig).mockReset()
  const orders = useOrdersStore()
  orders.historyLoaded = true
  const session = useSessionStore()
  session.configLoaded = true
  session.config.orders = { manual_destination_enabled: true, manual_destination_pricing: 'passenger_tariff_bounds' }
})

afterEach(() => vi.useRealTimers())

describe('place search: pickup A in the passenger’s own words', () => {
  it('offers the manual entry for A and keeps the pin as A’s point', async () => {
    const booking = useBookingStore()
    booking.setStopPlace(0, PIN)
    const { w, router } = await mountAt(PlaceSearchSheet, '/ride/search/0')

    const action = w.find('.search-row--manual')
    expect(action.text()).toContain(i18n.global.t('manual.action'))
    await action.trigger('click')
    const form = w.find('.search-actions--manual')
    expect(form.text()).toContain(i18n.global.t('manual.label'))
    expect(form.text()).toContain(i18n.global.t('manual.pickup_helper'))

    await form.find('input').setValue('  Мешіттің жанында,  көк қақпа ')
    await form.findAll('button').find((b) => b.text() === i18n.global.t('manual.save'))!.trigger('click')
    await flushPromises()

    expect(booking.stops[0]!.place).toEqual({ ...PIN, address: TYPED, typed: true })
    expect(router.currentRoute.value.name).toBe(ROUTE.book)
  })

  it('starts from the typed text when A is edited again', async () => {
    const booking = useBookingStore()
    booking.setStopPlace(0, { ...PIN, address: TYPED, typed: true })
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/0')
    await w.find('.search-row--manual').trigger('click')
    expect((w.find('.search-actions--manual input').element as HTMLInputElement).value).toBe(TYPED)
  })

  it('attaches the text to the device position while A has no point', async () => {
    const fix = { lat: 48.8211, lon: 58.152, accuracy_m: 15, heading: null, speed_mps: null, timestamp: Date.now(), source: 'browser' as const }
    useLocationStore().current = fix
    const booking = useBookingStore()
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/0')
    await w.find('.search-row--manual').trigger('click')
    await w.find('.search-actions--manual input').setValue(TYPED)
    await w.findAll('.search-actions--manual button').find((b) => b.text() === i18n.global.t('manual.save'))!.trigger('click')
    expect(booking.stops[0]!.place).toMatchObject({ lat: fix.lat, lon: fix.lon, address: TYPED, source: 'gps', typed: true })
  })

  it('has no manual entry for A without any point to keep, only a hint above «Choose on map»', async () => {
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/0')
    expect(w.find('.search-row--manual').exists()).toBe(false)
    expect(w.text()).toContain(i18n.global.t('search.use_my_location'))
    const hint = w.find('.search-actions .search-note')
    expect(hint.text()).toBe(i18n.global.t('manual.pickup_point_first'))
    expect(hint.element.nextElementSibling?.textContent).toContain(i18n.global.t('search.choose_on_map'))
  })

  it('asks to mark A first while A is only the default map centre named after a failed GPS attempt', async () => {
    const booking = useBookingStore()
    booking.setPickupPoint({ ...PIN, address: 'Ембі, Абай көшесі' }, true)
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/0')
    expect(w.find('.search-row--manual').exists()).toBe(false)
    expect(w.find('.search-actions .search-note').text()).toBe(i18n.global.t('manual.pickup_point_first'))

    // A device position is a point drivers can be sent to.
    useLocationStore().current = { lat: 48.8211, lon: 58.152, accuracy_m: 15, heading: null, speed_mps: null, timestamp: Date.now(), source: 'browser' }
    await nextTick()
    expect(w.find('.search-row--manual').exists()).toBe(true)
    expect(w.find('.search-actions .search-note').exists()).toBe(false)
  })

  it('keeps manual stops after A behind the administrator switch, without coordinates', async () => {
    const booking = useBookingStore()
    booking.setStopPlace(0, PIN)
    useSessionStore().config.orders = { manual_destination_enabled: false, manual_destination_pricing: 'disabled' }
    const off = await mountAt(PlaceSearchSheet, '/ride/search/1')
    expect(off.w.find('.search-row--manual').exists()).toBe(false)
    off.w.unmount()

    useSessionStore().config.orders = { manual_destination_enabled: true, manual_destination_pricing: 'passenger_tariff_bounds' }
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/1')
    expect(w.find('.search-actions--manual').exists()).toBe(false)
    await w.find('.search-row--manual').trigger('click')
    expect(w.find('.search-actions--manual').text()).toContain(i18n.global.t('manual.helper'))
    await w.find('.search-actions--manual input').setValue(MANUAL_B.address)
    await w.findAll('.search-actions--manual button').find((b) => b.text() === i18n.global.t('manual.save'))!.trigger('click')
    expect(booking.stops[1]!.place).toEqual(MANUAL_B)
    expect(booking.stops[0]!.place).toEqual(PIN)
  })
})

/** Fares from lo to hi in steps. */
function fareRange(lo: number, hi: number, step: number): number[] {
  const out: number[] = []
  for (let v = lo; v <= hi; v += step) out.push(v)
  return out
}

let quoteSeq = 0

/** A one-class (sedan) quote as the API sends it; a manual route has no recommendation. */
function sedanQuote(category: Category, manual: boolean): RouteQuote {
  const quoteId = `00000000-0000-4000-8000-0000000003${String(++quoteSeq).padStart(2, '0')}`
  const fare = manual
    ? { recommended: null, min: 400, max: 1500, step: 50, options: fareRange(400, 1500, 50) }
    : { recommended: 700, min: 700, max: 1500, step: 50, options: fareRange(700, 1500, 50) }
  const expires = new Date(Date.now() + 10 * 60_000).toISOString()
  return parseQuote({
    quote_id: quoteId,
    expires_at: expires,
    category,
    vehicle_class: 'sedan',
    passenger_count: category === 'taxi' ? 1 : null,
    cargo_size: category === 'taxi' ? null : 's',
    stops: [],
    distance_m: manual ? null : 3200,
    duration_s: manual ? null : 540,
    legs: [],
    geometry: null,
    bbox: null,
    fare,
    commission_bps: 1000,
    payment_methods: ['cash', 'bank_transfer'],
    options: [{ vehicle_class: 'sedan', icon: 'sedan', seats_max: 4, cargo_volume_l: 400, quote_id: quoteId, expires_at: expires, fare, commission_bps: 1000, eta_s: null }],
  })
}

/** A booking with a ready quote, as when the passenger continues from the route editor. */
async function readyBooking(
  category: Category,
  destination: PlaceValue,
  pickup: PlaceValue = { ...PIN, address: TYPED, typed: true },
): Promise<ReturnType<typeof useBookingStore>> {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const booking = useBookingStore()
  booking.setCategory(category)
  preview.mockResolvedValue(sedanQuote(category, destination.source === 'manual'))
  booking.setStopPlace(0, pickup)
  booking.setStopPlace(1, destination)
  await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
  expect(booking.quoteCurrent).toBe(true)
  return booking
}

describe('confirmation: route summary', () => {
  it('lists the stop texts the passenger wrote, as drivers get them, and never a dash for an unknown length', async () => {
    await readyBooking('taxi', MANUAL_B)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(w.findAll('.confirm-stops .stop-badge').map((b) => b.text())).toEqual(['A', 'B'])
    expect(w.findAll('.confirm-stops__address').map((a) => a.text())).toEqual([TYPED, MANUAL_B.address])
    const route = w.findAll('.confirm-fact')[1]!
    expect(route.text()).toContain(i18n.global.t('booking.title'))
    expect(route.text()).toContain(i18n.global.t('manual.route_unknown'))
    expect(route.text()).not.toContain('—')
    w.unmount()
  })

  it('keeps the compact route row when every address comes from the map or the search', async () => {
    await readyBooking('taxi', STATION, PIN)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(w.find('.confirm-stops').exists()).toBe(false)
    expect(plain(w.findAll('.confirm-fact')[1]!.text())).toContain('3,2 км')
    w.unmount()
  })
})

describe('confirmation: fare section', () => {
  it('has no fare input for a taxi with a manual destination: the class price goes on the button', async () => {
    const booking = await readyBooking('taxi', MANUAL_B)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(w.find('.confirm-fare').exists()).toBe(false)
    expect(w.find('input[type="number"]').exists()).toBe(false)
    expect(plain(w.find('.vclass__row.is-selected').text())).toContain('400 ₸')
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 400 ₸')
    expect(booking.fare).toBe(400)
    w.unmount()
  })

  it('has no fare picker for a taxi on a calculated route either', async () => {
    await readyBooking('taxi', STATION)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(w.find('.confirm-fare').exists()).toBe(false)
    expect(w.find('[role="spinbutton"]').exists()).toBe(false)
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 700 ₸')
    w.unmount()
  })

  it('keeps the fare input of a parcel with a manual destination', async () => {
    await readyBooking('parcel', MANUAL_B)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    const section = w.find('.confirm-fare')
    expect(section.exists()).toBe(true)
    expect(section.find('input[type="number"]').exists()).toBe(true)
    expect(plain(section.text())).toContain('400 ₸–1 500 ₸')
    w.unmount()
  })

  it('keeps the fare picker of another delivery on a calculated route', async () => {
    await readyBooking('other', STATION)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(w.find('.confirm-fare [role="spinbutton"]').exists()).toBe(true)
    w.unmount()
  })
})

describe('place search: manual entry and recent places', () => {
  it('shows the manual entry for B once the public config arrives after it failed at launch', async () => {
    const session = useSessionStore()
    session.configLoaded = false
    session.config = defaultPublicConfig()
    const config = defaultPublicConfig()
    config.orders = { manual_destination_enabled: true, manual_destination_pricing: 'passenger_tariff_bounds' }
    vi.mocked(fetchPublicConfig).mockResolvedValueOnce(config)
    useBookingStore().setStopPlace(0, PIN)
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/1')
    expect(fetchPublicConfig).toHaveBeenCalledWith('passenger')
    expect(session.configLoaded).toBe(true)
    expect(w.find('.search-row--manual').exists()).toBe(true)
  })

  it('lists manual recent places under their own keys and hides them while manual entry is off', async () => {
    const warn = vi.spyOn(console, 'warn')
    const trip = (id: string, pickup: string, lat: number, destination: string): OrderSummary =>
      parseOrderSummary({
        id,
        status: 'completed',
        category: 'taxi',
        pickup: { seq: 0, kind: 'pickup', lat, lon: 58.14, address: pickup },
        destination: { seq: 1, kind: 'destination', mode: 'manual', lat: null, lon: null, address: destination },
      })!
    const orders = useOrdersStore()
    orders.history = [
      trip('o1', 'Абай көшесі, 5', 48.82, 'Ескі мәдениет үйі'),
      trip('o2', 'Мектеп', 48.84, 'Ауылдағы үй'),
      trip('o3', 'Базар', 48.86, 'Су мұнарасы'),
    ]
    useBookingStore().setStopPlace(0, PIN)
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/1')
    const titles = () => w.findAll('.search-section .search-row__title').map((t) => t.text())
    expect(titles()).toEqual(['Ескі мәдениет үйі', 'Абай көшесі, 5', 'Ауылдағы үй', 'Мектеп', 'Су мұнарасы', 'Базар'])

    orders.history = [...orders.history].reverse()
    await nextTick()
    expect(titles()).toEqual(['Су мұнарасы', 'Базар', 'Ауылдағы үй', 'Мектеп', 'Ескі мәдениет үйі', 'Абай көшесі, 5'])
    expect(warn.mock.calls.flat().join(' ')).not.toContain('Duplicate keys')

    // The API refused manual stops: no manual entry and no manual places any more.
    useSessionStore().disableManualDestinations()
    await nextTick()
    expect(titles()).toEqual(['Базар', 'Мектеп', 'Абай көшесі, 5'])
    expect(w.find('.search-row--manual').exists()).toBe(false)
  })
})

describe('home: the pickup pin', () => {
  it('takes the default map centre only as a fallback after the GPS attempt failed, until the passenger moves the map', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    reverse.mockImplementation(async (p) => named({ lat: p.lat, lon: p.lon }, 'Абай көшесі'))
    const stage = fakeStage({ centre: () => EMBI })
    const booking = useBookingStore()
    const { w } = await mountAt(HomeSheet, '/', stage)
    await vi.advanceTimersByTimeAsync(300)
    expect(booking.stops[0]!.place).toMatchObject({ ...EMBI, address: 'Абай көшесі', source: 'map' })
    expect(booking.pickupFallback).toBe(true)

    stage.drag(DRAGGED)
    await vi.advanceTimersByTimeAsync(300)
    expect(booking.stops[0]!.place).toMatchObject({ ...DRAGGED, source: 'map' })
    expect(booking.pickupFallback).toBe(false)
    w.unmount()
  })

  it('a zoom does not place the pin, a drag does, and the placed pin survives leaving the screen', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    reverse.mockImplementation(async (p) => named({ lat: p.lat, lon: p.lon }, 'Абай көшесі'))
    const stage = fakeStage({ centre: () => EMBI })
    const booking = useBookingStore()
    const first = await mountAt(HomeSheet, '/', stage)
    await vi.advanceTimersByTimeAsync(300)
    // A pinch zoom keeps the centre: still only the default centre.
    stage.drag(EMBI)
    await vi.advanceTimersByTimeAsync(300)
    expect(booking.pickupFallback).toBe(true)
    expect(booking.pinPlaced).toBe(false)
    expect(booking.setPickupText(TYPED)).toBe(false)
    first.w.unmount()

    // A drag places it; coming back to the screen keeps that choice.
    const dragged = fakeStage({ centre: () => DRAGGED })
    const second = await mountAt(HomeSheet, '/', dragged)
    dragged.drag(DRAGGED)
    expect(booking.pinPlaced).toBe(false) // the centre did not change: no drag yet
    second.w.unmount()
    const moving = fakeStage({ centre: () => EMBI })
    const third = await mountAt(HomeSheet, '/', moving)
    moving.drag(DRAGGED)
    await vi.advanceTimersByTimeAsync(300)
    expect(booking.pinPlaced).toBe(true)
    expect(booking.pickupFallback).toBe(false)
    third.w.unmount()
    const again = await mountAt(HomeSheet, '/', fakeStage({ centre: () => DRAGGED }))
    await vi.advanceTimersByTimeAsync(300)
    expect(booking.pickupFallback).toBe(false)
    again.w.unmount()
  })

  it('moves only the point of a pickup the passenger wrote: the text stays', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    reverse.mockImplementation(async (p) => named({ lat: p.lat, lon: p.lon }, 'Абай көшесі, 9'))
    const booking = useBookingStore()
    booking.setStopPlace(0, PIN)
    expect(booking.setPickupText(TYPED)).toBe(true)
    const stage = fakeStage({ centre: () => EMBI })
    const { w } = await mountAt(HomeSheet, '/', stage)
    expect(stage.pin.callout?.title).toBe(TYPED)

    stage.drag(DRAGGED)
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(booking.stops[0]!.place).toEqual({ ...DRAGGED, address: TYPED, subtitle: 'Ембі', source: 'map', inServiceArea: true, typed: true })
    expect(booking.pickupFallback).toBe(false)
    expect(stage.pin.callout?.title).toBe(TYPED)
    w.unmount()
  })
})

describe('choose on map: pickup A in the passenger’s own words', () => {
  it('takes the chosen point and keeps the text', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    reverse.mockImplementation(async (p) => named({ lat: p.lat, lon: p.lon }, 'Абай көшесі, 9'))
    const booking = useBookingStore()
    booking.setStopPlace(0, PIN)
    booking.setPickupText(TYPED)
    const { w } = await mountAt(PickOnMapSheet, '/ride/pick/0', fakeStage({ centre: () => DRAGGED }))
    await vi.advanceTimersByTimeAsync(1200)
    await flushPromises()
    expect(w.find('.pick-address__title').text()).toBe('Абай көшесі, 9')

    await w.find('footer button').trigger('click')
    expect(booking.stops[0]!.place).toEqual({ ...DRAGGED, address: TYPED, subtitle: 'Ембі', source: 'map', inServiceArea: true, typed: true })
    w.unmount()
  })
})

describe('route editor: manual stops switched off', () => {
  it('asks to choose the manual stop from the search or the map, and shows a typed A as written', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    preview.mockRejectedValue(new ApiError(422, 'validation_failed', 'invalid', { stops: MANUAL_DISABLED }))
    const booking = useBookingStore()
    booking.setStopPlace(0, PIN)
    // Text that looks like coordinates is still the passenger's own words.
    booking.setPickupText('48.82, 58.14')
    booking.setStopPlace(1, MANUAL_B)
    const { w } = await mountAt(RouteEditorSheet, '/ride')
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    await flushPromises()
    const rows = w.findAll('.stops__row')
    expect(rows[0]!.find('.stops__title').text()).toBe('48.82, 58.14')
    expect(rows[1]!.find('.stops__title').text()).toBe(MANUAL_B.address)
    expect(rows[1]!.find('.stops__error').text()).toBe(i18n.global.t('booking.manual_disabled'))
    expect(useSessionStore().manualDestinations).toBe(false)
    w.unmount()
  })
})
