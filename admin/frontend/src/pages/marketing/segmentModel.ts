// Segment filters as typed form fields and back (same ranges as
// marketing/segments.go). Fields that belong to the other audience are never
// sent, so the API's driver_only_filter / passenger_only_filter cannot occur.

import type { Language } from '../../api/common'
import { SEGMENT_DRIVER_STATUSES, type Audience, type SegmentFilters } from '../../api/marketing'
import { parseInteger } from '../../forms/numbers'
import type { FieldErrors } from '../../forms/validation'
import type { Translate } from './campaignModel'

export const FILTER_LIMITS = { days: 3650, rides: 100_000, cities: 50 } as const

const LANGUAGES: readonly Language[] = ['kk', 'ru', 'en']

export interface FiltersForm {
  languages: Language[]
  registered_within_days: string
  registered_before_days: string
  completed_rides_min: string
  completed_rides_max: string
  /** Passengers only. */
  last_ride_within_days: string
  no_ride_for_days: string
  /** '' = any, 'yes' / 'no'. */
  has_active_coupon: '' | 'yes' | 'no'
  /** Drivers only (approved when none is chosen). */
  driver_statuses: string[]
  online_within_days: string
  city_ids: string[]
}

export function emptyFiltersForm(): FiltersForm {
  return {
    languages: [],
    registered_within_days: '',
    registered_before_days: '',
    completed_rides_min: '',
    completed_rides_max: '',
    last_ride_within_days: '',
    no_ride_for_days: '',
    has_active_coupon: '',
    driver_statuses: [],
    online_within_days: '',
    city_ids: [],
  }
}

export function filtersToForm(f: SegmentFilters | null | undefined): FiltersForm {
  const s = (v: number | undefined): string => (typeof v === 'number' ? String(v) : '')
  const x = f ?? {}
  return {
    languages: (x.languages ?? []).filter((l): l is Language => LANGUAGES.includes(l)),
    registered_within_days: s(x.registered_within_days),
    registered_before_days: s(x.registered_before_days),
    completed_rides_min: s(x.completed_rides_min),
    completed_rides_max: s(x.completed_rides_max),
    last_ride_within_days: s(x.last_ride_within_days),
    no_ride_for_days: s(x.no_ride_for_days),
    has_active_coupon: x.has_active_coupon === true ? 'yes' : x.has_active_coupon === false ? 'no' : '',
    driver_statuses: [...(x.driver_statuses ?? [])],
    online_within_days: s(x.online_within_days),
    city_ids: [...(x.city_ids ?? [])],
  }
}

type NumberKey =
  | 'registered_within_days'
  | 'registered_before_days'
  | 'completed_rides_min'
  | 'completed_rides_max'
  | 'last_ride_within_days'
  | 'no_ride_for_days'
  | 'online_within_days'

function optionalInt(errors: FieldErrors, out: SegmentFilters, form: FiltersForm, key: NumberKey, max: number): void {
  const raw = form[key].trim()
  if (raw === '') return
  const n = parseInteger(raw)
  if (n === null) errors[`filters.${key}`] = 'integer'
  else if (n < 0 || n > max) errors[`filters.${key}`] = 'out_of_range'
  else out[key] = n
}

/** Filters for the API ("filters.<field>" errors); empty fields are left out. */
export function formToFilters(audience: Audience, form: FiltersForm): { errors: FieldErrors; filters: SegmentFilters | null } {
  const errors: FieldErrors = {}
  const out: SegmentFilters = {}
  const langs = LANGUAGES.filter((l) => form.languages.includes(l))
  if (langs.length) out.languages = langs
  optionalInt(errors, out, form, 'registered_within_days', FILTER_LIMITS.days)
  optionalInt(errors, out, form, 'registered_before_days', FILTER_LIMITS.days)
  optionalInt(errors, out, form, 'completed_rides_min', FILTER_LIMITS.rides)
  optionalInt(errors, out, form, 'completed_rides_max', FILTER_LIMITS.rides)
  if (out.completed_rides_min !== undefined && out.completed_rides_max !== undefined && out.completed_rides_max < out.completed_rides_min) {
    errors['filters.completed_rides_max'] = 'below_min'
  }
  if (audience === 'passengers') {
    optionalInt(errors, out, form, 'last_ride_within_days', FILTER_LIMITS.days)
    optionalInt(errors, out, form, 'no_ride_for_days', FILTER_LIMITS.days)
    if (form.has_active_coupon) out.has_active_coupon = form.has_active_coupon === 'yes'
  } else {
    const statuses = SEGMENT_DRIVER_STATUSES.filter((s) => form.driver_statuses.includes(s))
    if (statuses.length) out.driver_statuses = statuses
    optionalInt(errors, out, form, 'online_within_days', FILTER_LIMITS.days)
    if (form.city_ids.length > FILTER_LIMITS.cities) errors['filters.city_ids'] = 'too_many'
    else if (form.city_ids.length) out.city_ids = [...form.city_ids]
  }
  return { errors, filters: Object.keys(errors).length ? null : out }
}

/** Plain-language list of the conditions ("everyone who started the bot" when none). */
export function filtersSummary(audience: Audience, f: SegmentFilters | null | undefined, t: Translate, cityName: (id: string) => string): string {
  const x = f ?? {}
  const parts: string[] = []
  if (x.languages?.length) parts.push(t('marketing.f_sum_languages', { list: x.languages.map((l) => t(`languages.${l}`)).join(', ') }))
  if (x.registered_within_days !== undefined) parts.push(t('marketing.f_sum_registered_within', { n: x.registered_within_days }))
  if (x.registered_before_days !== undefined) parts.push(t('marketing.f_sum_registered_before', { n: x.registered_before_days }))
  if (x.completed_rides_min !== undefined) parts.push(t('marketing.f_sum_rides_min', { n: x.completed_rides_min }))
  if (x.completed_rides_max !== undefined) parts.push(t('marketing.f_sum_rides_max', { n: x.completed_rides_max }))
  if (x.last_ride_within_days !== undefined) parts.push(t('marketing.f_sum_last_ride', { n: x.last_ride_within_days }))
  if (x.no_ride_for_days !== undefined) parts.push(t('marketing.f_sum_no_ride', { n: x.no_ride_for_days }))
  if (x.has_active_coupon !== undefined) parts.push(t(x.has_active_coupon ? 'marketing.f_sum_with_coupon' : 'marketing.f_sum_without_coupon'))
  if (audience === 'drivers') {
    const statuses = x.driver_statuses?.length ? x.driver_statuses : ['approved']
    parts.push(statuses.map((s) => t(`moderation_status.${s}`)).join(', '))
  }
  if (x.online_within_days !== undefined) parts.push(t('marketing.f_sum_online', { n: x.online_within_days }))
  if (x.city_ids?.length) parts.push(x.city_ids.map(cityName).join(', '))
  return parts.length ? parts.join(' · ') : t('marketing.f_sum_everyone')
}
