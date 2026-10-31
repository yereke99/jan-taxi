// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { describe, expect, it } from 'vitest'
import { contactTelHref, formatKzPhone, formatKzPhoneInput, kzNationalDigits, normalizeContactPhone, normalizeKzPhone, telHref, whatsAppHref } from './phone'

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

describe('contact links', () => {
  it.each([
    ['+7 (701) 234-56-78', '+77012345678'],
    ['8 (701) 234-56-78', '+77012345678'],
    ['77012345678', '+77012345678'],
    ['7012345678', '+77012345678'],
    ['+44 7700 900123', '+447700900123'],
  ])('uses the complete number for %j', (input, normalized) => {
    expect(normalizeContactPhone(input)).toBe(normalized)
    expect(contactTelHref(input)).toBe(`tel:${normalized}`)
    expect(whatsAppHref(input)).toBe(`https://wa.me/${normalized.slice(1)}`)
  })

  it('opens a WhatsApp chat with the international digits only, never with a message', () => {
    const href = whatsAppHref('+7 707 123 45 67')
    expect(href).toBe('https://wa.me/77071234567')
    const url = new URL(href!)
    expect(url.pathname).toMatch(/^\/\d+$/)
    expect(url.search).toBe('')
    expect(url.hash).toBe('')
  })

  it.each(['', '+', '++77012345678', '+77012345678;123', '+77012345678 ext 9', '+77012345678@evil.example', '+077012345678', '70123', '770123456789', '+770123456789', '+7701234567', '+1234567890123456', 'tel:+77012345678', '+77012345678?body=hi'])('rejects malformed or incomplete %j', (input) => {
    expect(normalizeContactPhone(input)).toBeNull()
    expect(contactTelHref(input)).toBeNull()
    expect(whatsAppHref(input)).toBeNull()
  })
})
