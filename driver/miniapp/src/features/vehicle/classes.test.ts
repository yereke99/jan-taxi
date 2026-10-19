import { describe, expect, it } from 'vitest'
import { findClass, fixedCommission, normalizeDriverExtras, normalizeVehicleClasses } from './classes'

const SEDAN = {
  code: 'sedan',
  name: { kk: 'Седан', ru: 'Седан', en: 'Sedan' },
  examples: 'Toyota Camry',
  icon: 'sedan',
  seats_default: 4,
  seats_max: 4,
  cargo_volume_l_default: 400,
  passenger_capable: true,
  cargo_capable: true,
}

describe('vehicle classes from the public config', () => {
  it('keeps valid classes in the server order', () => {
    const truck = { ...SEDAN, code: 'truck', icon: 'truck', seats_default: 2, seats_max: 2, cargo_volume_l_default: 15000, passenger_capable: false }
    const list = normalizeVehicleClasses([SEDAN, truck])
    expect(list.map((c) => c.code)).toEqual(['sedan', 'truck'])
    expect(list[1]).toMatchObject({ passenger_capable: false, cargo_capable: true, cargo_volume_l_default: 15000 })
  })

  it('drops malformed and duplicate classes and repairs odd values', () => {
    const list = normalizeVehicleClasses([
      SEDAN,
      { ...SEDAN }, // duplicate
      { ...SEDAN, code: 'Bad Code' },
      { ...SEDAN, code: 'nomax', seats_max: 0 },
      { ...SEDAN, code: 'cargo_van', icon: 'spaceship', seats_default: 9, seats_max: 2, name: null },
    ])
    expect(list.map((c) => c.code)).toEqual(['sedan', 'cargo_van'])
    expect(list[1]).toMatchObject({ icon: 'cargo_van', seats_default: 2, name: { kk: 'cargo_van', ru: 'cargo_van', en: 'cargo_van' } })
    expect(normalizeVehicleClasses(undefined)).toEqual([])
  })

  it('reads the commission policy and the welcome bonus (driver app)', () => {
    const x = normalizeDriverExtras({ vehicle_classes: [SEDAN], commission: { mode: 'fixed', fixed_amount: 50 }, welcome_bonus: 500 })
    expect(x.commission).toEqual({ mode: 'fixed', fixed_amount: 50 })
    expect(x.welcomeBonus).toBe(500)
    expect(fixedCommission(x.commission)).toBe(50)
    expect(fixedCommission({ mode: 'percent', fixed_amount: 50 })).toBeNull()
    expect(normalizeDriverExtras({ welcome_bonus: 0 }).welcomeBonus).toBe(0)
    expect(normalizeDriverExtras({})).toEqual({ vehicleClasses: [], commission: null, welcomeBonus: null })
    expect(findClass(x.vehicleClasses, 'sedan')?.examples).toBe('Toyota Camry')
    expect(findClass(x.vehicleClasses, 'minivan')).toBeNull()
  })
})
