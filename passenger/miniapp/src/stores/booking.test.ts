import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { reverseGeocode } from '../api/geo'
import { createOrder } from '../api/orders'
import { previewRoute } from '../api/routes'
import type { Order, RouteQuote, RouteQuoteOption } from '../api/types'
import type { PlaceValue } from '../domain/route'
import { ApiError } from '../services/http'
import { PREVIEW_DEBOUNCE_MS, useBookingStore } from './booking'
import { useSessionStore } from './session'

vi.mock('../api/routes', () => ({ previewRoute: vi.fn() }))
vi.mock('../api/orders', () => ({ createOrder: vi.fn() }))
vi.mock('../api/geo', () => ({ reverseGeocode: vi.fn() }))

const preview = vi.mocked(previewRoute)
const create = vi.mocked(createOrder)

function place(name: string, lat: number, lon = 58.1): PlaceValue {
  return { lat, lon, address: name, subtitle: null, source: 'search', inServiceArea: true }
}

let quoteSeq = 0
const SEDAN_FARE = { recommended: 700, min: 700, max: 900, step: 50, options: [700, 750, 800, 850, 900] }
const MINIVAN_FARE = { recommended: 1200, min: 1200, max: 1500, step: 100, options: [1200, 1300, 1400, 1500] }

function option(vehicleClass: string, over: Partial<RouteQuoteOption> = {}): RouteQuoteOption {
  quoteSeq++
  return {
    vehicle_class: vehicleClass,
    name: null,
    examples: '',
    icon: 'sedan',
    seats_max: 4,
    cargo_volume_l: 400,
    quote_id: `00000000-0000-4000-8000-0000000001${String(quoteSeq).padStart(2, '0')}`,
    expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
    fare: SEDAN_FARE,
    commission_bps: 1000,
    eta_s: null,
    ...over,
  }
}

/** A quote whose top level repeats options[0], like the API's. */
function quote(over: Partial<RouteQuote> = {}): RouteQuote {
  quoteSeq++
  const quoteId = over.quote_id ?? `00000000-0000-4000-8000-00000000000${quoteSeq}`
  const fare = over.fare ?? SEDAN_FARE
  const options = over.options ?? [option('sedan', { quote_id: quoteId, fare })]
  return {
    quote_id: options[0]!.quote_id,
    expires_at: options[0]!.expires_at,
    category: 'taxi',
    vehicle_class: options[0]!.vehicle_class,
    passenger_count: 1,
    cargo_size: null,
    city: null,
    stops: [],
    distance_m: 3200,
    duration_s: 540,
    legs: [],
    geometry: null,
    bbox: null,
    fare: options[0]!.fare,
    commission_bps: 1000,
    payment_methods: ['cash', 'bank_transfer'],
    ...over,
    options,
  }
}

/** Sedan and minivan, like a taxi quote for up to 4 passengers. */
function twoClasses(): RouteQuote {
  return quote({ options: [option('sedan'), option('minivan', { icon: 'minivan', seats_max: 8, fare: MINIVAN_FARE, eta_s: 240 })] })
}

function order(id = 'aaaaaaaa-0000-4000-8000-000000000001'): Order {
  return { id } as Order
}

/** A complete taxi route with a ready quote. */
async function readyBooking(q = quote()) {
  const b = useBookingStore()
  preview.mockResolvedValueOnce(q)
  b.setStopPlace(0, place('Ембі, Абай 1', 48.82))
  b.setStopPlace(1, place('Ембі, вокзал', 48.84))
  await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
  expect(b.previewStatus).toBe('ready')
  return b
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  preview.mockReset()
  create.mockReset()
  vi.mocked(reverseGeocode).mockReset()
})

afterEach(() => vi.useRealTimers())

