// Parsing of operator-typed numbers. Money is whole tenge (§6): digits with
// optional group separators (space, no-break space, comma, apostrophe).

const GROUP_SEPARATORS = /[\s  ,']/g

/** Whole number from text ("1 500" → 1500); null for empty or malformed input. */
export function parseInteger(input: string | number | null | undefined): number | null {
  if (typeof input === 'number') return Number.isSafeInteger(input) ? input : null
  const s = (input ?? '').replace(GROUP_SEPARATORS, '').replace(/^\+/, '').replace(/^[−–]/, '-')
  if (!/^-?\d{1,15}$/.test(s)) return null
  const n = Number(s)
  return Number.isSafeInteger(n) ? n : null
}

/** Decimal number with "." or "," as the separator; null for malformed input. */
export function parseDecimal(input: string | number | null | undefined): number | null {
  if (typeof input === 'number') return Number.isFinite(input) ? input : null
  const s = (input ?? '').trim().replace(/[\s  ]/g, '').replace(',', '.').replace(/^[−–]/, '-')
  if (!/^-?\d+(\.\d+)?$/.test(s)) return null
  const n = Number(s)
  return Number.isFinite(n) ? n : null
}

/** Commission percent ("10", "12,5") → basis points (1000, 1250), rounded half up. */
export function percentToBps(input: string | number): number | null {
  const v = parseDecimal(input)
  if (v === null) return null
  return Math.round(v * 100)
}

/** Basis points → percent text for an input field (1000 → "10", 1250 → "12.5"). */
export function bpsToPercentInput(bps: number): string {
  return String(Math.round(bps) / 100)
}
