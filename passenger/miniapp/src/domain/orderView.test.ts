import { describe, expect, it } from 'vitest'
import type { Order } from '../api/types'
import { i18n } from '../i18n'
import { cancelInfo, DRIVER_CANCEL_CODES } from './orderView'

function cancelled(by: Order['cancelled_by'], reason: string | null): Order {
  return { status: 'cancelled', cancelled_by: by, cancel_reason: reason } as Order
}

describe('cancelInfo', () => {
  it('translates the system timeout', () => {
    expect(cancelInfo(cancelled('system', 'no_driver_found'))).toEqual({ byKey: null, reasonKey: 'order.reason_no_driver_found', text: null })
  })

  it('translates driver reason codes instead of showing them raw', () => {
    expect(cancelInfo(cancelled('driver', 'passenger_no_show'))).toEqual({
      byKey: 'order.reason_by_driver',
      reasonKey: 'order.driver_reason.passenger_no_show',
      text: null,
    })
  })

  it('has a message for every driver reason code in every language', () => {
    for (const locale of ['kk', 'ru', 'en'] as const) {
      for (const code of DRIVER_CANCEL_CODES) {
        expect(i18n.global.te(`order.driver_reason.${code}`, locale)).toBe(true)
      }
    }
  })

  it('keeps free-text reasons from people', () => {
    expect(cancelInfo(cancelled('driver', 'Жолаушы шықпады'))).toEqual({ byKey: 'order.reason_by_driver', reasonKey: null, text: 'Жолаушы шықпады' })
    expect(cancelInfo(cancelled('passenger', null))).toEqual({ byKey: 'order.reason_by_passenger', reasonKey: null, text: null })
  })
})
