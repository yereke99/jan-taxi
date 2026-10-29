import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { normalizeDetail } from '../../api/orders'
import { copyText } from '../../composables/clipboard'
import { i18n } from '../../i18n'
import { openExternal, openTelegram } from '../../telegram'
import JSheet from '../ui/JSheet.vue'
import { toast } from '../ui/toast'
import TripContacts from './TripContacts.vue'

vi.mock('../../composables/clipboard', () => ({ copyText: vi.fn(async () => true) }))
vi.mock('../../telegram', async (original) => ({
  ...await original<typeof import('../../telegram')>(),
  openExternal: vi.fn(() => true),
  openTelegram: vi.fn(() => true),
}))

const order = (overrides: Record<string, unknown> = {}) => normalizeDetail({
  id: 'o1', number: 5, status: 'accepted', is_mine: true, category: 'taxi', recipient_phone: null,
  passenger: { first_name: 'Айгерім', phone: '+77017654321', phone_verified: true, telegram_url: 'https://t.me/aigerim_k' },
  ...overrides,
})
const global = { plugins: [i18n] }

beforeEach(() => {
  vi.mocked(copyText).mockResolvedValue(true)
  vi.mocked(openExternal).mockReturnValue(true)
  vi.mocked(openTelegram).mockReturnValue(true)
  vi.clearAllMocks()
})
afterEach(() => toast.clear())

describe('active order contact actions', () => {
  it('keeps the native telephone tap synchronous and does not bubble to trip controls', () => {
    const parentClick = vi.fn()
    const Host = defineComponent({ setup: () => () => h('div', { onClick: parentClick }, h(TripContacts, { order: order() })) })
    const w = mount(Host, { global })
    const link = w.find('a[href^="tel:"]')
    const click = new MouseEvent('click', { bubbles: true, cancelable: true })
    link.element.dispatchEvent(click)
    expect(link.attributes('href')).toBe('tel:+77017654321')
    expect(link.attributes('target')).toBe('_blank')
    expect(click.defaultPrevented).toBe(false)
    expect(parentClick).not.toHaveBeenCalled()
    expect(openExternal).not.toHaveBeenCalled()
    w.unmount()
  })

  it('opens the exact private WhatsApp target in the same tap and preserves Telegram chat', async () => {
    const w = mount(TripContacts, { props: { order: order() }, global })
    const link = w.find('a[href^="https://wa.me/"]')
    const click = new MouseEvent('click', { bubbles: true, cancelable: true })
    link.element.dispatchEvent(click)
    expect(openExternal).toHaveBeenCalledExactlyOnceWith('https://wa.me/77017654321')
    expect(click.defaultPrevented).toBe(true)
    expect(link.attributes('href')).not.toContain('text=')
    await w.find('a[href="https://t.me/aigerim_k"]').trigger('click')
    expect(openTelegram).toHaveBeenCalledExactlyOnceWith('https://t.me/aigerim_k')
    w.unmount()
  })

  it('always offers the displayed passenger number as a copy fallback and reports failures', async () => {
    const w = mount(TripContacts, { props: { order: order() }, global })
    await w.find('.contacts__number').trigger('click')
    await flushPromises()
    expect(copyText).toHaveBeenCalledExactlyOnceWith('+77017654321')
    expect(toast.items.at(-1)?.tone).toBe('neutral')
    vi.mocked(copyText).mockResolvedValueOnce(false)
    await w.find('.contacts__number').trigger('click')
    await flushPromises()
    expect(toast.items.at(-1)?.tone).toBe('error')
    w.unmount()
  })

  it.each(['searching', 'cancelled', 'completed', 'disputed'])('withholds contact actions for %s even if stale contact data is present', (status) => {
    const w = mount(TripContacts, { props: { order: order({ status, recipient_phone: '+77012345678' }) }, global })
    expect(w.find('.contacts').exists()).toBe(false)
    w.unmount()
  })

  it('withholds all contacts from a driver who no longer owns the order', () => {
    const w = mount(TripContacts, { props: { order: order({ is_mine: false }) }, global })
    expect(w.find('.contacts').exists()).toBe(false)
    w.unmount()
  })

  it.each([
    { phone: '+770176543219', phone_verified: true },
    { phone: '+77017654321', phone_verified: false },
    { phone: null, phone_verified: false },
  ])('does not build contact links for an invalid, unverified or absent phone: %j', (passenger) => {
    const w = mount(TripContacts, { props: { order: order({ passenger: { first_name: 'Test', telegram_url: 'https://t.me/aigerim_k', ...passenger } }) }, global })
    expect(w.find('a[href^="tel:"]').exists()).toBe(false)
    expect(w.find('a[href^="https://wa.me/"]').exists()).toBe(false)
    expect(w.find('.contacts__number').exists()).toBe(false)
    expect(w.find('a[href="https://t.me/aigerim_k"]').exists()).toBe(true)
    w.unmount()
  })

  it('keeps contact taps outside sheet drag recognition', () => {
    const w = mount(JSheet, {
      props: { snap: 'half', snapPoints: ['half', 'full'] },
      slots: { default: () => h(TripContacts, { order: order() }) }, global, attachTo: document.body,
    })
    const link = w.find('a[href^="https://wa.me/"]').element
    const pointer = (target: EventTarget, type: string, y: number) => target.dispatchEvent(new PointerEvent(type, {
      bubbles: true, cancelable: true, pointerId: 3, pointerType: 'touch', isPrimary: true, clientX: 100, clientY: y,
    }))
    pointer(link, 'pointerdown', 500)
    pointer(window, 'pointermove', 490)
    pointer(window, 'pointerup', 490)
    link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    expect(openExternal).toHaveBeenCalledExactlyOnceWith('https://wa.me/77017654321')
    expect(w.emitted('update:snap')).toBeUndefined()
    w.unmount()
  })
})
