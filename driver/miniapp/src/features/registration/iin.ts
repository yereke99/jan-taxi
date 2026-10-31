// Kazakhstan individual identification number (ЖСН / ИИН), the rules of the
// API (admin/backend/internal/platform/iin): twelve digits, the last a check
// digit. The check digit is the weighted sum of the first eleven digits modulo
// 11 with weights 1..11; when that gives 10 the sum is taken again with
// weights 3..11, 1, 2, and a second 10 means the number is never issued.

export const IIN_LENGTH = 12

/** Why a typed IIN cannot be used: nothing typed, not twelve digits, or a wrong check digit. */
export type IinProblem = 'required' | 'length' | 'checksum'

const DIGITS_RE = /^\d{12}$/

/**
 * Keeps only the digits of what was typed or pasted ("9001 0130 0017" →
 * "900101300017"). Nothing is cut: a thirteenth digit stays visible so the
 * length message explains it.
 */
export function iinDigits(s: string): string {
  return s.replace(/\D+/g, '')
}

function weighted(d: readonly number[], weight: (i: number) => number): number {
  let sum = 0
  for (let i = 0; i < 11; i++) sum += d[i]! * weight(i)
  return sum % 11
}

/** Expects twelve ASCII digits. */
function checksumOk(value: string): boolean {
  const d = [...value].map(Number)
  let c = weighted(d, (i) => i + 1)
  if (c === 10) c = weighted(d, (i) => ((i + 2) % 11) + 1)
  return c !== 10 && c === d[11]
}

/** What is wrong with the IIN, or null when it is valid. */
export function iinProblem(value: string): IinProblem | null {
  if (value === '') return 'required'
  if (!DIGITS_RE.test(value)) return 'length'
  return checksumOk(value) ? null : 'checksum'
}

export function isValidIin(value: string): boolean {
  return iinProblem(value) === null
}
