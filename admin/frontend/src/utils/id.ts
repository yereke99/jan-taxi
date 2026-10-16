/**
 * RFC 4122 v4 UUID. Uses crypto.randomUUID when present (secure contexts) and
 * falls back to crypto.getRandomValues for older WebViews.
 */
export function uuidv4(): string {
  const c: Crypto | undefined = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') return c.randomUUID()
  if (!c || typeof c.getRandomValues !== 'function') {
    throw new Error('secure random generator unavailable')
  }
  const b = c.getRandomValues(new Uint8Array(16))
  b[6] = (b[6]! & 0x0f) | 0x40
  b[8] = (b[8]! & 0x3f) | 0x80
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** True for a canonical UUID string (any version). */
export function isUuid(s: string): boolean {
  return UUID_RE.test(s)
}
