// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { cleanText, cleanedLength, codePointLength, fitsLimit, TEXT_MAX } from './text'

const KAZAKH = 'әғқңөұүһі'

function kazakhOf(n: number): string {
  return Array.from({ length: n }, (_, i) => KAZAKH[i % KAZAKH.length]).join('')
}

describe('codePointLength', () => {
  it.each([
    ['', 0],
    ['abc', 3],
    [KAZAKH, 9],
    ['Сәлеметсіз бе', 13],
  ])('counts %j as %i', (s, n) => {
    expect(codePointLength(s)).toBe(n)
  })

  it('counts NFC-composed characters once', () => {
    // "и" + combining breve composes to "й" under NFC.
    const decomposed = 'и' + String.fromCodePoint(0x0306)
    expect(decomposed.length).toBe(2)
    expect(codePointLength(decomposed)).toBe(1)
  })

  it('counts emoji by code points, like the backend', () => {
    expect(codePointLength(String.fromCodePoint(0x1f44d))).toBe(1)
    // Thumbs up + skin tone modifier.
    expect(codePointLength(String.fromCodePoint(0x1f44d, 0x1f3fd))).toBe(2)
    // Family: man ZWJ woman ZWJ girl = 5 code points, 8 UTF-16 units.
    const family = String.fromCodePoint(0x1f468, 0x200d, 0x1f469, 0x200d, 0x1f467)
    expect(family.length).toBe(8)
    expect(codePointLength(family)).toBe(5)
  })
})

describe('120 code point limit', () => {
  it('accepts exactly 120 Kazakh letters', () => {
    const s = kazakhOf(120)
    expect(codePointLength(s)).toBe(120)
    expect(fitsLimit(s)).toBe(true)
  })

  it('rejects 121 Kazakh letters', () => {
    expect(fitsLimit(kazakhOf(121))).toBe(false)
  })

  it('counts emoji as more than one UTF-16 unit but by code points', () => {
    const emoji = String.fromCodePoint(0x1f695) // taxi
    const s = kazakhOf(119) + emoji
    expect(s.length).toBe(121)
    expect(fitsLimit(s)).toBe(true)
    expect(fitsLimit(s + 'а')).toBe(false)
  })

  it('uses 120 as the default maximum', () => {
    expect(TEXT_MAX).toBe(120)
  })
})

describe('cleanText', () => {
  it('trims, replaces line breaks/tabs and collapses spaces', () => {
    expect(cleanText('  Сәлем\n\tдос   қалай ')).toBe('Сәлем дос қалай')
  })

  it('strips control and bidi override characters', () => {
    const rlo = String.fromCharCode(0x202e)
    const nul = String.fromCharCode(0)
    expect(cleanText(`ab${rlo}c${nul}d`)).toBe('abcd')
  })

  it('keeps the zero-width joiner inside emoji sequences', () => {
    const family = String.fromCodePoint(0x1f468, 0x200d, 0x1f469)
    expect(cleanText(family)).toBe(family)
  })

  it('measures the cleaned text', () => {
    expect(cleanedLength('  a  b  ')).toBe(3)
  })
})
