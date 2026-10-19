import { describe, expect, it } from 'vitest'
import type { Order } from '../api/types'
import { i18n } from '../i18n'
import { arrivalMinutes, cancelInfo, DRIVER_CANCEL_CODES, ETA_MAX_AGE_MS, orderLocality } from './orderView'

function cancelled(by: Order['cancelled_by'], reason: string | null, code: Order['cancel_reason_code'] = null): Order {
  return { status: 'cancelled', cancelled_by: by, cancel_reason: reason, cancel_reason_code: code } as Order
}

describe('cancelInfo', () => {
  it('translates the system timeout', () => {
    expect(cancelInfo(cancelled('system', 'no_driver_found'))).toEqual({ byKey: null, reasonKey: 'order.reason_no_driver_found', labelKey: null, text: null })
  })

  it('translates driver reason codes instead of showing them raw', () => {
    expect(cancelInfo(cancelled('driver', 'passenger_no_show'))).toEqual({
      byKey: 'order.reason_by_driver',
      reasonKey: 'order.driver_reason.passenger_no_show',
      labelKey: null,
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
    expect(cancelInfo(cancelled('driver', 'Жолаушы шықпады'))).toEqual({ byKey: 'order.reason_by_driver', reasonKey: null, labelKey: null, text: 'Жолаушы шықпады' })
    expect(cancelInfo(cancelled('passenger', null))).toEqual({ byKey: 'order.reason_by_passenger', reasonKey: null, labelKey: null, text: null })
  })

  it("shows the passenger's reason code with the comment", () => {
    expect(cancelInfo(cancelled('passenger', 'Басқа көлік таптым', 'other'))).toEqual({
      byKey: 'order.reason_by_passenger',
      reasonKey: null,
      labelKey: 'cancel.reason.other',
      text: 'Басқа көлік таптым',
    })
    expect(cancelInfo(cancelled('passenger', null, 'long_wait')).labelKey).toBe('cancel.reason.long_wait')
  })
})

describe('arrivalMinutes', () => {
  const now = Date.parse('2026-10-19T10:00:00Z')
  function accepted(over: Partial<Order>): Order {
    return {
      status: 'accepted',
      driver_eta_s: null,
      driver_eta_at: null,
      driver_location: null,
      stops: [{ lat: 48.82, lon: 58.14 }],
      ...over,
    } as Order
  }

  it("uses the API's road estimate less the time since it was computed", () => {
    expect(arrivalMinutes(accepted({ driver_eta_s: 300, driver_eta_at: '2026-10-19T10:00:00Z' }), now)).toBe(5)
    expect(arrivalMinutes(accepted({ driver_eta_s: 300, driver_eta_at: '2026-10-19T09:58:00Z' }), now)).toBe(3)
    // Never below one minute while the driver is still on the way.
    expect(arrivalMinutes(accepted({ driver_eta_s: 60, driver_eta_at: '2026-10-19T09:58:00Z' }), now)).toBe(1)
  })

  it('falls back to the straight-line estimate when the API estimate is missing or old', () => {
    const loc = { lat: 48.83, lon: 58.14, accuracy_m: null, heading: null, recorded_at: '2026-10-19T09:59:50Z' }
    const old = new Date(now - ETA_MAX_AGE_MS - 1000).toISOString()
    const local = arrivalMinutes(accepted({ driver_location: loc }), now)
    expect(local).toBeGreaterThanOrEqual(1)
    expect(arrivalMinutes(accepted({ driver_location: loc, driver_eta_s: 1200, driver_eta_at: old }), now)).toBe(local)
    expect(arrivalMinutes(accepted({}), now)).toBeNull()
  })

  it('applies only while the driver is on the way', () => {
    expect(arrivalMinutes(accepted({ status: 'driver_arriving', driver_eta_s: 300 }), now)).toBeNull()
  })
})

describe('orderLocality', () => {
  it("shows the API's locality of pickup A, never the tariff region", () => {
    expect(orderLocality({ locality: 'Алматы' })).toBe('Алматы')
    expect(orderLocality({ locality: '  Шымкент ' }, 'Абай даңғылы, Алматы')).toBe('Шымкент')
  })

  it('falls back to the pickup subtitle seen while booking, never to coordinates', () => {
    expect(orderLocality({ locality: null }, 'Алматы, Алматы облысы')).toBe('Алматы, Алматы облысы')
    expect(orderLocality({ locality: '43.238900, 76.889700' }, '43.2389, 76.8897')).toBeNull()
    expect(orderLocality({ locality: null })).toBeNull()
    expect(orderLocality({ locality: '' }, '  ')).toBeNull()
  })
})
