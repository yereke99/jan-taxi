// Booking sheets on a stand-in map stage: pickup A written by hand in the
// place search (it keeps A's point on the map, also when the pin moves), the
// place search's manual entry and recent places, the confirmation sheet's
// route summary and fare section, which only deliveries have (a taxi goes at
// the chosen class's price shown on its row and on the button), and its
// promotion rows: trip fare, coupon or bonus, what the passenger pays (a
// delivery's under its fare picker, the large-vehicle sheet's prices too).
// Then the live order sheet's price with a coupon: running, cancelled, done.

import { flushPromises, mount } from '@vue/test-utils'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, nextTick, reactive, ref, shallowRef, type Component } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { reverseGeocode } from '../../api/geo'
import { createOrder, listOrders } from '../../api/orders'
import { parseOrder, parseOrderSummary, parsePromotions, parseQuote } from '../../api/parse'
import { getPromotions } from '../../api/promotions'
import { previewRoute } from '../../api/routes'
import type { GeoReverse, Order, OrderSummary, RouteQuote } from '../../api/types'
import { activeActionSheet } from '../../components/ui/actionSheet'
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
import OrderSheet from './OrderSheet.vue'
import PickOnMapSheet from './PickOnMapSheet.vue'
import PlaceSearchSheet from './PlaceSearchSheet.vue'
import RouteEditorSheet from './RouteEditorSheet.vue'

/** The order the live order sheet shows (instead of GET /orders/:id with its realtime sync). */
const shown = vi.hoisted(() => ({ order: null as Order | null }))