describe('booking store: route preview', () => {
  it('keeps GPS A when street lookup is unavailable and allows manual B to continue', async () => {
    const b = useBookingStore()
    preview.mockResolvedValue(quote({ distance_m: null, duration_s: null }))
    vi.mocked(reverseGeocode).mockRejectedValue(new ApiError(503, 'geocoding_unavailable', 'unavailable'))
    const loc = { lat: 48.82, lon: 58.14, accuracy_m: 12, heading: null, speed_mps: null, timestamp: Date.now(), source: 'browser' as const }
    await b.setPickupFromLocation(loc)
    b.setStopPlace(1, { lat: null, lon: null, address: 'Старый клуб', source: 'manual', subtitle: null, inServiceArea: null })
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.stops[0]?.place).toMatchObject({ lat: loc.lat, lon: loc.lon, source: 'gps' })
    expect(preview.mock.calls[0]![0].stops[1]).toMatchObject({ mode: 'manual', lat: null, lon: null })
    expect(b.canContinue).toBe(true)
  })

  it('saves the fix before leaving for B can cancel the optional street lookup', async () => {
    const b = useBookingStore()
    preview.mockResolvedValue(quote({ distance_m: null, duration_s: null }))
    let finish!: (value: Awaited<ReturnType<typeof reverseGeocode>>) => void
    vi.mocked(reverseGeocode).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const ctrl = new AbortController()
    const loc = { lat: 48.82, lon: 58.14, accuracy_m: 12, heading: null, speed_mps: null, timestamp: Date.now(), source: 'browser' as const }
    const lookup = b.setPickupFromLocation(loc, ctrl.signal)
    expect(b.stops[0]?.place).toMatchObject({ lat: loc.lat, lon: loc.lon })
    ctrl.abort()
    finish({ lat: 49, lon: 59, title: 'Different place', subtitle: null, source: 'photon', in_service_area: true })
    await lookup
    b.setStopPlace(1, { lat: null, lon: null, address: 'Ауылдағы үй', source: 'manual', subtitle: null, inServiceArea: null })
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.canContinue).toBe(true)
    expect(b.stops[0]?.place?.lat).toBe(loc.lat)
  })

  it('quotes a manual intermediate with a geolocated destination while retaining admin fare bounds', async () => {
    const b = useBookingStore()
    preview.mockResolvedValue(quote({ distance_m: null, duration_s: null }))
    b.setStopPlace(0, place('A', 48.82))
    b.setStopPlace(1, place('B', 48.84))
    b.addStop()
    b.setStopPlace(1, { lat: null, lon: null, address: 'У магазина', source: 'manual', subtitle: null, inServiceArea: null })
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.canContinue).toBe(true)
    expect(b.manualDestination).toBe(true)
    expect(b.fare).toBeNull()
    expect(b.selectedOption?.fare).toEqual(SEDAN_FARE)
    expect(preview.mock.calls[0]![0].stops.map(s => s.mode ?? 'geocoded')).toEqual(['geocoded', 'manual', 'geocoded'])
  })

  it('quotes once after the debounce and never auto-reorders the stops', async () => {
    const b = useBookingStore()
    // The API echoes stops; even a differently ordered echo must not reorder the editor.
    preview.mockResolvedValue(quote())
    b.setStopPlace(0, place('A', 48.82))
    b.addStop()
    b.setStopPlace(2, place('C', 48.86))
    b.setStopPlace(1, place('B far', 47.1))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS - 1)
    expect(preview).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(preview).toHaveBeenCalledTimes(1)
    expect(preview.mock.calls[0]![0].stops.map((s) => s.address)).toEqual(['A', 'B far', 'C'])
    expect(b.stops.map((s) => s.place?.address)).toEqual(['A', 'B far', 'C'])
    expect(b.canContinue).toBe(true)
  })

  it('an outdated preview response never replaces the newer route', async () => {
    const b = useBookingStore()
    let resolveOld!: (q: RouteQuote) => void
    const oldQuote = quote({ distance_m: 1 })
    const newQuote = quote({ distance_m: 2 })
    preview.mockImplementationOnce(() => new Promise((res) => (resolveOld = res)))
    preview.mockResolvedValueOnce(newQuote)

    b.setStopPlace(0, place('A', 48.82))
    b.setStopPlace(1, place('B', 48.84))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    const firstSignal = preview.mock.calls[0]![1]!
    b.setStopPlace(1, place('B2', 48.85))
    await nextTick()
    expect(firstSignal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    resolveOld(oldQuote)
    await vi.advanceTimersByTimeAsync(0)
    expect(b.quote?.distance_m).toBe(2)
    expect(b.quoteCurrent).toBe(true)
  })

  it('marks the stop the API reports outside Kazakhstan and blocks continuing', async () => {
    const b = useBookingStore()
    preview.mockRejectedValueOnce(new ApiError(422, 'outside_service_area', 'outside', {}, { field: 'stops', index: 1 }))
    b.setStopPlace(0, place('A', 48.82))
    b.setStopPlace(1, place('Border', 41.2, 69.3))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.stopErrors).toEqual({ 1: 'outside_service_area' })
    expect(b.blockedStops).toEqual([1])
    expect(b.canContinue).toBe(false)
  })

  it('does not request a quote while a chosen point is known to be outside Kazakhstan', async () => {
    const b = useBookingStore()
    b.setStopPlace(0, place('A', 48.82))
    b.setStopPlace(1, { ...place('Abroad', 55.7, 37.6), inServiceArea: false })
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS * 2)
    expect(preview).not.toHaveBeenCalled()
    expect(b.blockedStops).toEqual([1])
  })
})

