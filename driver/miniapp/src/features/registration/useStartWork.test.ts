// After finishing the registration the driver goes to work at once: a fresh
// GPS fix, online, the Orders tab — and lands on Orders whatever fails.

import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { normalizeRegistration } from '../../api/normalize'
import { useSessionStore } from '../../stores/session'
import { useStartWork } from './useStartWork'

const calls: string[] = []
const replace = vi.fn(async (to: { name: string }) => void calls.push(`open ${to.name}`))
const ensure = vi.fn(async () => {
  calls.push('gps')
  return true
})
const setOnline = vi.fn(async (on: boolean) => {
  calls.push(`online ${on}`)
  return { ok: true as const, online: on, withoutLocation: false }
})

vi.mock('vue-router', () => ({ useRouter: () => ({ replace }) }))
vi.mock('../location/useEnsureLocation', () => ({ useEnsureLocation: () => ({ ensure }) }))
vi.mock('../../stores/duty', () => ({ useDutyStore: () => ({ setOnline }) }))

const approved = normalizeRegistration({ id: 'd1', status: 'approved', code: 'KAZ001', first_name: 'Ерлан', iin: '900101300017' })

function signedIn(): ReturnType<typeof useSessionStore> {
  const session = useSessionStore()
  session.me = {
    id: 'u1',
    telegram_user_id: 1,
    first_name: 'Ерлан',
    last_name: '',
    username: null,
    language: 'kk',
    phone_masked: null,
    phone_verified: true,
    app: 'driver',
    driver: { id: 'd1', code: 'KAZ001', status: 'draft', is_online: false, city: null },
  }
  return session
}

describe('useStartWork', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    calls.length = 0
  })

  it('takes the approved driver to Orders and online with a fresh fix first', async () => {
    const session = signedIn()
    await useStartWork()(approved)
    expect(session.driverState).toBe('approved')
    expect(replace).toHaveBeenCalledWith({ name: 'orders' })
    expect(setOnline).toHaveBeenCalledWith(true)
    expect(calls.indexOf('gps')).toBeLessThan(calls.indexOf('online true'))
  })

  it('still opens Orders without location, and does not try to go online', async () => {
    signedIn()
    ensure.mockResolvedValueOnce(false)
    await useStartWork()(approved)
    expect(replace).toHaveBeenCalledWith({ name: 'orders' })
    expect(setOnline).not.toHaveBeenCalled()
  })

  it('still opens Orders when going online fails', async () => {
    signedIn()
    setOnline.mockRejectedValueOnce(new Error('offline'))
    await expect(useStartWork()(approved)).resolves.toBeUndefined()
    expect(replace).toHaveBeenCalledWith({ name: 'orders' })
  })

  it('leaves routing to the session when the answer is not an approval', async () => {
    signedIn()
    await useStartWork()(normalizeRegistration({ id: 'd1', status: 'pending' }))
    expect(replace).not.toHaveBeenCalled()
    expect(ensure).not.toHaveBeenCalled()
  })
})
