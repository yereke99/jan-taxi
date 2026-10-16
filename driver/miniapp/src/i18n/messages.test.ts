// Message catalogue checks for the driver app (core + app messages): identical
// key sets in kk, ru and en, matching {placeholders}, no empty texts, and no
// language mixed into the wrong catalogue. Key usage is covered by keys.test.ts.

import { describe, expect, it } from 'vitest'
import { LOCALES, type Locale } from './core'
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

describe('driver message catalogue', () => {
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
