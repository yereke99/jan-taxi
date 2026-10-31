// Booking sheets on a stand-in map stage: pickup A written by hand in the
// place search (it keeps A's point on the map), and the confirmation sheet's
// fare section, which only deliveries have (a taxi goes at the chosen class's
// price shown on its row and on the button).

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, reactive, ref, shallowRef, type Component } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { parseQuote } from '../../api/parse'
import { previewRoute } from '../../api/routes'
import type { RouteQuote } from '../../api/types'
import type { PlaceValue } from '../../domain/route'
import { i18n } from '../../i18n'
import { ROUTE } from '../../router/names'
import { STAGE_KEY, type MapStage, type StageControls } from '../../stage/mapStage'
import { PREVIEW_DEBOUNCE_MS, useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import type { Category } from '../../utils/status'
import ConfirmSheet from './ConfirmSheet.vue'
import PlaceSearchSheet from './PlaceSearchSheet.vue'

vi.mock('../../api/routes', () => ({ previewRoute: vi.fn() }))
vi.mock('../../api/geo', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/geo')>()),
  searchPlaces: vi.fn().mockResolvedValue([]),
  reverseGeocode: vi.fn(),
}))

const preview = vi.mocked(previewRoute)
/** Money and the submit label use no-break spaces. */
const plain = (s: string) => s.replace(/ /g, ' ')

const TYPED = 'Мешіттің жанында, көк қақпа'
const PIN: PlaceValue = { lat: 48.8264, lon: 58.1442, address: 'Абай көшесі, 5', subtitle: 'Ембі', source: 'map', inServiceArea: true }
const STATION: PlaceValue = { lat: 48.8291, lon: 58.1504, address: 'Теміржол вокзалы', subtitle: null, source: 'search', inServiceArea: true }
const MANUAL_B: PlaceValue = { lat: null, lon: null, address: 'Ескі мәдениет үйі', subtitle: null, source: 'manual', inServiceArea: null }

function fakeStage(): MapStage {
  const zero = { top: 0, right: 0, bottom: 0, left: 0 }
  return {
    map: shallowRef(null),
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
    center: () => null,
    onMoveStart: () => () => undefined,
    onMoveEnd: () => () => undefined,
    claim: () => Symbol('screen'),
    release: vi.fn(),
  }
}

const Blank = { template: '<div />' }
const SheetStub = { template: '<section><slot name="header" /><slot /><footer><slot name="footer" /></footer></section>' }

async function mountAt(component: Component, path: string): Promise<{ w: ReturnType<typeof mount>; router: Router }> {
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
      provide: { [STAGE_KEY]: fakeStage() },
      stubs: { JSheet: SheetStub, PhoneShareSheet: true },
    },
  })
  await flushPromises()
  return { w, router }
}

beforeEach(() => {
  setActivePinia(createPinia())
  preview.mockReset()
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

  it('has no manual entry for A without any point to keep', async () => {
    const { w } = await mountAt(PlaceSearchSheet, '/ride/search/0')
    expect(w.find('.search-row--manual').exists()).toBe(false)
    expect(w.text()).toContain(i18n.global.t('search.use_my_location'))
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
async function readyBooking(category: Category, destination: PlaceValue): Promise<ReturnType<typeof useBookingStore>> {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  const booking = useBookingStore()
  booking.setCategory(category)
  preview.mockResolvedValue(sedanQuote(category, destination.source === 'manual'))
  booking.setStopPlace(0, { ...PIN, address: TYPED, typed: true })
  booking.setStopPlace(1, destination)
  await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
  expect(booking.quoteCurrent).toBe(true)
  return booking
}

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