vi.mock('../../composables/useLiveOrder', async () => {
  const { ref, shallowRef } = await import('vue')
  return {
    useLiveOrder: () => ({ order: shallowRef(shown.order), loading: ref(false), error: shallowRef(null), refresh: () => undefined, accept: () => undefined }),
  }
})
vi.mock('../../api/routes', () => ({ previewRoute: vi.fn() }))
vi.mock('../../api/geo', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/geo')>()),
  searchPlaces: vi.fn().mockResolvedValue([]),
  reverseGeocode: vi.fn(),
}))
vi.mock('../../api/orders', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/orders')>()),
  listOrders: vi.fn(),
  createOrder: vi.fn(),
}))
vi.mock('../../api/promotions', () => ({ getPromotions: vi.fn() }))
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
  // A lookup no test asked for (a debounce left by an earlier test's store)
  // waits forever instead of failing on an undefined answer.
  preview.mockReset().mockImplementation(() => new Promise<never>(() => {}))
  reverse.mockReset().mockImplementation(() => new Promise<never>(() => {}))
  vi.mocked(listOrders).mockResolvedValue({ items: [], next_cursor: null })
  vi.mocked(fetchPublicConfig).mockReset()
  vi.mocked(createOrder).mockReset()
  // An API without promotions unless a test gives the passenger some.
  vi.mocked(getPromotions).mockReset().mockRejectedValue(new ApiError(404, 'not_found', 'no route'))
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
    vi.useFakeTimers({ shouldAdvanceTime: true })
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
    w.unmount()
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
function sedanQuote(category: Category, manual: boolean, price = 700): RouteQuote {
  const quoteId = `00000000-0000-4000-8000-0000000003${String(++quoteSeq).padStart(2, '0')}`
  const fare = manual
    ? { recommended: null, min: 400, max: 1500, step: 50, options: fareRange(400, 1500, 50) }
    : { recommended: price, min: price, max: price + 800, step: 50, options: fareRange(price, price + 800, 50) }
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
  price = 700,
): Promise<ReturnType<typeof useBookingStore>> {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const booking = useBookingStore()
  booking.setCategory(category)
  preview.mockResolvedValue(sedanQuote(category, destination.source === 'manual', price))
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

/** GET /promotions: a 400 ₸ coupon (from `minFare`, valid 72 h; taxi unless `categories`) and/or a bonus balance. */
function promotionsAnswer(opts: { coupon?: boolean; minFare?: number; bonus?: number; categories?: string[] } = {}) {
  const now = Date.now()
  return parsePromotions({
    server_time: new Date(now).toISOString(),
    redemptions_enabled: true,
    coupons:
      opts.coupon === false
        ? []
        : [
            {
              id: 'c1',
              title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' },
              amount: 400,
              min_order_fare: opts.minFare ?? 400,
              categories: opts.categories ?? ['taxi'],
              status: 'issued',
              issued_at: new Date(now - 3_600_000).toISOString(),
              expires_at: new Date(now + 72 * 3_600_000).toISOString(),
            },
          ],
    bonus: { balance: opts.bonus ?? 0, reserved: 0, available: opts.bonus ?? 0 },
    bonus_categories: ['taxi'],
    referral: { state: 'unavailable' },
    marketing_subscribed: true,
  })
}

/** Each row of the order facts as [title, value] texts. */
function factRows(w: ReturnType<typeof mount>): string[][] {
  return w.findAll('.confirm-fact').map((f) => f.findAll(':scope > span').map((s) => plain(s.text())))
}

describe('confirmation: promotion (§23.6)', () => {
  it('lists the trip fare, the coupon and what the passenger pays; the button names that price, the class row its own', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer())
    await readyBooking('taxi', STATION, PIN, 1500)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    const rows = factRows(w)
    expect(rows).toContainEqual(['Сапар құны', '1 500 ₸'])
    expect(rows).toContainEqual(['Купон', '−400 ₸'])
    expect(rows).toContainEqual(['Төлейсіз', '1 100 ₸'])
    expect(w.find('.confirm-fact.is-total').exists()).toBe(true)
    expect(w.find('[role="switch"]').exists()).toBe(false)
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 1 100 ₸')
    expect(plain(w.find('.vclass__row.is-selected').text())).toContain('1 500 ₸')
    w.unmount()
  })

  it('offers the bonus behind a switch that is on; off, the button goes back to the class price', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ coupon: false, bonus: 300 }))
    const booking = await readyBooking('taxi', STATION, PIN)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    const toggle = w.find('[role="switch"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(toggle.attributes('aria-label')).toBe('Бонусты қолдану')
    expect(factRows(w)).toContainEqual(['Бонус', '−300 ₸'])
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 400 ₸')

    await toggle.trigger('click')
    expect(booking.useBonus).toBe(false)
    expect(w.find('.confirm-fact.is-total').exists()).toBe(false)
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 700 ₸')
    w.unmount()
  })

  it('says from which fare a coupon applies when this one is too cheap', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ minFare: 1000 }))
    await readyBooking('taxi', STATION, PIN)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(plain(w.find('.confirm-fact.is-note').text())).toBe('400 ₸ купоны құны 1 000 ₸ және одан жоғары тапсырыстарға қолданылады')
    expect(w.find('.confirm-fact.is-total').exists()).toBe(false)
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 700 ₸')
    w.unmount()
  })

  it('shows no promotion rows when the API has no promotions', async () => {
    await readyBooking('taxi', STATION, PIN, 1500)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(factRows(w).flat()).not.toContain('Сапар құны')
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 1 500 ₸')
    w.unmount()
  })

  it('asks to check the new price after 409 promotion_changed and sends nothing by itself', async () => {
    vi.mocked(getPromotions).mockResolvedValueOnce(promotionsAnswer())
    await readyBooking('taxi', STATION, PIN, 1500)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 1 100 ₸')

    vi.mocked(createOrder).mockRejectedValueOnce(
      new ApiError(409, 'promotion_changed', 'the promotion changed; review the price', {}, { reason: 'no_coupon', discount: 0 }),
    )
    vi.mocked(getPromotions).mockResolvedValueOnce(promotionsAnswer({ coupon: false }))
    await w.find('.confirm-submit').trigger('click')
    await flushPromises()
    expect(w.find('.confirm-notice').text()).toBe('Акция жағдайы өзгерді. Бағаны тексеріп, қайта растаңыз.')
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 1 500 ₸')
    await vi.advanceTimersByTimeAsync(30_000)
    expect(createOrder).toHaveBeenCalledTimes(1)
    w.unmount()
  })

  it('names what the passenger would pay in the large-vehicle sheet: both classes with the coupon', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer())
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const booking = useBookingStore()
    preview.mockResolvedValue(sedanAndMinivan())
    booking.setStopPlace(0, PIN)
    booking.setStopPlace(1, STATION)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    booking.setVehicleClass('minivan')
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Минивэн · 600 ₸')

    await w.find('.confirm-submit').trigger('click')
    await flushPromises()
    const sheet = activeActionSheet.value
    // Minivan 1 000 − 400 and sedan 700 − 400: the prices the confirmation shows after either choice.
    expect(sheet?.actions.map((a) => plain(a.label))).toEqual(['Иә, Минивэн · 600 ₸', 'Седан таңдау · 300 ₸'])
    sheet!.resolve(null)
    await flushPromises()
    expect(createOrder).not.toHaveBeenCalled()
    w.unmount()
  })

  it('shows a coupon that covers deliveries under the fare picker, with what the passenger pays', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ categories: ['taxi', 'parcel'] }))
    const booking = await readyBooking('parcel', STATION, PIN)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    const lines = () => w.findAll('.confirm-fare .confirm-fare__line').map((l) => l.findAll(':scope > span').map((s) => plain(s.text())))
    expect(lines()).toEqual([
      ['Купон', '−400 ₸'],
      ['Төлейсіз', '300 ₸'],
    ])
    expect(factRows(w).flat()).not.toContain('Сапар құны')
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 300 ₸')

    booking.fare = 800
    await nextTick()
    expect(lines()).toEqual([
      ['Купон', '−400 ₸'],
      ['Төлейсіз', '400 ₸'],
    ])
    w.unmount()
  })

  it('shows no promotion for a delivery the taxi coupon and the bonus do not cover', async () => {
    vi.mocked(getPromotions).mockResolvedValue(promotionsAnswer({ bonus: 300 }))
    await readyBooking('parcel', STATION, PIN)
    const { w } = await mountAt(ConfirmSheet, '/ride/confirm')
    expect(w.find('.confirm-fare').exists()).toBe(true)
    expect(w.find('.confirm-fare__line').exists()).toBe(false)
    expect(w.find('[role="switch"]').exists()).toBe(false)
    expect(plain(w.find('.confirm-submit').text())).toBe('Тапсырыс беру · Седан · 700 ₸')
    w.unmount()
  })
})

