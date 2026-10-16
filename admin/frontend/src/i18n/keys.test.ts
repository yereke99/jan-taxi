// Every literal message key used in the sources must exist (vue-i18n would
// otherwise print the raw key to operators), and ru/en must not leave a kk
// string untranslated by accident.

import { describe, expect, it } from 'vitest'
import { en, kk, ru } from './messages'

const sources = import.meta.glob(['../**/*.vue', '../**/*.ts', '!../**/*.test.ts', '!./areas/**'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

function has(tree: unknown, key: string): boolean {
  let node: unknown = tree
  for (const part of key.split('.')) {
    if (!node || typeof node !== 'object' || !(part in node)) return false
    node = (node as Record<string, unknown>)[part]
  }
  return typeof node === 'string'
}

function leaves(tree: unknown, prefix = ''): Array<[string, string]> {
  if (typeof tree === 'string') return [[prefix, tree]]
  if (!tree || typeof tree !== 'object') return []
  return Object.entries(tree).flatMap(([k, v]) => leaves(v, prefix ? `${prefix}.${k}` : k))
}

describe('i18n keys', () => {
  it('finds sources to scan', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(50)
  })

  it('defines every literal key passed to t()', () => {
    const missing: string[] = []
    const re = /\bt\(\s*'([a-z0-9_]+(?:\.[a-z0-9_]+)+)'/g
    for (const [file, text] of Object.entries(sources)) {
      for (const m of text.matchAll(re)) {
        const key = m[1]!
        if (!has(kk, key)) missing.push(`${file}: ${key}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('has the same keys in kk, ru and en', () => {
    const k = leaves(kk).map(([p]) => p).sort()
    expect(leaves(ru).map(([p]) => p).sort()).toEqual(k)
    expect(leaves(en).map(([p]) => p).sort()).toEqual(k)
  })

  it('has no Kazakh-only letters in ru or en strings', () => {
    const kazakh = /[әғқңөұүһі]/i
    const offenders = [...leaves(ru), ...leaves(en)].filter(([path, v]) => kazakh.test(v) && !path.startsWith('languages.'))
    expect(offenders).toEqual([])
  })

  it('has no empty texts', () => {
    const empty = [...leaves(kk), ...leaves(ru), ...leaves(en)].filter(([, v]) => v.trim() === '')
    expect(empty).toEqual([])
  })

  it('uses the same {placeholders} in every language', () => {
    const names = (text: string): string => [...text.matchAll(/\{\s*([A-Za-z0-9_]+)\s*\}/g)].map((m) => m[1]!).sort().join(',')
    const base = new Map(leaves(kk).map(([p, v]) => [p, names(v)]))
    const bad = [...leaves(ru), ...leaves(en)].filter(([p, v]) => names(v) !== base.get(p)).map(([p]) => p)
    expect(bad).toEqual([])
  })

  it('has no Russian words inside Kazakh texts', () => {
    // Function words and Russian forms of the product's nouns never appear in Kazakh UI text.
    const russian = new Set([
      'и', 'в', 'во', 'на', 'для', 'что', 'это', 'или', 'по', 'от', 'из', 'при', 'без', 'же',
      'вы', 'вас', 'вам', 'ваш', 'ваша', 'ваше', 'ваши', 'мы', 'нет', 'уже', 'ещё', 'еще', 'если', 'когда',
      'чтобы', 'только', 'можно', 'нужно', 'пожалуйста', 'спасибо', 'заказ', 'заказа', 'заказы', 'водитель',
      'водителя', 'поездка', 'поездки', 'оплата', 'отмена', 'отменить', 'выберите', 'укажите', 'введите',
    ])
    const offenders = leaves(kk)
      .filter(([path]) => !path.startsWith('languages.'))
      .filter(([, v]) => v.toLowerCase().split(/[^a-zа-яёәғқңөұүһі]+/).some((w) => russian.has(w)))
    expect(offenders).toEqual([])
  })
})