describe('booking store: order submission', () => {
  it('reuses the same idempotency key when retrying after a lost response', async () => {
    const b = await readyBooking()
    create.mockRejectedValueOnce(new ApiError(0, 'network_error', 'offline'))
    create.mockResolvedValueOnce(order())

    expect((await b.submit()).kind).toBe('failed')
    const res = await b.submit()
    expect(res.kind).toBe('created')
    expect(create).toHaveBeenCalledTimes(2)
    const [first, second] = create.mock.calls.map((c) => c[0])
    expect(first!.idempotency_key).toMatch(/^[0-9a-f-]{36}$/)
    expect(second!.idempotency_key).toBe(first!.idempotency_key)
    expect(second).toEqual(first)
    // The draft is cleared after success.
    expect(b.stops.every((s) => s.place === null)).toBe(true)
  })

  it('sends the chosen fare, payment and category fields', async () => {
    const b = await readyBooking()
    b.fare = 800
    b.paymentMethod = 'bank_transfer'
    b.comment = '  екінші  подъезд '
    create.mockResolvedValueOnce(order())
    await b.submit()
    expect(create.mock.calls[0]![0]).toMatchObject({
      quote_id: expect.any(String),
      fare: 800,
      payment_method: 'bank_transfer',
      comment: 'екінші подъезд',
    })
    expect(create.mock.calls[0]![0]).not.toHaveProperty('recipient_phone')
  })

  it('quote_expired: re-previews silently and asks to confirm again without creating', async () => {
    const b = await readyBooking()
    const firstQuoteId = b.quote!.quote_id
    create.mockRejectedValueOnce(new ApiError(422, 'quote_expired', 'expired'))
    const fresh = quote()
    preview.mockResolvedValueOnce(fresh)

    const res = await b.submit()
    expect(res.kind).toBe('requoted')
    expect(b.reconfirm).toBe(true)
    expect(b.quote?.quote_id).toBe(fresh.quote_id)
    expect(fresh.quote_id).not.toBe(firstQuoteId)
    expect(create).toHaveBeenCalledTimes(1)

    create.mockResolvedValueOnce(order())
    expect((await b.submit()).kind).toBe('created')
    expect(create.mock.calls[1]![0].quote_id).toBe(fresh.quote_id)
  })

  it('fare_not_allowed: re-quotes and falls back to an allowed fare', async () => {
    const b = await readyBooking()
    b.fare = 700
    create.mockRejectedValueOnce(new ApiError(422, 'fare_not_allowed', 'fare', {}, { min: 800, max: 1000, step: 50 }))
    preview.mockResolvedValueOnce(quote({ fare: { recommended: 800, min: 800, max: 1000, step: 50, options: [800, 850, 900, 950, 1000] } }))
    expect((await b.submit()).kind).toBe('requoted')
    expect(b.fare).toBe(800)
  })

  it('active_order_exists hands over the existing order id', async () => {
    const b = await readyBooking()
    create.mockRejectedValueOnce(
      new ApiError(409, 'conflict', 'active', {}, { reason: 'active_order_exists', active_order_id: 'bbbbbbbb-0000-4000-8000-000000000002' }),
    )
    expect(await b.submit()).toEqual({ kind: 'active_exists', orderId: 'bbbbbbbb-0000-4000-8000-000000000002' })
  })

  it('maps API validation errors to the form fields', async () => {
    const b = await readyBooking()
    create.mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'bad', { comment: 'prohibited_content' }))
    expect((await b.submit()).kind).toBe('invalid')
    expect(b.fieldErrors).toEqual({ comment: 'prohibited_content' })
    b.comment = 'басқа'
    await nextTick()
    expect(b.fieldErrors).toEqual({})
  })

  it('validates delivery details locally before calling the API', async () => {
    const b = await readyBooking(quote({ category: 'parcel' }))
    b.setCategory('parcel')
    preview.mockResolvedValue(quote({ category: 'parcel' }))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect((await b.submit()).kind).toBe('invalid')
    expect(b.fieldErrors).toEqual({
      cargo_description: 'required',
      recipient_phone: 'required',
      prohibited_items_ack: 'must_be_true',
    })
    expect(create).not.toHaveBeenCalled()

    b.cargoDescription = 'Құжаттар'
    b.recipientPhone = '+7 701 234 56 78'
    b.prohibitedItemsAck = true
    create.mockResolvedValueOnce(order())
    expect((await b.submit()).kind).toBe('created')
    expect(create.mock.calls[0]![0]).toMatchObject({
      cargo_description: 'Құжаттар',
      recipient_phone: '+77012345678',
      prohibited_items_ack: true,
    })
  })
})

