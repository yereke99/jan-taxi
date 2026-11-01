// The passenger's own words (a manual stop, or pickup A typed over its GPS or
// pin point) are shown exactly as written; any other address goes through the
// "Point on the map" filter.

import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it } from 'vitest'
import { defineComponent } from 'vue'
import { i18n } from '../../i18n'
import { isPassengerWritten, useStopText, type StopAddress } from './stopText'

/** Runs useStopText inside a component, as the screens do. */
function textOf(s: StopAddress | null): string {
  let text = ''
  mount(
    defineComponent({
      setup() {
        text = useStopText()(s)
        return () => null
      },
    }),
    { global: { plugins: [i18n] } },
  )
  return text
}

const COORDS = '48.826400, 58.144200'

beforeEach(() => {
  i18n.global.locale.value = 'kk'
})

describe('isPassengerWritten', () => {
  it('is true for a manual stop and for a typed pickup', () => {
    expect(isPassengerWritten({ mode: 'manual', address_typed: false })).toBe(true)
    expect(isPassengerWritten({ mode: 'geocoded', address_typed: true })).toBe(true)
  })

  it('is false for any other stop, also when the flag is missing', () => {
    expect(isPassengerWritten({ mode: 'geocoded', address_typed: false })).toBe(false)
    expect(isPassengerWritten({})).toBe(false)
    expect(isPassengerWritten(null)).toBe(false)
    expect(isPassengerWritten(undefined)).toBe(false)
  })
})

describe('useStopText', () => {
  it("shows the passenger's words verbatim, never as a point on the map", () => {
    expect(textOf({ mode: 'geocoded', address_typed: true, address: 'Мешіттің жанында, көк қақпа' })).toBe('Мешіттің жанында, көк қақпа')
    expect(textOf({ mode: 'manual', address: 'Әженің үйі' })).toBe('Әженің үйі')
    expect(textOf({ mode: 'geocoded', address_typed: true, address: COORDS })).toBe(COORDS)
    expect(textOf({ mode: 'manual', address: COORDS })).toBe(COORDS)
  })

  it('passes every other address through the "Point on the map" filter', () => {
    expect(textOf({ mode: 'geocoded', address_typed: false, address: ' Абай көшесі, 12 ' })).toBe('Абай көшесі, 12')
    expect(textOf({ mode: 'geocoded', address_typed: false, address: COORDS })).toBe('Картадағы нүкте')
    expect(textOf({ address: '' })).toBe('Картадағы нүкте')
    expect(textOf(null)).toBe('Картадағы нүкте')
  })
})
