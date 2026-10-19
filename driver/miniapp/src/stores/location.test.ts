// One device location source: the live stream for the following map is a
// single watcher shared by all users, throttled to about one fix per second,
// paused while the Mini App is in the background, and reused by the reporting
// instead of a second GPS read.

import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DeviceLocation } from '../telegram'
import { LIVE_MIN_GAP_MS, useLocationStore } from './location'

const tg = vi.hoisted(() => ({
  listeners: [] as Array<(active: boolean) => void>,
  state: null as unknown as { isActive: boolean; isTelegram: boolean },
}))

const watch = vi.hoisted(() => ({
  calls: [] as Array<{ onFix: (l: DeviceLocation) => void; onError: (e: unknown) => void; prefer: string | undefined; stop: ReturnType<typeof vi.fn> }>,
}))

vi.mock('../telegram', async (importOriginal) => {
  const real = await importOriginal<typeof import('../telegram')>()
  const { reactive: r } = await import('vue')
  tg.state = r({ isActive: true, isTelegram: true })
  return {
    ...real,
    getLocation: vi.fn(),
    watchLocation: vi.fn((onFix: (l: DeviceLocation) => void, onError: (e: unknown) => void, opts?: { prefer?: string }) => {
      const stop = vi.fn()
      watch.calls.push({ onFix, onError, prefer: opts?.prefer, stop })
      return stop
    }),
    useTelegramFullscreen: () => ({
      state: tg.state,
      onActiveChange: (cb: (active: boolean) => void) => {
        tg.listeners.push(cb)
        return () => undefined
      },
    }),
  }
})
vi.mock('../api/driver', () => ({ reportLocation: vi.fn().mockResolvedValue(undefined) }))

const telegram = await import('../telegram')
const api = await import('../api/driver')
const getLocation = vi.mocked(telegram.getLocation)
const reportLocation = vi.mocked(api.reportLocation)

function loc(over: Partial<DeviceLocation> = {}): DeviceLocation {
  return { lat: 48.82, lon: 58.14, accuracy_m: 8, heading: null, speed_mps: null, timestamp: Date.now(), source: 'telegram', ...over }
}

function setActive(active: boolean): void {
  tg.state.isActive = active
  for (const l of tg.listeners) l(active)
}

beforeEach(() => {
  vi.useFakeTimers()
  setActivePinia(createPinia())
  watch.calls.length = 0
  tg.listeners.length = 0
  tg.state.isActive = true
  getLocation.mockReset()
  reportLocation.mockClear()
  getLocation.mockImplementation(async () => loc())
})

afterEach(() => {
  vi.useRealTimers()
})

describe('live location stream', () => {
  it('starts only after a provider has worked, with that provider', async () => {
    const store = useLocationStore()
    const release = store.acquireLive()
    expect(watch.calls).toHaveLength(0) // no prompt loop before a successful read
    await store.locateOnce()
    expect(watch.calls).toHaveLength(1)
    expect(watch.calls[0]!.prefer).toBe('telegram')
    expect(store.live).toBe(true)
    release()
    expect(watch.calls[0]!.stop).toHaveBeenCalledTimes(1)
    expect(store.live).toBe(false)
  })

  it('shares one watcher between users', async () => {
    const store = useLocationStore()
    await store.locateOnce()
    const a = store.acquireLive()
    const b = store.acquireLive()
    expect(watch.calls).toHaveLength(1)
    a()
    a() // releasing twice is harmless
    expect(watch.calls[0]!.stop).not.toHaveBeenCalled()
    b()
    expect(watch.calls[0]!.stop).toHaveBeenCalledTimes(1)
  })

  it('publishes at most one fix per second, keeping the newest of a burst', async () => {
    const store = useLocationStore()
    await store.locateOnce()
    store.acquireLive()
    const { onFix } = watch.calls[0]!
    vi.advanceTimersByTime(LIVE_MIN_GAP_MS)
    onFix(loc({ lat: 48.8201 }))
    expect(store.lastFix?.lat).toBe(48.8201)
    onFix(loc({ lat: 48.8202 }))
    onFix(loc({ lat: 48.8203 }))
    expect(store.lastFix?.lat).toBe(48.8201)
    vi.advanceTimersByTime(LIVE_MIN_GAP_MS)
    expect(store.lastFix?.lat).toBe(48.8203)
  })

  it('stops while the Mini App is in the background and resumes after', async () => {
    const store = useLocationStore()
    await store.locateOnce()
    store.acquireLive()
    setActive(false)
    expect(watch.calls[0]!.stop).toHaveBeenCalledTimes(1)
    expect(store.live).toBe(false)
    setActive(true)
    expect(watch.calls).toHaveLength(2)
    expect(store.live).toBe(true)
  })

  it('stops after a denial until a read works again', async () => {
    const store = useLocationStore()
    await store.locateOnce()
    store.acquireLive()
    watch.calls[0]!.onError(new telegram.LocationError('denied'))
    expect(watch.calls[0]!.stop).toHaveBeenCalledTimes(1)
    expect(store.issue).toBe('denied')
    await store.locateOnce()
    expect(watch.calls).toHaveLength(2)
  })

  it('reporting reuses the live fix instead of reading the GPS again', async () => {
    const store = useLocationStore()
    await store.locateOnce()
    store.acquireLive()
    expect(getLocation).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(LIVE_MIN_GAP_MS)
    watch.calls[0]!.onFix(loc({ lat: 48.83 }))
    store.setReporting(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(getLocation).toHaveBeenCalledTimes(1)
    expect(reportLocation).toHaveBeenCalledTimes(1)
    expect(reportLocation.mock.calls[0]![0]).toMatchObject({ lat: 48.83 })
    store.setReporting(false)
  })

  it('without the live stream, reporting reads the GPS itself', async () => {
    const store = useLocationStore()
    store.setReporting(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(getLocation).toHaveBeenCalledTimes(1)
    expect(reportLocation).toHaveBeenCalledTimes(1)
    store.setReporting(false)
  })
})
