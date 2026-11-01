import { describe, expect, it } from 'vitest'
import { parseOrder, parseOrderSummary } from '../api/parse'
import type { OrderSummary } from '../api/types'
import { placeKey, placesOfStops, recentDestinations, recentPlaces } from './places'

const TYPED = 'Мешіттің жанында, көк қақпа'

function trip(id: string, pickup: Record<string, unknown>, destination: Record<string, unknown>): OrderSummary {
  return parseOrderSummary({ id, status: 'completed', category: 'taxi', pickup: { seq: 0, kind: 'pickup', ...pickup }, destination: { seq: 1, kind: 'destination', ...destination } })!
}

const manual = (address: string) => ({ mode: 'manual', lat: null, lon: null, address })

describe('places from the order history', () => {
  it('brings a pickup the passenger wrote back typed, so editing it starts from that text', () => {
    const o = parseOrder({
      id: 'o1',
      status: 'completed',
      stops: [
        { id: 'a', seq: 0, kind: 'pickup', lat: 48.82, lon: 58.14, address: TYPED, address_typed: true },
        { id: 'b', seq: 1, kind: 'destination', ...manual('Ескі мәдениет үйі') },
      ],
    })
    const [a, b] = placesOfStops(o.stops)
    expect(a).toEqual({ lat: 48.82, lon: 58.14, address: TYPED, subtitle: null, source: 'order', inServiceArea: true, typed: true })
    expect(b).toEqual({ lat: null, lon: null, address: 'Ескі мәдениет үйі', subtitle: null, source: 'manual', inServiceArea: null })

    const history = [trip('o2', { lat: 48.82, lon: 58.14, address: TYPED, address_typed: true }, { lat: 48.9, lon: 58.2, address: 'Вокзал' })]
    expect(recentPlaces(history)).toEqual([
      { lat: 48.9, lon: 58.2, address: 'Вокзал', subtitle: null, source: 'history', inServiceArea: true },
      { lat: 48.82, lon: 58.14, address: TYPED, subtitle: null, source: 'history', inServiceArea: true, typed: true },
    ])
  })

  it('offers manual places only while manual stops are switched on', () => {
    const history = [
      trip('o3', { lat: 48.82, lon: 58.14, address: 'Абай көшесі, 5' }, manual('Ескі мәдениет үйі')),
      trip('o4', { lat: 48.83, lon: 58.15, address: 'Мектеп' }, manual('Ауылдағы үй')),
      trip('o5', { lat: 48.84, lon: 58.16, address: 'Базар' }, { lat: 48.9, lon: 58.2, address: 'Вокзал' }),
    ]
    expect(recentDestinations(history, 2).map((p) => p.address)).toEqual(['Ескі мәдениет үйі', 'Ауылдағы үй'])
    expect(recentDestinations(history, 2, false).map((p) => p.address)).toEqual(['Вокзал'])
    expect(recentPlaces(history).map((p) => p.source)).toContain('manual')
    expect(recentPlaces(history, 6, false).some((p) => p.source === 'manual')).toBe(false)
  })

  it('gives every place of a list its own key, also manual places without coordinates', () => {
    const history = [trip('o6', { lat: 48.82, lon: 58.14, address: 'A' }, manual('Ескі мәдениет үйі')), trip('o7', { lat: 48.83, lon: 58.15, address: 'B' }, manual('Ауылдағы үй'))]
    const keys = recentPlaces(history).map(placeKey)
    expect(keys).toHaveLength(4)
    expect(new Set(keys).size).toBe(4)
  })
})