/** A taxi quote with a sedan for 4 (700 ₸) and a minivan for 8 (1 000 ₸). */
function sedanAndMinivan(): RouteQuote {
  const expires = new Date(Date.now() + 10 * 60_000).toISOString()
  const option = (code: string, name: string, seats: number, price: number) => ({
    vehicle_class: code,
    name: { kk: name, ru: name, en: name },
    icon: code,
    seats_max: seats,
    cargo_volume_l: 400,
    quote_id: `00000000-0000-4000-8000-0000000004${String(++quoteSeq).padStart(2, '0')}`,
    expires_at: expires,
    fare: { recommended: price, min: price, max: price + 500, step: 50, options: fareRange(price, price + 500, 50) },
    commission_bps: 1000,
    eta_s: null,
  })
  const options = [option('sedan', 'Седан', 4, 700), option('minivan', 'Минивэн', 8, 1000)]
  return parseQuote({
    quote_id: options[0]!.quote_id,
    expires_at: expires,
    category: 'taxi',
    vehicle_class: 'sedan',
    passenger_count: 1,
    cargo_size: null,
    stops: [],
    distance_m: 3200,
    duration_s: 540,
    legs: [],
    geometry: null,
    bbox: null,
    fare: options[0]!.fare,
    commission_bps: 1000,
    payment_methods: ['cash', 'bank_transfer'],
    options,
  })
}

const ORDER_ID = '7d3c9a52-4f1e-4b8a-9c61-0e5b2f8a1d34'
const COUPON_PROMOTION = { kind: 'coupon', title: { kk: 'Алғашқы сапар', ru: 'Первая поездка', en: 'First ride' }, discount: 400, gross_fare: 1500, cash_due: 1100, compensation: null, settled_at: null }

/** A 1 500 ₸ taxi order with a 400 ₸ coupon, as GET /orders/:id returns it. */
function couponOrder(over: Record<string, unknown> = {}): Order {
  return parseOrder({
    id: ORDER_ID,
    number: 1024,
    status: 'searching',
    category: 'taxi',
    vehicle_class: 'sedan',
    vehicle_class_name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
    passenger_count: 1,
    fare: 1500,
    promo_discount: 400,
    cash_due: 1100,
    promotion: { ...COUPON_PROMOTION, status: 'reserved' },
    payment_method: 'cash',
    distance_m: 3200,
    duration_s: 540,
    stops: [
      { id: 's1', seq: 0, kind: 'pickup', lat: PIN.lat, lon: PIN.lon, address: PIN.address },
      { id: 's2', seq: 1, kind: 'destination', lat: STATION.lat, lon: STATION.lon, address: STATION.address },
    ],
    route: {},
    events: [],
    can_cancel: true,
    version: 1,
    created_at: new Date().toISOString(),
    ...over,
  })
}

describe('live order: promotion (§23.6)', () => {
  it('shows what the passenger pays while a driver is searched for, the fare and the coupon under it', async () => {
    shown.order = couponOrder()
    const { w } = await mountAt(OrderSheet, `/order/${ORDER_ID}`)
    const price = w.findAll('.trip-list__row').at(-1)!
    expect(plain(price.text())).toContain('1 100 ₸ · Қолма-қол')
    expect(plain(price.find('.trip-promo').text())).toBe('1 500 ₸ · Купон −400 ₸')
    w.unmount()
  })

  it('shows a completed order at what the passenger paid, the fare and the coupon under it', async () => {
    shown.order = couponOrder({ status: 'completed', can_cancel: false, completed_at: new Date().toISOString(), promotion: { ...COUPON_PROMOTION, status: 'settled', compensation: 400 } })
    const { w } = await mountAt(OrderSheet, `/order/${ORDER_ID}`)
    const fare = w.find('.trip-card__fare')
    expect(plain(fare.text())).toContain('1 100 ₸ · Қолма-қол')
    expect(plain(fare.find('.trip-promo').text())).toBe('1 500 ₸ · Купон −400 ₸')
    w.unmount()
  })

  it('shows a cancelled order at its fare, without the discount, and says the coupon came back', async () => {
    shown.order = couponOrder({
      status: 'cancelled',
      can_cancel: false,
      cancelled_by: 'passenger',
      cancelled_at: new Date().toISOString(),
      promotion: { ...COUPON_PROMOTION, status: 'released' },
    })
    const { w } = await mountAt(OrderSheet, `/order/${ORDER_ID}`)
    expect(plain(w.find('.trip-card__fare').text())).toBe('1 500 ₸ · Қолма-қол')
    expect(w.find('.trip-promo').exists()).toBe(false)
    expect(w.find('.trip-cancel').text()).toContain('Купон қайтарылды: мерзімі біткенше оны қайта пайдалана аласыз.')
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
