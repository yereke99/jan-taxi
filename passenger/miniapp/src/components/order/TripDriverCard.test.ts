import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import type { OrderDriver, Vehicle } from '../../api/types'
import { createI18nInstance } from '../../i18n/core'
import { en } from '../../i18n/messages/en'
import { kk } from '../../i18n/messages/kk'
import { ru } from '../../i18n/messages/ru'
import { copyText } from '../../utils/clipboard'
import JSheet from '../ui/JSheet.vue'
import { toast } from '../ui'
import TripDriverCard from './TripDriverCard.vue'

vi.mock('../../utils/clipboard', () => ({ copyText: vi.fn(async () => true) }))

const global = { plugins: [createI18nInstance({ kk, ru, en })] }

function driver(over: Partial<OrderDriver> = {}): OrderDriver {
  return {
    id: 'd1',
    code: 'EMB001',
    first_name: 'Ерлан',
    last_name: 'С.',
    phone: '+77012345678',
    rating: 4.9,
    rating_count: 12,
    completed_trips: 40,
    photo_url: null,
    ...over,
  }
}

const vehicle = { make: 'Toyota', model: 'Camry', color: 'ақ', plate: '123ABC04' } as Vehicle

function mountCard(d: OrderDriver = driver()) {
  return mount(TripDriverCard, { props: { driver: d, vehicle, photoUrl: null }, global })
}

beforeEach(() => {
  vi.mocked(copyText).mockResolvedValue(true)
  vi.clearAllMocks()
})
afterEach(() => toast.clear())

describe('TripDriverCard call', () => {
  it('calls the driver through a real tel: link opened outside the WebView', () => {
    const parentClick = vi.fn()
    const Host = defineComponent({ setup: () => () => h('div', { onClick: parentClick }, h(TripDriverCard, { driver: driver(), vehicle, photoUrl: null })) })
    const w = mount(Host, { global })
    const link = w.find('a[href^="tel:"]')
    expect(link.attributes('href')).toBe('tel:+77012345678')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
    expect(link.text()).toContain('Қоңырау шалу')
    const click = new MouseEvent('click', { bubbles: true, cancelable: true })
    link.element.dispatchEvent(click)
    expect(click.defaultPrevented).toBe(false)
    expect(parentClick).not.toHaveBeenCalled()
    w.unmount()
  })

  it.each([null, '+7701234', '+77012345678;1'])('offers no call and no number for %j', (phone) => {
    const w = mountCard(driver({ phone }))
    expect(w.find('a[href^="tel:"]').exists()).toBe(false)
    expect(w.find('.ride-driver__phone').exists()).toBe(false)
    expect(w.findAll('.ride-action')).toHaveLength(2)
    w.unmount()
  })

  it('shows the number as a copy fallback and reports a failed copy', async () => {
    const w = mountCard(driver({ phone: '87012345678' }))
    const number = w.find('.ride-driver__phone')
    expect(number.text()).toBe('+7 701 234 56 78')
    expect(number.attributes('aria-label')).toBe('Жүргізуші нөмірін көшіру')
    await number.trigger('click')
    await flushPromises()
    expect(copyText).toHaveBeenCalledExactlyOnceWith('+77012345678')
    expect(toast.items.at(-1)).toMatchObject({ tone: 'neutral', message: 'Көшірілді' })

    vi.mocked(copyText).mockResolvedValueOnce(false)
    await number.trigger('click')
    await flushPromises()
    expect(toast.items.at(-1)?.tone).toBe('error')
    w.unmount()
  })

  // The card sits in the sheet header, which drags the sheet: a tap whose
  // finger moves a few pixels must still reach the call link.
  it('keeps the call and the number out of the sheet drag', async () => {
    const w = mount(JSheet, {
      props: { snap: 'header', snapPoints: ['header', 'content'] },
      slots: { header: () => h(TripDriverCard, { driver: driver(), vehicle, photoUrl: null }), default: () => h('p', 'order') },
      global,
      attachTo: document.body,
    })
    const pointer = (target: EventTarget, type: string, y: number) => target.dispatchEvent(new PointerEvent(type, {
      bubbles: true, cancelable: true, pointerId: 5, pointerType: 'touch', isPrimary: true, clientX: 100, clientY: y,
    }))
    const tap = (el: Element): MouseEvent => {
      pointer(el, 'pointerdown', 600)
      pointer(window, 'pointermove', 590)
      pointer(window, 'pointerup', 590)
      const click = new MouseEvent('click', { bubbles: true, cancelable: true })
      el.dispatchEvent(click)
      return click
    }
    expect(tap(w.find('a[href^="tel:"]').element).defaultPrevented).toBe(false)
    tap(w.find('.ride-driver__phone').element)
    await flushPromises()
    expect(copyText).toHaveBeenCalledOnce()
    expect(w.emitted('update:snap')).toBeUndefined()
    w.unmount()
  })
})
