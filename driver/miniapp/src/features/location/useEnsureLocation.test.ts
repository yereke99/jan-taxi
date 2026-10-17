import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../services/http'
import { useLocationStore } from '../../stores/location'
import { LocationError } from '../../telegram'
import { useEnsureLocation } from './useEnsureLocation'

const FIX = { lat: 48.8264, lon: 58.1442, accuracy_m: 10, heading: null, speed_mps: null, timestamp: Date.now() }

describe('useEnsureLocation', () => {
  beforeEach(() => setActivePinia(createPinia()))

  function setup(read: () => Promise<typeof FIX>, send: () => Promise<void>) {
    const store = useLocationStore()
    vi.spyOn(store, 'locateOnce').mockImplementation(read)
    vi.spyOn(store, 'sendNow').mockImplementation(send)
    return { gps: useEnsureLocation(), store }
  }

  it('reads and sends a fix', async () => {
    const { gps, store } = setup(
      () => Promise.resolve(FIX),
      () => Promise.resolve(),
    )
    expect(await gps.ensure()).toBe(true)
    expect(store.sendNow).toHaveBeenCalledWith(FIX)
    expect(gps.state.value).toBe('idle')
  })

  it('a rate-limited send still means the API has a fresh fix', async () => {
    const { gps } = setup(
      () => Promise.resolve(FIX),
      () => Promise.reject(new ApiError(429, 'too_many_requests', 'slow down')),
    )
    expect(await gps.ensure()).toBe(true)
    expect(gps.state.value).toBe('idle')
  })

  it('says why it failed: denied, no fix, outside Kazakhstan', async () => {
    let r = setup(
      () => Promise.reject(new LocationError('denied')),
      () => Promise.resolve(),
    )
    expect(await r.gps.ensure()).toBe(false)
    expect(r.gps.state.value).toBe('denied')

    setActivePinia(createPinia())
    r = setup(
      () => Promise.reject(new LocationError('timeout')),
      () => Promise.resolve(),
    )
    await r.gps.ensure()
    expect(r.gps.state.value).toBe('failed')

    setActivePinia(createPinia())
    r = setup(
      () => Promise.resolve(FIX),
      () => Promise.reject(new ApiError(422, 'outside_service_area', 'outside')),
    )
    await r.gps.ensure()
    expect(r.gps.state.value).toBe('outside')
  })

  it('runs one read at a time', async () => {
    let release: () => void = () => undefined
    const read = vi.fn(() => new Promise<typeof FIX>((res) => (release = () => res(FIX))))
    const { gps } = setup(read, () => Promise.resolve())
    const a = gps.ensure()
    const b = gps.ensure()
    expect(gps.state.value).toBe('locating')
    release()
    expect(await a).toBe(true)
    expect(await b).toBe(true)
    expect(read).toHaveBeenCalledTimes(1)
  })
})
