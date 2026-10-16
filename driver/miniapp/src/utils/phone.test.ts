// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { formatKzPhone, formatKzPhoneInput, kzNationalDigits, normalizeKzPhone, telHref } from './phone'

describe('KZ phone mask', () => {
  it.each([
    ['', ''],
    ['7', '+7 7'],
    ['701', '+7 701'],
    ['7012', '+7 701 2'],
    ['7012345678', '+7 701 234 56 78'],
    ['87012345678', '+7 701 234 56 78'],
    ['+7 701 234 56 78', '+7 701 234 56 78'],
    ['+77012345678', '+7 701 234 56 78'],
    ['77012345678', '+7 701 234 56 78'],
    ['8 (701) 234-56-78', '+7 701 234 56 78'],
    ['8', ''],
    ['+7 701 234 56 789', '+7 701 234 56 78'],
  ])('formats %j as %j', (input, out) => {
    expect(formatKzPhoneInput(input)).toBe(out)
  })

  it('keeps the national digits stable when the formatted value is typed into again', () => {
    const once = formatKzPhoneInput('70123')
    expect(kzNationalDigits(`${once}4`)).toBe('701234')
  })

  it.each([
    ['+7 701 234 56 78', '+77012345678'],
    ['87012345678', '+77012345678'],
    ['7012345678', '+77012345678'],
    ['+7 701 234 56', null],
    ['+7 601 234 56 78', null],
    ['', null],
  ])('normalizes %j to %j', (input, out) => {
    expect(normalizeKzPhone(input)).toBe(out)
  })

  it('formats stored numbers and builds tel links', () => {
    expect(formatKzPhone('+77071234567')).toBe('+7 707 123 45 67')
    expect(formatKzPhone('+44123')).toBe('+44123')
    expect(telHref('+7 707 123 45 67')).toBe('tel:+77071234567')
  })
})
