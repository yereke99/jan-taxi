// The online card always shows the availability the API confirmed: the
// switch, the title and its colour can never disagree, a request in flight is
// shown as such, and a failed or stale state is re-read from GET /me.

import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import * as driverApi from '../../api/driver'
import * as meApi from '../../api/me'
import type { Me } from '../../api/me'
import type { Availability } from '../../api/types'
import { i18n } from '../../i18n'
import { useDutyStore } from '../../stores/duty'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import OnlineCard from './OnlineCard.vue'

vi.mock('../../api/driver', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../api/driver')>()
  return { ...real, setAvailability: vi.fn(), reportLocation: vi.fn().mockResolvedValue(undefined) }
})
vi.mock('../../api/me', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../api/me')>()
  return { ...real, getMe: vi.fn() }
})

const setAvailability = vi.mocked(driverApi.setAvailability)
const getMe = vi.mocked(meApi.getMe)

function me(online: boolean): Me {
  return {
    id: 'u1',
    telegram_user_id: 1,
    first_name: 'Асан',
    last_name: 'Сериков',
    username: null,
    language: 'kk',
    phone_masked: null,
    phone_verified: true,
    app: 'driver',
    driver: { id: 'd1', code: 'EMB028', status: 'approved', is_online: online, city: null },
  }
}

function availability(online: boolean): Availability {
  return { is_online: online, online_since: null, can_go_online: true, blocked_reason: null, available: 500, min_online_balance: 0, currency: 'KZT' }
}

let pinia: Pinia

async function mountCard(online: boolean, serverOnline = online) {
  const session = useSessionStore()
  session.me = me(online)
  session.status = 'ready'
  getMe.mockResolvedValue(me(serverOnline))
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
  const w = mount(OnlineCard, { global: { plugins: [pinia, i18n, router] } })
  await flushPromises()
  return { w, session }
}

function shown(w: Awaited<ReturnType<typeof mountCard>>['w']) {
  const card = w.find('.online')
  return {
    title: w.find('.online__title').text(),
    tone: ['is-online', 'is-offline', 'is-pending'].find((c) => card.classes().includes(c)),
    checked: w.find('[role="switch"]').attributes('aria-checked'),
  }
}

beforeEach(() => {
  pinia = createPinia()
  setActivePinia(pinia)
  i18n.global.locale.value = 'kk'
  setAvailability.mockReset()
  getMe.mockReset()
})

describe('OnlineCard', () => {
  it('online: «Желіде» in green with the switch on', async () => {
    const { w } = await mountCard(true)
    expect(shown(w)).toEqual({ title: 'Желіде', tone: 'is-online', checked: 'true' })
  })

  it('offline: «Желіде емес» in neutral gray with the switch off', async () => {
    const { w } = await mountCard(false)
    expect(shown(w)).toEqual({ title: 'Желіде емес', tone: 'is-offline', checked: 'false' })
  })

  it('re-reads the real state when it appears (switched offline by the idle timeout)', async () => {
    const { w } = await mountCard(true, false)
    expect(getMe).toHaveBeenCalledTimes(1)
    expect(shown(w)).toEqual({ title: 'Желіде емес', tone: 'is-offline', checked: 'false' })
  })

  it('shows a request in flight as pending, never flips the switch before the API answers', async () => {
    const { w } = await mountCard(true)
    let answer: (a: Availability) => void = () => undefined
    setAvailability.mockReturnValue(new Promise((resolve) => (answer = resolve)))
    await w.find('[role="switch"]').trigger('click')
    expect(setAvailability).toHaveBeenCalledWith(false)
    expect(shown(w)).toEqual({ title: 'Желіден шығуда…', tone: 'is-pending', checked: 'true' })
    answer(availability(false))
    await flushPromises()
    expect(shown(w)).toEqual({ title: 'Желіде емес', tone: 'is-offline', checked: 'false' })
  })

  it('after a failed request shows what the server really has', async () => {
    const { w } = await mountCard(true)
    // The PATCH timed out, but the server had switched the driver off.
    setAvailability.mockRejectedValue(new TypeError('Failed to fetch'))
    getMe.mockResolvedValue(me(false))
    await w.find('[role="switch"]').trigger('click')
    await flushPromises()
    expect(getMe).toHaveBeenCalledTimes(2)
    expect(shown(w)).toEqual({ title: 'Желіде емес', tone: 'is-offline', checked: 'false' })
  })

  it('a read that started before a confirmed toggle does not overwrite it', async () => {
    const { w } = await mountCard(false)
    let late: (m: Me) => void = () => undefined
    getMe.mockReturnValue(new Promise((resolve) => (late = resolve)))
    void useDutyStore().reconcile() // e.g. a resync after reconnecting
    setAvailability.mockResolvedValue(availability(true))
    await w.find('[role="switch"]').trigger('click')
    await flushPromises()
    expect(shown(w)).toEqual({ title: 'Желіде', tone: 'is-online', checked: 'true' })
    late(me(false)) // answered before the PATCH was applied
    await flushPromises()
    expect(shown(w)).toEqual({ title: 'Желіде', tone: 'is-online', checked: 'true' })
  })

  it('re-reads when the order list says offline while the card shows online', async () => {
    const { w } = await mountCard(true)
    getMe.mockResolvedValue(me(false))
    useOrdersStore().eligibility = { eligible: false, reason: 'offline' }
    await flushPromises()
    expect(getMe).toHaveBeenCalledTimes(2)
    expect(shown(w)).toEqual({ title: 'Желіде емес', tone: 'is-offline', checked: 'false' })
  })

  it('uses the Russian and English labels too', async () => {
    const { w } = await mountCard(true)
    i18n.global.locale.value = 'ru'
    await flushPromises()
    expect(shown(w).title).toBe('На линии')
    i18n.global.locale.value = 'en'
    await flushPromises()
    expect(shown(w).title).toBe('Online')
  })
})
