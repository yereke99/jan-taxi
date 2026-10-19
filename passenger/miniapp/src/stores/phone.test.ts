import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getMe, type Me } from '../api/me'
import { requestContact } from '../telegram'
import { PHONE_POLL_MS, PHONE_VERIFIED_EVENT, PHONE_WAIT_MS, readPhoneChoice, usePhoneStore } from './phone'
import { useSessionStore } from './session'

vi.mock('../telegram', () => ({ requestContact: vi.fn() }))
vi.mock('../api/me', () => ({ getMe: vi.fn(), updateLanguage: vi.fn() }))
vi.mock('../services/auth', () => ({ authenticate: vi.fn() }))

const handlers = new Map<string, () => void>()
vi.mock('../services/realtime', () => ({
  useRealtime: () => ({
    on: (type: string, h: () => void) => {
      handlers.set(type, h)
      return () => handlers.delete(type)
    },
  }),
}))

const contact = vi.mocked(requestContact)
const me = vi.mocked(getMe)

function user(verified: boolean): Me {
  return { id: 'u1', telegram_user_id: 1, first_name: 'Айгерім', last_name: '', username: null, language: 'kk', phone_masked: verified ? '+7 701 ••• •• 45' : null, phone_verified: verified }
}

function ready(verified = false) {
  const session = useSessionStore()
  session.status = 'ready'
  session.me = user(verified)
  return { session, phone: usePhoneStore() }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.useFakeTimers()
  localStorage.clear()
  handlers.clear()
  contact.mockReset()
  me.mockReset()
})

afterEach(() => vi.useRealTimers())

describe('phone sharing before the first order (§22.5)', () => {
  it('does not ask a passenger whose phone is verified', async () => {
    const { phone } = ready(true)
    expect(await phone.beforeOrder()).toBe(true)
    expect(phone.open).toBe(false)
    expect(me).not.toHaveBeenCalled()
  })

  it('re-reads /me first: a phone shared in the bot meanwhile skips the prompt', async () => {
    const { phone } = ready(false)
    me.mockResolvedValueOnce(user(true))
    await phone.beforeOrder()
    expect(phone.open).toBe(false)
  })

  it('"Not now" lets the order go ahead and is remembered', async () => {
    const { phone } = ready(false)
    me.mockResolvedValue(user(false))
    const pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    expect(phone.open).toBe(true)
    phone.skip()
    expect(await pending).toBe(true)
    expect(phone.open).toBe(false)
    expect(readPhoneChoice('u1')).toBe('skipped')
    // Not asked again on this device.
    await phone.beforeOrder()
    expect(phone.open).toBe(false)
  })

  it('shares through Telegram and waits until /me reports the verified phone', async () => {
    const { phone, session } = ready(false)
    me.mockResolvedValue(user(false))
    const pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    contact.mockResolvedValueOnce('sent')

    const shared = phone.share()
    await vi.advanceTimersByTimeAsync(0)
    expect(phone.flow).toBe('waiting')
    expect(phone.open).toBe(true)
    await vi.advanceTimersByTimeAsync(PHONE_POLL_MS)
    expect(phone.flow).toBe('waiting')

    // The bot stored the contact: the realtime event triggers the re-read.
    me.mockResolvedValue(user(true))
    handlers.get(PHONE_VERIFIED_EVENT)!()
    expect(await shared).toBe('verified')
    expect(await pending).toBe(true)
    expect(phone.open).toBe(false)
    expect(phone.flow).toBe('idle')
    expect(session.me?.phone_verified).toBe(true)
    expect(readPhoneChoice('u1')).toBe('shared')
    expect(handlers.has(PHONE_VERIFIED_EVENT)).toBe(false)
  })

  it('stops waiting after the limit and still lets the order go ahead', async () => {
    const { phone } = ready(false)
    me.mockResolvedValue(user(false))
    const pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    contact.mockResolvedValueOnce('sent')
    const shared = phone.share()
    await vi.advanceTimersByTimeAsync(PHONE_WAIT_MS + PHONE_POLL_MS)
    expect(await shared).toBe('timeout')
    await pending
    expect(phone.open).toBe(false)
  })

  it('"Not now" while waiting ends the wait at once', async () => {
    const { phone } = ready(false)
    me.mockResolvedValue(user(false))
    const pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    contact.mockResolvedValueOnce('sent')
    const shared = phone.share()
    await vi.advanceTimersByTimeAsync(0)
    phone.skip()
    expect(await shared).toBe('stopped')
    await pending
    expect(readPhoneChoice('u1')).toBe('shared')
  })

  it('leaving the screen closes the prompt without ordering or remembering', async () => {
    const { phone } = ready(false)
    me.mockResolvedValue(user(false))
    const pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    phone.abandon()
    expect(await pending).toBe(false)
    expect(phone.open).toBe(false)
    expect(readPhoneChoice('u1')).toBeNull()
  })

  it('declining in Telegram counts as skipping; a failed request asks again next time', async () => {
    const { phone } = ready(false)
    me.mockResolvedValue(user(false))
    contact.mockResolvedValueOnce('failed')
    let pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    expect(await phone.share()).toBe('failed')
    await pending
    expect(readPhoneChoice('u1')).toBeNull()

    contact.mockResolvedValueOnce('cancelled')
    pending = phone.beforeOrder()
    await vi.advanceTimersByTimeAsync(0)
    expect(phone.open).toBe(true)
    expect(await phone.share()).toBe('declined')
    await pending
    expect(readPhoneChoice('u1')).toBe('skipped')
  })
})