describe('booking store: vehicle class and load (§21)', () => {
  it('quotes a taxi with the passenger count and re-quotes after a change, ignoring stale answers', async () => {
    const b = await readyBooking()
    expect(preview.mock.calls[0]![0]).toMatchObject({ category: 'taxi', passenger_count: 1 })
    expect(preview.mock.calls[0]![0]).not.toHaveProperty('cargo_size')

    let resolveOld!: (q: RouteQuote) => void
    preview.mockImplementationOnce(() => new Promise((res) => (resolveOld = res)))
    b.setPassengerCount(5)
    await nextTick()
    // The old quote no longer matches; the route itself still does.
    expect(b.quoteCurrent).toBe(false)
    expect(b.routeCurrent).toBe(true)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(preview.mock.calls[1]![0]).toMatchObject({ passenger_count: 5 })

    const six = quote({ passenger_count: 6, options: [option('minivan', { fare: MINIVAN_FARE })] })
    preview.mockResolvedValueOnce(six)
    b.setPassengerCount(6)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    resolveOld(quote({ passenger_count: 5 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(b.quote).toBe(six)
    expect(b.quoteCurrent).toBe(true)
    expect(b.selectedOption?.vehicle_class).toBe('minivan')
    expect(b.fare).toBe(1200)
  })

  it('keeps the passenger count within 1 and the largest active class', async () => {
    const b = useBookingStore()
    b.setPassengerCount(40)
    expect(b.passengerCount).toBe(15)
    b.setPassengerCount(0)
    expect(b.passengerCount).toBe(1)
    useSessionStore().config.vehicle_classes = [
      { code: 'sedan', name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' }, examples: '', icon: 'sedan', seats_default: 4, seats_max: 4, cargo_volume_l_default: 400, passenger_capable: true, cargo_capable: true },
      { code: 'minivan', name: { kk: 'Минивэн', ru: 'Минивэн', en: 'Minivan' }, examples: '', icon: 'minivan', seats_default: 7, seats_max: 8, cargo_volume_l_default: 800, passenger_capable: true, cargo_capable: true },
      { code: 'truck', name: { kk: 'Жүк көлігі', ru: 'Грузовик', en: 'Truck' }, examples: '', icon: 'truck', seats_default: 2, seats_max: 20, cargo_volume_l_default: 15000, passenger_capable: false, cargo_capable: true },
    ]
    expect(b.passengerLimit).toBe(8)
    b.setPassengerCount(12)
    expect(b.passengerCount).toBe(8)
  })

  it('quotes deliveries with the cargo size instead of passengers', async () => {
    const b = await readyBooking()
    preview.mockResolvedValue(quote({ category: 'parcel', passenger_count: null, cargo_size: 's' }))
    b.setCategory('parcel')
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(preview.mock.lastCall![0]).toMatchObject({ category: 'parcel', cargo_size: 's' })
    expect(preview.mock.lastCall![0]).not.toHaveProperty('passenger_count')
    b.setCargoSize('xl')
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(preview.mock.lastCall![0]).toMatchObject({ category: 'parcel', cargo_size: 'xl' })
    // The passenger count of taxi does not re-quote a delivery.
    const calls = preview.mock.calls.length
    b.setPassengerCount(3)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(preview.mock.calls.length).toBe(calls)
  })

  it("orders with the chosen class's quote and restarts the fare at its recommendation", async () => {
    const q = twoClasses()
    const b = await readyBooking(q)
    expect(b.selectedOption?.vehicle_class).toBe('sedan')
    b.fare = 850
    b.setVehicleClass('minivan')
    expect(b.selectedOption?.vehicle_class).toBe('minivan')
    expect(b.fare).toBe(1200)
    b.fare = 1400
    // Not offered: ignored.
    b.setVehicleClass('truck')
    expect(b.vehicleClass).toBe('minivan')

    create.mockResolvedValueOnce(order())
    expect((await b.submit()).kind).toBe('created')
    expect(create.mock.calls[0]![0]).toMatchObject({ quote_id: q.options[1]!.quote_id, fare: 1400 })
  })

  it('validates the fare against the chosen class', async () => {
    const b = await readyBooking(twoClasses())
    b.setVehicleClass('minivan')
    b.fare = 800
    expect(b.validate()).toBe(false)
    expect(b.fieldErrors.fare).toBe('invalid')
    expect(create).not.toHaveBeenCalled()
  })

  it('keeps the chosen class as a preference across re-quotes', async () => {
    const b = await readyBooking(twoClasses())
    b.setVehicleClass('minivan')
    b.fare = 1300

    // 9 passengers: only a minibus can go; the minivan comes back for 6.
    preview.mockResolvedValueOnce(quote({ options: [option('microbus', { fare: { recommended: 2000, min: 2000, max: 3000, step: 500, options: [2000, 2500, 3000] } })] }))
    b.setPassengerCount(9)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.selectedOption?.vehicle_class).toBe('microbus')
    expect(b.fare).toBe(2000)
    expect(b.vehicleClass).toBe('minivan')

    preview.mockResolvedValueOnce(twoClasses())
    b.setPassengerCount(6)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.selectedOption?.vehicle_class).toBe('minivan')
    expect(b.fare).toBe(1200)

    // Same class after a re-quote: the passenger's fare stays when still allowed.
    b.fare = 1400
    preview.mockResolvedValueOnce(twoClasses())
    b.setPassengerCount(7)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.fare).toBe(1400)
  })

  // Order #1034: a class picked for an earlier route must not come back for a new one.
  it('starts every new route with the default class, never an earlier pick', async () => {
    const b = await readyBooking(twoClasses())
    b.setVehicleClass('minivan')
    expect(b.selectedOption?.vehicle_class).toBe('minivan')

    // A new destination is a new booking: the cheapest class again.
    preview.mockResolvedValueOnce(twoClasses())
    b.setStopPlace(1, place('Ембі, мектеп', 48.86))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.vehicleClass).toBeNull()
    expect(b.selectedOption?.vehicle_class).toBe('sedan')
    expect(b.fare).toBe(700)

    // Going back to the first route does not restore the old pick either.
    preview.mockResolvedValueOnce(twoClasses())
    b.setStopPlace(1, place('Ембі, вокзал', 48.84))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.selectedOption?.vehicle_class).toBe('sedan')
  })

  it('forgets the class when a new booking starts on the home screen', async () => {
    const b = await readyBooking(twoClasses())
    b.setVehicleClass('minivan')
    b.startNewBooking()
    expect(b.vehicleClass).toBeNull()
    expect(b.selectedOption?.vehicle_class).toBe('sedan')
    expect(b.fare).toBe(700)
    // The route itself stays (the pickup and the last destination are reused).
    expect(b.stops.map((s) => s.place?.address)).toEqual(['Ембі, Абай 1', 'Ембі, вокзал'])
  })

  // Order #1034, replayed: one passenger, the preview offered sedan, estate,
  // minivan and minibus (sedan first), and the order went out as a minibus
  // because a pick from an earlier, abandoned booking was still in the store.
  it('order #1034: a new taxi booking for one passenger is ordered as the first option', async () => {
    const MICROBUS_FARE = { recommended: 2150, min: 2150, max: 6000, step: 50, options: [2150, 2200, 2250] }
    const fourClasses = (): RouteQuote =>
      quote({
        options: [
          option('sedan'),
          option('universal', { fare: { recommended: 750, min: 750, max: 1800, step: 50, options: [750, 800] } }),
          option('minivan', { icon: 'minivan', seats_max: 8, fare: MINIVAN_FARE }),
          option('microbus', { icon: 'microbus', seats_max: 20, fare: MICROBUS_FARE }),
        ],
      })

    // An earlier booking: the minibus row was selected, nothing was ordered.
    const b = await readyBooking(fourClasses())
    b.setVehicleClass('microbus')
    expect(b.fare).toBe(2150)

    // Back on the home screen, a new booking to another address.
    b.startNewBooking()
    const q = fourClasses()
    preview.mockResolvedValueOnce(q)
    b.setStopPlace(1, place('Алматы, Абай даңғылы 10', 43.24, 76.89))
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.passengerCount).toBe(1)
    expect(b.selectedOption?.vehicle_class).toBe('sedan')
    expect(b.fare).toBe(700)

    create.mockResolvedValueOnce(order())
    expect((await b.submit()).kind).toBe('created')
    expect(create.mock.calls[0]![0]).toMatchObject({ quote_id: q.options[0]!.quote_id, fare: 700 })
  })

  it('defaults to the cheapest option even when the API lists another first', async () => {
    const q = quote({ options: [option('minivan', { fare: MINIVAN_FARE, seats_max: 8 }), option('sedan')] })
    const b = await readyBooking(q)
    expect(b.selectedOption?.vehicle_class).toBe('sedan')
    expect(b.fare).toBe(700)
  })

  it('reports a load the API cannot carry as a preview error', async () => {
    const b = await readyBooking()
    preview.mockRejectedValueOnce(new ApiError(422, 'validation_failed', 'bad', { passenger_count: 'too_large' }))
    b.setPassengerCount(15)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.previewStatus).toBe('error')
    expect(b.stopErrors).toEqual({})
    // The load is changed on the confirmation screen, so the route editor lets the passenger go on.
    expect(b.loadBlocked).toBe(true)
    expect(b.canContinue).toBe(true)
    // Ordering still needs a quote that carries the load.
    expect((await b.submit()).kind).toBe('route_invalid')
    expect(create).not.toHaveBeenCalled()

    // Other preview errors still stop the passenger in the route editor.
    preview.mockRejectedValueOnce(new ApiError(503, 'route_unavailable', 'down'))
    b.setPassengerCount(2)
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    expect(b.loadBlocked).toBe(false)
    expect(b.canContinue).toBe(false)
  })

  it('resets the load and class after an order and restores them for "order again"', async () => {
    const b = await readyBooking(twoClasses())
    b.setPassengerCount(3)
    b.setVehicleClass('minivan')
    preview.mockResolvedValueOnce(twoClasses())
    await vi.advanceTimersByTimeAsync(PREVIEW_DEBOUNCE_MS)
    create.mockResolvedValueOnce(order())
    expect((await b.submit()).kind).toBe('created')
    expect(b.passengerCount).toBe(1)
    expect(b.cargoSize).toBe('s')
    expect(b.vehicleClass).toBeNull()

    b.prefill('parcel', [place('A', 48.82), place('B', 48.84)], { vehicleClass: 'cargo_van', passengerCount: null, cargoSize: 'l' })
    expect(b.category).toBe('parcel')
    expect(b.cargoSize).toBe('l')
    expect(b.vehicleClass).toBe('cargo_van')
    expect(b.passengerCount).toBe(1)
  })
})
