import { describe, expect, it } from 'vitest'
import type { RouteQuote, RouteQuoteOption } from '../api/types'
import { defaultPublicConfig, type VehicleClassInfo } from '../services/config'
import { ApiError } from '../services/http'
import { cargoVolume, clampPassengers, defaultOption, etaMinutes, isCargoSize, loadProblem, maxPassengers, oversizedForParty, pickOption } from './vehicleClass'

function cls(code: string, seatsMax: number, passengers = true): VehicleClassInfo {
  return {
    code,
    name: { kk: code, ru: code, en: code },
    examples: '',
    icon: 'sedan',
    seats_default: seatsMax,
    seats_max: seatsMax,
    cargo_volume_l_default: 400,
    passenger_capable: passengers,
    cargo_capable: true,
  }
}

function opt(vehicleClass: string, recommended = 700, seatsMax = 4): RouteQuoteOption {
  return {
    vehicle_class: vehicleClass,
    name: null,
    examples: '',
    icon: 'sedan',
    seats_max: seatsMax,
    cargo_volume_l: 400,
    quote_id: `q-${vehicleClass}`,
    expires_at: '',
    fare: { recommended, min: recommended, max: recommended + 200, step: 100, options: [recommended, recommended + 100, recommended + 200] },
    commission_bps: 1000,
    eta_s: null,
  }
}

describe('vehicle class rules', () => {
  it('limits passengers by the API maximum and the largest passenger class', () => {
    const config = defaultPublicConfig()
    expect(maxPassengers(config)).toBe(15)
    config.vehicle_classes = [cls('sedan', 4), cls('minivan', 8), cls('truck', 30, false)]
    expect(maxPassengers(config)).toBe(8)
    config.vehicle_classes.push(cls('microbus', 20))
    expect(maxPassengers(config)).toBe(15)
  })

  it('clamps the passenger count', () => {
    expect(clampPassengers(0, 8)).toBe(1)
    expect(clampPassengers(3.6, 8)).toBe(4)
    expect(clampPassengers(99, 8)).toBe(8)
    expect(clampPassengers(Number.NaN, 8)).toBe(1)
  })

  it('knows the cargo sizes and their volumes', () => {
    expect(isCargoSize('xl')).toBe(true)
    expect(isCargoSize('xxl')).toBe(false)
    expect(cargoVolume('m')).toBe(60)
    expect(cargoVolume('m', [{ code: 'm', volume_l: 80 }])).toBe(80)
    expect(cargoVolume('xl', [{ code: 'm', volume_l: 80 }])).toBe(1500)
  })

  it('picks the chosen option or the default one', () => {
    const q = { options: [opt('sedan'), opt('minivan', 1000, 8)] } as RouteQuote
    expect(pickOption(q, 'minivan')?.vehicle_class).toBe('minivan')
    expect(pickOption(q, 'truck')?.vehicle_class).toBe('sedan')
    expect(pickOption(q, null)?.vehicle_class).toBe('sedan')
    expect(pickOption(null, 'sedan')).toBeNull()
  })

  it('starts with the cheapest class, the first one on a tie', () => {
    const sorted = { options: [opt('sedan', 500), opt('universal', 600), opt('minivan', 1000, 8), opt('microbus', 2000, 20)] } as RouteQuote
    expect(defaultOption(sorted)?.vehicle_class).toBe('sedan')
    const unsorted = { options: [opt('microbus', 2000, 20), opt('universal', 600), opt('sedan', 600)] } as RouteQuote
    expect(defaultOption(unsorted)?.vehicle_class).toBe('universal')
    expect(pickOption(unsorted, null)?.vehicle_class).toBe('universal')
    expect(defaultOption({ options: [] as RouteQuoteOption[] } as RouteQuote)).toBeNull()
  })

  it('asks before a big vehicle for a small taxi party', () => {
    expect(oversizedForParty('taxi', 1, opt('microbus', 2000, 20))).toBe(true)
    expect(oversizedForParty('taxi', 4, opt('minivan', 1000, 7))).toBe(true)
    expect(oversizedForParty('taxi', 5, opt('minivan', 1000, 8))).toBe(false)
    expect(oversizedForParty('taxi', 1, opt('universal', 600, 4))).toBe(false)
    expect(oversizedForParty('parcel', 1, opt('microbus', 2000, 20))).toBe(false)
    expect(oversizedForParty('taxi', 1, null)).toBe(false)
  })

  it('rounds an ETA up to whole minutes, at least one', () => {
    expect(etaMinutes(0)).toBe(1)
    expect(etaMinutes(61)).toBe(2)
    expect(etaMinutes(240)).toBe(4)
    expect(etaMinutes(null)).toBeNull()
    expect(etaMinutes(-5)).toBeNull()
  })

  it('tells load problems apart from other preview errors', () => {
    expect(loadProblem(new ApiError(422, 'validation_failed', 'x', { passenger_count: 'too_large' }))).toBe('passengers')
    expect(loadProblem(new ApiError(422, 'validation_failed', 'x', { cargo_size: 'too_large' }))).toBe('cargo')
    expect(loadProblem(new ApiError(422, 'validation_failed', 'x', { category: 'not_allowed' }))).toBe('unavailable')
    expect(loadProblem(new ApiError(422, 'validation_failed', 'x', { 'stops.1': 'duplicate' }))).toBeNull()
    expect(loadProblem(new ApiError(503, 'route_unavailable', 'x'))).toBeNull()
    expect(loadProblem(new Error('x'))).toBeNull()
  })
})
