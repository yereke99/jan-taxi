// Segment conditions: only the audience's own fields are sent, ranges match
// the API, and the plain-language summary.

import { afterEach, describe, expect, it } from 'vitest'
import { i18n } from '../../i18n'
import { emptyFiltersForm, filtersSummary, filtersToForm, formToFilters } from './segmentModel'

afterEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('formToFilters', () => {
  it('sends nothing for an empty form', () => {
    expect(formToFilters('passengers', emptyFiltersForm())).toEqual({ errors: {}, filters: {} })
  })

  it('keeps passenger-only and driver-only fields to their audience', () => {
    const f = { ...emptyFiltersForm(), languages: ['ru' as const, 'kk' as const], no_ride_for_days: '30', has_active_coupon: 'no' as const, driver_statuses: ['approved'], online_within_days: '7', city_ids: ['c1'] }
    expect(formToFilters('passengers', f).filters).toEqual({ languages: ['kk', 'ru'], no_ride_for_days: 30, has_active_coupon: false })
    expect(formToFilters('drivers', f).filters).toEqual({ languages: ['kk', 'ru'], driver_statuses: ['approved'], online_within_days: 7, city_ids: ['c1'] })
  })

  it('checks the ranges like the API', () => {
    const f = { ...emptyFiltersForm(), registered_within_days: '4000', completed_rides_min: '3', completed_rides_max: '1', last_ride_within_days: 'x' }
    expect(formToFilters('passengers', f)).toEqual({
      errors: { 'filters.registered_within_days': 'out_of_range', 'filters.completed_rides_max': 'below_min', 'filters.last_ride_within_days': 'integer' },
      filters: null,
    })
  })

  it('reads stored filters back into the form', () => {
    expect(filtersToForm({ has_active_coupon: true, completed_rides_min: 0 })).toMatchObject({ has_active_coupon: 'yes', completed_rides_min: '0', languages: [] })
  })
})

describe('filtersSummary', () => {
  it('lists the conditions in plain words', () => {
    i18n.global.locale.value = 'en'
    const t = i18n.global.t
    expect(filtersSummary('passengers', {}, t, (id) => id)).toBe('Everyone who started the bot')
    expect(filtersSummary('passengers', { languages: ['kk'], completed_rides_min: 1, has_active_coupon: false }, t, (id) => id)).toBe(
      'language: Қазақша · rides ≥ 1 · no active coupon',
    )
    expect(filtersSummary('drivers', { online_within_days: 3, city_ids: ['c1'] }, t, () => 'Embi')).toBe('Approved · online in the last 3 days · Embi')
  })
})
