// "Latest request wins" runner for type-ahead search, reverse geocoding and
// route previews. Every schedule() gets a monotonically increasing id and
// aborts whatever is still pending, so a slow answer to an outdated edit can
// never overwrite the result of a newer one.

import { isAbortError } from '../services/http'

export interface LatestOptions<I, O> {
  run: (input: I, signal: AbortSignal) => Promise<O>
  /** Debounce before the request starts (ms); 0 starts on the next tick. */
  delayMs?: number
  /** The request for `input` actually started (after the debounce). */
  onStart?: (input: I, id: number) => void
  onResult: (output: O, input: I, id: number) => void
  onError?: (error: unknown, input: I, id: number) => void
  /** Injected timers (tests). */
  setTimer?: (fn: () => void, ms: number) => unknown
  clearTimer?: (handle: unknown) => void
}

export interface LatestRunner<I> {
  /** Supersedes everything pending and runs `input` after the debounce. */
  schedule(input: I, opts?: { immediate?: boolean }): number
  /** Drops the pending request without starting a new one. */
  cancel(): void
  /** Id of the newest scheduled request (0 before the first). */
  readonly latestId: number
  /** True from schedule() until the newest request settles or is cancelled. */
  readonly busy: boolean
}

export function createLatest<I, O>(opts: LatestOptions<I, O>): LatestRunner<I> {
  const setTimer = opts.setTimer ?? ((fn: () => void, ms: number) => setTimeout(fn, ms))
  const clearTimer = opts.clearTimer ?? ((h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>))
  let seq = 0
  let timer: unknown = null
  let controller: AbortController | null = null
  let busy = false

  function stopPending(): void {
    if (timer !== null) clearTimer(timer)
    timer = null
    controller?.abort()
    controller = null
  }

  function start(input: I, id: number): void {
    timer = null
    const ctrl = new AbortController()
    controller = ctrl
    opts.onStart?.(input, id)
    let promise: Promise<O>
    try {
      promise = opts.run(input, ctrl.signal)
    } catch (err) {
      promise = Promise.reject(err)
    }
    promise.then(
      (out) => {
        if (id !== seq || ctrl.signal.aborted) return
        controller = null
        busy = false
        opts.onResult(out, input, id)
      },
      (err: unknown) => {
        if (id !== seq || ctrl.signal.aborted || isAbortError(err)) return
        controller = null
        busy = false
        opts.onError?.(err, input, id)
      },
    )
  }

  return {
    schedule(input, o = {}) {
      stopPending()
      const id = ++seq
      busy = true
      const delay = o.immediate ? 0 : (opts.delayMs ?? 0)
      timer = setTimer(() => start(input, id), delay)
      return id
    },
    cancel() {
      seq++
      stopPending()
      busy = false
    },
    get latestId() {
      return seq
    },
    get busy() {
      return busy
    },
  }
}
