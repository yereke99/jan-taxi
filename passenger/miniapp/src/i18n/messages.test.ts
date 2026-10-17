// Message catalogue checks for the passenger app (core + app messages):
// identical key sets in kk, ru and en, matching {placeholders}, no empty texts,
// every literal t('…') key used by the screens defined, and no language mixed
// into the wrong catalogue.

import { describe, expect, it } from 'vitest'
import { KNOWN_PROHIBITED_ITEMS } from '../domain/prohibited'
import { CATEGORIES, MODERATION_STATUSES, ORDER_STATUSES, PAYMENT_METHODS, TOPUP_STATUSES } from '../utils/status'
import { ApiError } from '../services/http'
import { errorText, LOCALES, type Locale } from './core'
import { i18n } from './index'

type Tree = { [key: string]: string | Tree }

function leaves(tree: unknown, prefix = ''): Array<[string, string]> {
  if (typeof tree === 'string') return [[prefix, tree]]
  if (!tree || typeof tree !== 'object') return []
  return Object.entries(tree as Tree).flatMap(([k, v]) => leaves(v, prefix ? `${prefix}.${k}` : k))
}

function catalogue(locale: Locale): Map<string, string> {
  return new Map(leaves(i18n.global.getLocaleMessage(locale)))
}

function placeholders(text: string): string[] {
  return [...text.matchAll(/\{\s*([A-Za-z0-9_]+)\s*\}/g)].map((m) => m[1]!).sort()
}

// Raw sources of the app (Vite glob import), without message files and tests.
const SOURCES = import.meta.glob<string>(['../**/*.vue', '../**/*.ts', '!../i18n/**', '!../**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
})

/**
 * Russian words that never occur in Kazakh UI text (function words and the
 * Russian forms of the product's own nouns). A Kazakh string containing one of
 * them was left untranslated or mixed.
 */
const RUSSIAN_ONLY = new Set([
  'и', 'в', 'во', 'на', 'для', 'что', 'это', 'или', 'по', 'от', 'из', 'при', 'без', 'же',
  'вы', 'вас', 'вам', 'ваш', 'ваша', 'ваше', 'ваши', 'мы', 'нет', 'уже', 'ещё', 'еще', 'если', 'когда',
  'чтобы', 'только', 'можно', 'нужно', 'пожалуйста', 'спасибо', 'заказ', 'заказа', 'заказы', 'водитель',
  'водителя', 'поездка', 'поездки', 'оплата', 'отмена', 'отменить', 'выберите', 'укажите', 'введите',
])

function russianWords(text: string): string[] {
  return text
    .toLowerCase()
    .split(/[^a-zа-яёәғқңөұүһі]+/)
    .filter((w) => RUSSIAN_ONLY.has(w))
}

describe('passenger message catalogue', () => {
  const kk = catalogue('kk')

  it('has the same keys in kk, ru and en', () => {
    const keys = [...kk.keys()].sort()
    expect(keys.length).toBeGreaterThan(200)
    for (const locale of LOCALES) expect([...catalogue(locale).keys()].sort()).toEqual(keys)
  })

  it('has no empty texts', () => {
    const empty = LOCALES.flatMap((l) => [...catalogue(l)].filter(([, v]) => v.trim() === '').map(([k]) => `${l}:${k}`))
    expect(empty).toEqual([])
  })

  it('uses the same {placeholders} in every language', () => {
    const bad: string[] = []
    for (const locale of LOCALES) {
      for (const [k, v] of catalogue(locale)) {
        if (placeholders(v).join(',') !== placeholders(kk.get(k) ?? '').join(',')) bad.push(`${locale}:${k}`)
      }
    }
    expect(bad).toEqual([])
  })

  it('defines every literal key the screens pass to t()', () => {
    const missing: string[] = []
    const re = /\bte?\(\s*'([a-z0-9_]+(?:\.[a-z0-9_]+)+)'/g
    for (const [file, text] of Object.entries(SOURCES)) {
      for (const m of text.matchAll(re)) if (!kk.has(m[1]!)) missing.push(`${file}: ${m[1]}`)
    }
    expect(missing).toEqual([])
  })

  it('defines every key chosen from an enum', () => {
    const keys = [
      ...ORDER_STATUSES.map((s) => `order_status.${s}`),
      ...CATEGORIES.map((c) => `category.${c}`),
      ...PAYMENT_METHODS.map((p) => `payment_method.${p}`),
      ...MODERATION_STATUSES.map((s) => `moderation_status.${s}`),
      ...TOPUP_STATUSES.map((s) => `topup_status.${s}`),
      ...[...KNOWN_PROHIBITED_ITEMS, 'other'].map((c) => `prohibited.${c}`),
      ...LOCALES.map((l) => `languages.${l}`),
    ]
    expect(keys.filter((k) => !kk.has(k))).toEqual([])
  })

  it('keeps Kazakh-only letters out of ru and en', () => {
    const kazakh = /[әғқңөұүһі]/i
    const offenders = (['ru', 'en'] as const).flatMap((l) =>
      [...catalogue(l)].filter(([k, v]) => kazakh.test(v) && !k.startsWith('languages.')).map(([k]) => `${l}:${k}`),
    )
    expect(offenders).toEqual([])
  })

  it('has no Russian words inside Kazakh texts', () => {
    const offenders = [...kk].filter(([k, v]) => !k.startsWith('languages.') && russianWords(v).length > 0).map(([k, v]) => `${k}: ${v}`)
    expect(offenders).toEqual([])
  })
})

describe('passenger error texts', () => {
  const tr = {
    t: (k: string) => i18n.global.t(k),
    te: (k: string, l?: string) => (l ? i18n.global.te(k, l as Locale) : i18n.global.te(k)),
  }

  it('uses the text of a known 409 reason (orders/lifecycle ConflictReason)', () => {
    const err = new ApiError(409, 'conflict', 'order already rated', {}, { reason: 'already_rated' })
    expect(errorText(err, tr)).toBe(i18n.global.t('errors.conflict_reasons.already_rated'))
  })

  it('uses the reason of an invalid_transition when the app defines one', () => {
    const err = new ApiError(409, 'invalid_transition', 'transition not allowed', {}, { from: 'completed', action: 'dispute', reason: 'dispute_window_closed' })
    expect(errorText(err, tr)).toBe(i18n.global.t('errors.conflict_reasons.dispute_window_closed'))
  })

  it('falls back to the code text for unknown reasons and to errors.unknown for unknown codes', () => {
    expect(errorText(new ApiError(409, 'conflict', 'x', {}, { reason: 'something_new' }), tr)).toBe(i18n.global.t('errors.conflict'))
    expect(errorText(new ApiError(422, 'image_too_large', 'x'), tr)).toBe(i18n.global.t('errors.image_too_large'))
    expect(errorText(new ApiError(418, 'teapot', 'x'), tr)).toBe(i18n.global.t('errors.unknown'))
  })
})
