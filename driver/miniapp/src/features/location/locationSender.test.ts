import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LocationSender, type Fix } from './locationSender'

function fix(n: number): Fix {
  return { lat: 48.82 + n / 1000, lon: 58.14, accuracy_m: 10, heading: null, speed_mps: null, timestamp: Date.now() }
}

describe('LocationSender', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  function setup(opts: { read?: () => Promise<Fix>; send?: (f: Fix) => Promise<void> } = {}) {
    let n = 0
    const read = vi.fn(opts.read ?? (() => Promise.resolve(fix(n++))))
    const send = vi.fn(opts.send ?? (() => Promise.resolve()))
    const issues: Array<string | null> = []
    const sender = new LocationSender({
      read,
      send,
      intervalMs: 6_000,
      minSendGapMs: 5_000,
      errorBackoffMs: 15_000,
      onIssue: (i) => issues.push(i),
    })
    return { sender, read, send, issues }
  }

  it('reads and sends immediately on start, then every interval', async () => {
    const { sender, read, send } = setup()
    sender.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(read).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(6_000)
    expect(send).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(12_000)
    expect(send).toHaveBeenCalledTimes(4)
    sender.stop()
  })

  it('never sends faster than the minimum gap (API: one per 2 s)', async () => {
    const { sender, send } = setup()
    const s = new LocationSender({
      read: () => Promise.resolve(fix(1)),
      send,
      intervalMs: 1_000,
      minSendGapMs: 5_000,
    })
    s.start()
    await vi.advanceTimersByTimeAsync(9_500)
    // t=0, t=5s (reads at 1..4 s are skipped), next would be t=10s.
    expect(send).toHaveBeenCalledTimes(2)
    s.stop()
    sender.stop()
  })

  it('pauses while the Mini App is hidden and resumes with an immediate fix', async () => {
    const { sender, read, send } = setup()
    sender.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(send).toHaveBeenCalledTimes(1)

    sender.setActive(false)
    expect(sender.status).toBe('paused')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(read).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledTimes(1)

    sender.setActive(true)
    expect(sender.status).toBe('running')
    await vi.advanceTimersByTimeAsync(0)
    expect(read).toHaveBeenCalledTimes(2)
    expect(send).toHaveBeenCalledTimes(2)
    sender.stop()
  })

  it('stops completely and is idempotent', async () => {
    const { sender, read } = setup()
    sender.start()
    sender.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(read).toHaveBeenCalledTimes(1)
    sender.stop()
    expect(sender.status).toBe('stopped')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(read).toHaveBeenCalledTimes(1)
  })

  it('never invents a position: without a fix nothing is sent and the issue is reported', async () => {
    const denied = Object.assign(new Error('denied'), { code: 'denied' })
    const { sender, send, read, issues } = setup({ read: () => Promise.reject(denied) })
    sender.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(send).not.toHaveBeenCalled()
    expect(issues).toEqual(['denied'])
    // Backs off before reading again.
    await vi.advanceTimersByTimeAsync(14_000)
    expect(read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(read).toHaveBeenCalledTimes(2)
    sender.stop()
  })

  it('reports outside_service_area from the API and keeps trying', async () => {
    const outside = Object.assign(new Error('outside'), { code: 'outside_service_area' })
    const { sender, send, issues } = setup({ send: () => Promise.reject(outside) })
    sender.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(issues).toEqual(['outside_service_area'])
    await vi.advanceTimersByTimeAsync(6_000)
    expect(send).toHaveBeenCalledTimes(2)
    sender.stop()
    expect(issues.at(-1)).toBeNull()
  })

  it('a failed send (429, network) does not stop reporting', async () => {
    let calls = 0
    const { sender, send } = setup({
      send: () => (calls++ === 0 ? Promise.reject(Object.assign(new Error('429'), { code: 'too_many_requests' })) : Promise.resolve()),
    })
    sender.start()
    await vi.advanceTimersByTimeAsync(6_000)
    expect(send).toHaveBeenCalledTimes(2)
    sender.stop()
  })
})
