// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Kazakhstan mobile numbers (ARCHITECTURE §11): the API stores E.164
// "+77XXXXXXXXX" and accepts "8 7xx…", "+7 7xx…" and "7xx…". The input mask
// shows "+7 7xx xxx xx xx" while the user types the ten national digits.

const NATIONAL_DIGITS = 10
const PREFIX = '+7'

/**
 * National digits (without the country code) from any typed or pasted text:
 * a leading "+7" is the country code, an 11-digit number starting with 7 or 8
 * drops its first digit, and a lone leading 8 (trunk prefix) is ignored.
 */
export function kzNationalDigits(input: string): string {
  const s = input.trim()
  if (s.startsWith(PREFIX)) return s.slice(PREFIX.length).replace(/\D/g, '').slice(0, NATIONAL_DIGITS)
  let digits = s.replace(/\D/g, '')
  if (digits.length > NATIONAL_DIGITS && (digits[0] === '7' || digits[0] === '8')) digits = digits.slice(1)
  else if (digits[0] === '8') digits = digits.slice(1)
  return digits.slice(0, NATIONAL_DIGITS)
}

/** Display form for the input field: "+7 701 234 56 78" (partial while typing). */
export function formatKzPhoneInput(input: string): string {
  const d = kzNationalDigits(input)
  if (d.length === 0) return ''
  const parts = [d.slice(0, 3), d.slice(3, 6), d.slice(6, 8), d.slice(8, 10)].filter((p) => p.length > 0)
  return `${PREFIX} ${parts.join(' ')}`
}

/** E.164 "+77XXXXXXXXX" for a complete Kazakhstan mobile number, else null. */
export function normalizeKzPhone(input: string): string | null {
  const d = kzNationalDigits(input)
  return d.length === NATIONAL_DIGITS && d[0] === '7' ? `${PREFIX}${d}` : null
}

/** Human form of an E.164 KZ number: "+7 701 234 56 78" (other values unchanged). */
export function formatKzPhone(e164: string): string {
  return /^\+77\d{9}$/.test(e164) ? formatKzPhoneInput(e164) : e164
}

/** tel: link for a phone number (digits and a leading plus only). */
export function telHref(phone: string): string {
  const clean = phone.replace(/[^\d+]/g, '')
  return `tel:${clean}`
}

/** Validates a complete contact number without truncating or guessing digits. */
export function normalizeContactPhone(input: string): string | null {
  const raw = input.trim()
  if (!raw || raw.length > 64 || !/^\+?[\d\s().-]+$/.test(raw)) return null
  const digits = raw.replace(/[\s().-]/g, '')
  if (digits.startsWith('+7')) return /^\+77\d{9}$/.test(digits) ? digits : null
  if (digits.startsWith('+')) return /^\+[1-9]\d{7,14}$/.test(digits) ? digits : null
  if (/^[78]7\d{9}$/.test(digits)) return `+7${digits.slice(1)}`
  if (/^7\d{9}$/.test(digits)) return `+7${digits}`
  return null
}

/** A private recipient-specific chat; this only opens the composer. */
export function whatsAppHref(phone: string): string | null {
  const normalized = normalizeContactPhone(phone)
  return normalized ? `https://wa.me/${normalized.slice(1)}` : null
}
