import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createLatest } from './latest'

interface Deferred<T> {
  promise: Promise<T>
  resolve: (v: T) => void
  reject: (e: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('createLatest (route preview / search request guard)', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('debounces: only the last input within the delay is requested', async () => {
    const run = vi.fn((input: string) => Promise.resolve(`quote:${input}`))
    const onResult = vi.fn()
    const r = createLatest({ run, delayMs: 400, onResult })
    r.schedule('a')
    await vi.advanceTimersByTimeAsync(200)
    r.schedule('ab')
    await vi.advanceTimersByTimeAsync(399)
    expect(run).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(run).toHaveBeenCalledTimes(1)
    expect(run.mock.calls[0]![0]).toBe('ab')
    expect(onResult).toHaveBeenCalledWith('quote:ab', 'ab', 2)
  })

  it('aborts the in-flight request when a newer edit is scheduled', async () => {
    const signals: AbortSignal[] = []
    const pending: Array<Deferred<string>> = []
    const r = createLatest({
      run: (_: string, signal: AbortSignal) => {
        signals.push(signal)
        const d = deferred<string>()
        pending.push(d)
        return d.promise
      },
      onResult: vi.fn(),
    })
    r.schedule('first')
    await vi.advanceTimersByTimeAsync(0)
    expect(signals).toHaveLength(1)
    r.schedule('second')
    expect(signals[0]!.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(0)
    expect(signals[1]!.aborted).toBe(false)
  })

  it('never lets an outdated response overwrite a newer one', async () => {
    const first = deferred<string>()
    const second = deferred<string>()
    const queue = [first, second]
    const onResult = vi.fn()
    // run ignores the signal on purpose: the guard must hold even when a
    // transport resolves after an abort.
    const r = createLatest({ run: () => queue.shift()!.promise, onResult })
    r.schedule('old')
    await vi.advanceTimersByTimeAsync(0)
    r.schedule('new')
    await vi.advanceTimersByTimeAsync(0)
    second.resolve('NEW')
    await vi.advanceTimersByTimeAsync(0)
    first.resolve('OLD')
    await vi.advanceTimersByTimeAsync(0)
    expect(onResult).toHaveBeenCalledTimes(1)
    expect(onResult).toHaveBeenCalledWith('NEW', 'new', 2)
    expect(r.busy).toBe(false)
  })

  it('drops errors of superseded requests and reports the latest one', async () => {
    const first = deferred<string>()
    const second = deferred<string>()
    const queue = [first, second]
    const onError = vi.fn()
    const r = createLatest({ run: () => queue.shift()!.promise, onResult: vi.fn(), onError })
    r.schedule('a')
    await vi.advanceTimersByTimeAsync(0)
    r.schedule('b')
    await vi.advanceTimersByTimeAsync(0)
    first.reject(new Error('old failure'))
    second.reject(new Error('new failure'))
    await vi.advanceTimersByTimeAsync(0)
    expect(onError).toHaveBeenCalledTimes(1)
    expect((onError.mock.calls[0]![0] as Error).message).toBe('new failure')
  })

  it('ignores AbortError from the transport and cancel() drops the pending result', async () => {
    const d = deferred<string>()
    const onResult = vi.fn()
    const onError = vi.fn()
    const r = createLatest({ run: () => d.promise, onResult, onError })
    r.schedule('x')
    await vi.advanceTimersByTimeAsync(0)
    r.cancel()
    d.reject(new DOMException('Aborted', 'AbortError'))
    await vi.advanceTimersByTimeAsync(0)
    expect(onResult).not.toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()
    expect(r.busy).toBe(false)
  })

  it('assigns monotonically increasing request ids', () => {
    const r = createLatest({ run: () => Promise.resolve(1), onResult: vi.fn() })
    const a = r.schedule(1)
    const b = r.schedule(2)
    r.cancel()
    const c = r.schedule(3)
    expect(a).toBeLessThan(b)
    expect(b).toBeLessThan(c)
  })
})
