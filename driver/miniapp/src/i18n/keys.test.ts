// Every message key the driver app uses exists in kk, ru and en: static
// t('…') calls found in the sources, plus each value of the enums that pick a
// key dynamically.

import { describe, expect, it } from 'vitest'
import { CORRECTION_REASONS, REGISTRATION_FIELDS } from '../api/types'
import { CANCEL_REASONS } from '../features/trip/labels'
import { CONSENT_SECTIONS } from '../features/registration/consent'
import { STEP_IDS } from '../features/registration/form'
import { RECEIPT_REASONS } from '../features/topup/receipt'
import { CATEGORIES, MODERATION_STATUSES, ORDER_STATUSES, PAYMENT_METHODS, TOPUP_STATUSES } from '../utils/status'
import { i18n } from './index'
import { LOCALES } from './core'

// Raw sources of the app (Vite glob import), without messages and tests.
const SOURCES = import.meta.glob<string>(['../**/*.vue', '../**/*.ts', '!../i18n/**', '!../**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
})

function staticKeys(): string[] {
  const keys = new Set<string>()
  const re = /\bte?\(\s*'([a-z0-9_]+(?:\.[a-z0-9_]+)+)'/g
  for (const text of Object.values(SOURCES)) {
    for (const m of text.matchAll(re)) keys.add(m[1]!)
  }
  return [...keys].sort()
}

function missing(keys: readonly string[]): string[] {
  const out: string[] = []
  for (const locale of LOCALES) for (const k of keys) if (!i18n.global.te(k, locale)) out.push(`${locale}:${k}`)
  return out
}

describe('i18n keys', () => {
  it('finds the static keys used by the screens', () => {
    expect(staticKeys().length).toBeGreaterThan(150)
  })

  it('every static key exists in kk, ru and en', () => {
    expect(missing(staticKeys())).toEqual([])
  })

  it('every dynamically chosen key exists in kk, ru and en', () => {
    const keys = [
      ...STEP_IDS.map((s) => `registration.steps.${s}`),
      ...REGISTRATION_FIELDS.map((f) => `registration.field.${f}`),
      ...CORRECTION_REASONS.map((r) => `registration.reason.${r}`),
      ...['required', 'too_long', 'invalid'].map((e) => `registration.form_errors.${e}`),
      ...['unsupported', 'too_large'].map((e) => `registration.upload.${e}`),
      ...CONSENT_SECTIONS.flatMap((s) => [`registration.consent.sections.${s}.title`, `registration.consent.sections.${s}.text`]),
      ...ORDER_STATUSES.map((s) => `trip.status.${s}`),
      ...CATEGORIES.map((c) => `category.${c}`),
      ...PAYMENT_METHODS.flatMap((p) => [`payment_method.${p}`, `payment.${p}`]),
      ...TOPUP_STATUSES.map((s) => `topup_status.${s}`),
      ...MODERATION_STATUSES.map((s) => `moderation_status.${s}`),
      ...['not_approved', 'offline', 'insufficient_balance', 'has_active_order', 'location_required', 'location_stale'].map((r) => `orders.eligibility.${r}`),
      ...CANCEL_REASONS.map((r) => `trip.cancel_reasons.${r}`),
      ...['passenger', 'driver', 'admin', 'system'].map((a) => `trip.cancelled_by.${a}`),
      ...['ios', 'no_gps', 'too_many_points'].map((n) => `trip.note_${n}`),
      ...[
        'topup_credit',
        'topup_refund',
        'commission_reserve',
        'commission_release',
        'commission_charge',
        'commission_refund',
        'adjustment_credit',
        'adjustment_debit',
      ].map((e) => `wallet.entry.${e}`),
      ...LOCALES.map((l) => `languages.${l}`),
      ...RECEIPT_REASONS.map((r) => `topup.reasons.${r}`),
      ...['duplicate', 'not_pdf', 'too_large', 'unreadable', 'not_allowed', 'closed', 'rate_limited', 'network', 'other'].map((e) => `topup.upload_errors.${e}`),
    ]
    expect(missing(keys)).toEqual([])
  })

  it('Kazakh texts are not left in Russian or English', () => {
    const kk = (key: string) => i18n.global.t(key, {}, { locale: 'kk' })
    for (const key of ['trip.open_2gis', 'trip.next_only', 'order.accept', 'order.taken', 'trip.arrive', 'topup.open_kaspi', 'topup.upload_pdf', 'topup.review_title']) {
      expect(kk(key)).toMatch(/[әғқңөұүһі]|[А-Яа-я]/)
    }
    expect(kk('trip.open_2gis')).toBe('2GIS арқылы ашу')
    expect(kk('order.accept')).toBe('Тапсырысты қабылдау')
    expect(kk('order.taken')).toBe('Тапсырысты басқа жүргізуші қабылдады')
    expect(i18n.global.t('trip.open_2gis', {}, { locale: 'ru' })).toBe('Открыть в 2GIS')
    expect(i18n.global.t('trip.open_2gis', {}, { locale: 'en' })).toBe('Open in 2GIS')
  })
})
