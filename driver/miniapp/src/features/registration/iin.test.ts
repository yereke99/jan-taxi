// The IIN rules must match the API (admin/backend/internal/platform/iin): the
// same synthetic numbers are valid or rejected for the same reason there.

import { describe, expect, it } from 'vitest'
import { iinDigits, iinProblem, isValidIin } from './iin'

describe('IIN (ЖСН)', () => {
  it.each(['900101300017', '000301500128', '000102500780', '860303401029', '070707603041', '991231400892'])('accepts %s', (v) => {
    expect(iinProblem(v)).toBeNull()
    expect(isValidIin(v)).toBe(true)
  })

  it('uses the second weights when the first sum gives 10', () => {
    // 860303401029: weights 1..11 give 10, weights 3..11,1,2 give the check digit 9.
    expect(isValidIin('860303401029')).toBe(true)
    expect(isValidIin('860303401020')).toBe(false)
  })

  it.each(['900101300018', '000301500129', '860303401020', '111111111111'])('rejects the check digit of %s', (v) => {
    expect(iinProblem(v)).toBe('checksum')
  })

  it('asks for exactly twelve digits', () => {
    expect(iinProblem('')).toBe('required')
    expect(iinProblem('90010130001')).toBe('length')
    expect(iinProblem('9001013000170')).toBe('length')
    expect(iinProblem('9001-0130-0017')).toBe('length')
  })

  it('keeps only digits of what was typed or pasted, leading zeros included', () => {
    expect(iinDigits(' 9001 0130 0017 ')).toBe('900101300017')
    expect(iinDigits('0003-0150-0128')).toBe('000301500128')
    expect(iinDigits('ЖСН: 9001a')).toBe('9001')
    expect(iinDigits('9001013000170')).toBe('9001013000170')
  })
})
