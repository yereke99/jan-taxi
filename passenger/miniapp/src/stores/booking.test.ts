import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { createOrder } from '../api/orders'
import { previewRoute } from '../api/routes'
import type { Order, RouteQuote } from '../api/types'
import type { PlaceValue } from '../domain/route'
import { ApiError } from '../services/http'
import { PREVIEW_DEBOUNCE_MS, useBookingStore } from './booking'

vi.mock('../api/routes', () => ({ previewRoute: vi.fn() }))
vi.mock('../api/orders', () => ({ createOrder: vi.fn() }))
vi.mock('../api/geo', () => ({ reverseGeocode: vi.fn() }))

const preview = vi.mocked(previewRoute)
const create = vi.mocked(createOrder)

function place(name: string, lat: number, lon = 58.1): PlaceValue {
  return { lat, lon, address: name, subtitle: null, source: 'search', inServiceArea: true }
}

let quoteSeq = 0
function quote(over: Partial<RouteQuote> = {}): RouteQuote {
  quoteSeq++
  return {
    quote_id: `00000000-0000-4000-8000-00000000000${quoteSeq}`,
    expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
    category: 'taxi',
    city: null,
    stops: [],
    distance_m: 3200,
    duration_s: 540,
    legs: [],
    geometry: null,
    bbox: null,
    fare: { recommended: 700, min: 700, max: 900, step: 50, options: [700, 750, 800, 850, 900] },
    commission_bps: 1000,
    payment_methods: ['cash', 'bank_transfer'],
    ...over,
  }
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
})

afterEach(() => vi.useRealTimers())

describe('booking store: route preview', () => {
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
