// Shared with the other Mini App — keep both copies identical (make verify-shared).

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// --- @tma.js/sdk mock ---------------------------------------------------------
// Minimal stand-ins for the 3.3.0 objects the controller touches: callable
// signals with .sub(), and wrapped functions with isAvailable/ifAvailable.

const sdk = vi.hoisted(() => {
  type Listener<T> = (cur: T, prev: T) => void
  interface Sig<T> {
    (): T
    set(v: T): void
    sub(fn: Listener<T>): () => void
  }
  function signal<T>(initial: T): Sig<T> {
    let value = initial
    const subs = new Set<Listener<T>>()
    const s = (() => value) as Sig<T>
    s.set = (v: T) => {
      const prev = value
      value = v
      for (const fn of [...subs]) fn(v, prev)
    }
    s.sub = (fn) => {
      subs.add(fn)
      return () => subs.delete(fn)
    }
    return s
  }
  type AnyFn = (...args: never[]) => unknown
  function wrapped<F extends AnyFn>(impl: F) {
    const f = Object.assign(vi.fn(impl), {
      isAvailable: vi.fn(() => true),
      ifAvailable: vi.fn((...args: Parameters<F>) => ({ ok: true as const, data: f(...args) })),
      supports: vi.fn(() => true),
    })
    return f
  }

  const zero = { top: 0, right: 0, bottom: 0, left: 0 }
  const state = signal({
    height: 700,
    stableHeight: 700,
    width: 390,
    isExpanded: false,
    isFullscreen: false,
    safeAreaInsets: { ...zero },
    contentSafeAreaInsets: { ...zero },
  })
  const viewport = {
    state,
    isFullscreen: signal(false),
    safeAreaInsets: signal({ ...zero }),
    contentSafeAreaInsets: signal({ ...zero }),
    isMounted: vi.fn(() => true),
    isExpanded: vi.fn(() => state().isExpanded),
    mount: wrapped(async (_opts?: unknown) => undefined),
    expand: wrapped(() => {
      sdk.setViewport({ isExpanded: true })
    }),
    requestFullscreen: wrapped(async (_opts?: unknown): Promise<void> => {
      sdk.setViewport({ isFullscreen: true })
    }),
  }
  const miniApp = {
    isActive: signal(true),
    isMounted: vi.fn(() => true),
    mount: wrapped(() => undefined),
    unmount: vi.fn(),
    ready: wrapped(() => undefined),
    setHeaderColor: wrapped((_c: string) => undefined),
    setBgColor: wrapped((_c: string) => undefined),
    setBottomBarColor: wrapped((_c: string) => undefined),
  }
  const api = {
    isTMA: vi.fn(() => true),
    init: vi.fn(() => () => undefined),
    initData: { restore: vi.fn(), startParam: vi.fn((): string | undefined => undefined) },
    retrieveLaunchParams: vi.fn(() => ({ tgWebAppPlatform: 'ios', tgWebAppVersion: '8.0', tgWebAppStartParam: undefined as string | undefined })),
    themeParams: { mount: wrapped(() => undefined), unmount: vi.fn() },
    swipeBehavior: { mount: wrapped(() => undefined), disableVertical: wrapped(() => undefined), unmount: vi.fn() },
    miniApp,
    viewport,
    setViewport(patch: Partial<ReturnType<typeof state>>) {
      const next = { ...state(), ...patch }
      state.set(next)
      if (patch.isFullscreen !== undefined) viewport.isFullscreen.set(patch.isFullscreen)
      if (patch.safeAreaInsets) viewport.safeAreaInsets.set(patch.safeAreaInsets)
      if (patch.contentSafeAreaInsets) viewport.contentSafeAreaInsets.set(patch.contentSafeAreaInsets)
    },
    reset() {
      state.set({
        height: 700,
        stableHeight: 700,
        width: 390,
        isExpanded: false,
        isFullscreen: false,
        safeAreaInsets: { ...zero },
        contentSafeAreaInsets: { ...zero },
      })
      viewport.isFullscreen.set(false)
      miniApp.isActive.set(true)
    },
  }
  return api
})

vi.mock('@tma.js/sdk', () => ({
  init: sdk.init,
  initData: sdk.initData,
  isTMA: sdk.isTMA,
  miniApp: sdk.miniApp,
  retrieveLaunchParams: sdk.retrieveLaunchParams,
  swipeBehavior: sdk.swipeBehavior,
  themeParams: sdk.themeParams,
  viewport: sdk.viewport,
}))

const { createTelegramFullscreen, sanitizeStartParam } = await import('./useTelegramFullscreen')

function named(name: string, message: string): Error {
  const e = new Error(message)
  e.name = name
  return e
}

const unsupported = (): Error => named('FullscreenFailedError', 'UNSUPPORTED')
const timeout = (): Error => named('TimeoutError', 'Timeout reached: 5000ms')

function cssVar(name: string): string {
  return document.documentElement.style.getPropertyValue(name)
}

function pointerUp(): void {
  window.dispatchEvent(new Event('pointerup'))
}

async function settle(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

describe('useTelegramFullscreen', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sdk.reset()
    sdk.isTMA.mockReturnValue(true)
    sdk.viewport.requestFullscreen.mockImplementation(async () => {
      sdk.setViewport({ isFullscreen: true })
    })
    document.documentElement.removeAttribute('style')
    window.history.replaceState({}, '', '/passenger/')
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('initialises the SDK in order and requests fullscreen exactly once', async () => {
    const tg = createTelegramFullscreen()
    await tg.start()
    await tg.start() // idempotent

    expect(sdk.init).toHaveBeenCalledTimes(1)
    expect(sdk.initData.restore).toHaveBeenCalledTimes(1)
    const themeOrder = sdk.themeParams.mount.ifAvailable.mock.invocationCallOrder[0]!
    const miniAppOrder = sdk.miniApp.mount.ifAvailable.mock.invocationCallOrder[0]!
    expect(themeOrder).toBeLessThan(miniAppOrder)
    expect(sdk.miniApp.ready.ifAvailable).toHaveBeenCalled()
    expect(sdk.viewport.mount).toHaveBeenCalledTimes(1)
    expect(sdk.viewport.expand.ifAvailable).toHaveBeenCalled()
    expect(sdk.swipeBehavior.disableVertical.ifAvailable).toHaveBeenCalled()
    expect(sdk.miniApp.setHeaderColor).toHaveBeenCalledWith('#FFFFFF')
    expect(sdk.miniApp.setBgColor.ifAvailable).toHaveBeenCalledWith('#FFFFFF')
    expect(sdk.miniApp.setBottomBarColor.ifAvailable).toHaveBeenCalledWith('#FFFFFF')

    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)
    expect(tg.state.isTelegram).toBe(true)
    expect(tg.state.isFullscreen).toBe(true)

    // Further viewport events never trigger another request.
    sdk.setViewport({ height: 650 })
    pointerUp()
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)
    tg.dispose()
  })

  it('never retries after UNSUPPORTED', async () => {
    sdk.viewport.requestFullscreen.mockRejectedValue(unsupported())
    const tg = createTelegramFullscreen()
    await tg.start()
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)
    expect(tg.state.fullscreenUnsupported).toBe(true)

    pointerUp()
    await settle()
    sdk.miniApp.isActive.set(false)
    sdk.miniApp.isActive.set(true)
    await settle()
    await expect(tg.requestFullscreen()).resolves.toBe('unsupported')
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)
    expect(tg.state.isExpanded).toBe(true) // stays expanded
    tg.dispose()
  })

  it('retries a non-UNSUPPORTED failure once on the next pointerup', async () => {
    sdk.viewport.requestFullscreen.mockRejectedValueOnce(timeout()).mockRejectedValueOnce(timeout())
    const tg = createTelegramFullscreen()
    await tg.start()
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)

    pointerUp()
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(2)

    pointerUp()
    pointerUp()
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(2)
    expect(tg.state.fullscreenUnsupported).toBe(false)
    tg.dispose()
  })

  it('re-expands and re-requests fullscreen once on activation if it was fullscreen before', async () => {
    const tg = createTelegramFullscreen()
    const activeChanges: boolean[] = []
    tg.onActiveChange((a) => activeChanges.push(a))
    await tg.start()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)

    sdk.miniApp.isActive.set(false)
    // The client leaves fullscreen and collapses while the app is in the background.
    sdk.setViewport({ isFullscreen: false, isExpanded: false })
    sdk.viewport.expand.ifAvailable.mockClear()
    sdk.miniApp.isActive.set(true)
    await settle()

    expect(sdk.viewport.expand.ifAvailable).toHaveBeenCalledTimes(1)
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(2)
    expect(activeChanges).toEqual([false, true])

    // The user exits fullscreen while active: no request on the next activation.
    vi.useFakeTimers({ toFake: ['Date'] })
    sdk.setViewport({ isFullscreen: false })
    await settle()
    vi.setSystemTime(Date.now() + 5000)
    sdk.miniApp.isActive.set(false)
    sdk.miniApp.isActive.set(true)
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(2)
    tg.dispose()
  })

  it('treats a fullscreen exit just before deactivation as caused by it', async () => {
    const tg = createTelegramFullscreen()
    await tg.start()
    sdk.setViewport({ isFullscreen: false })
    sdk.miniApp.isActive.set(false)
    sdk.miniApp.isActive.set(true)
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(2)
    tg.dispose()
  })

  it('writes safe-area and viewport CSS variables', async () => {
    sdk.setViewport({
      stableHeight: 720,
      height: 690,
      safeAreaInsets: { top: 47, right: 0, bottom: 34, left: 0 },
      contentSafeAreaInsets: { top: 46, right: 0, bottom: 0, left: 0 },
    })
    const tg = createTelegramFullscreen()
    await tg.start()
    expect(tg.state.isFullscreen).toBe(true)
    expect(cssVar('--jt-safe-top')).toBe('93px')
    expect(cssVar('--jt-safe-bottom')).toBe('34px')
    expect(cssVar('--jt-safe-left')).toBe('0px')
    expect(cssVar('--jt-vh')).toBe('720px')
    expect(cssVar('--jt-vh-live')).toBe('690px')
    expect(cssVar('--jt-keyboard')).toBe('0px')

    // Leaving fullscreen: only the content-safe top remains.
    sdk.setViewport({ isFullscreen: false })
    expect(cssVar('--jt-safe-top')).toBe('46px')
    expect(tg.state.safeArea).toEqual({ top: 47, right: 0, bottom: 34, left: 0 })
    tg.dispose()
  })

  it('reads and validates the start parameter (initData first, then URL)', async () => {
    sdk.initData.startParam.mockReturnValue('order_7d9f2c1e-1111-4a2b-9c3d-123456789abc')
    const a = createTelegramFullscreen()
    await a.start()
    expect(a.state.startParam).toBe('order_7d9f2c1e-1111-4a2b-9c3d-123456789abc')
    expect(a.state.platform).toBe('ios')
    expect(a.state.version).toBe('8.0')
    a.dispose()

    sdk.initData.startParam.mockReturnValue('bad param!')
    window.history.replaceState({}, '', '/passenger/?startapp=taxi')
    const b = createTelegramFullscreen()
    await b.start()
    expect(b.state.startParam).toBe('taxi')
    b.dispose()
  })

  it('works outside Telegram with browser fallbacks and no SDK calls', async () => {
    sdk.isTMA.mockReturnValue(false)
    window.history.replaceState({}, '', '/passenger/?startapp=parcel')
    const tg = createTelegramFullscreen()
    await expect(tg.start()).resolves.toBeUndefined()
    expect(sdk.init).not.toHaveBeenCalled()
    expect(sdk.viewport.requestFullscreen).not.toHaveBeenCalled()
    expect(tg.state.isTelegram).toBe(false)
    expect(tg.state.platform).toBe('browser')
    expect(tg.state.startParam).toBe('parcel')
    expect(tg.state.stableHeight).toBe(window.innerHeight)
    expect(cssVar('--jt-safe-top')).toBe('env(safe-area-inset-top, 0px)')
    expect(cssVar('--jt-vh')).toBe(`${window.innerHeight}px`)
    await expect(tg.requestFullscreen()).resolves.toBe('skipped')
    tg.dispose()
  })

  it('falls back to browser mode when SDK init throws', async () => {
    sdk.init.mockImplementationOnce(() => {
      throw named('LaunchParamsRetrieveError', 'no launch params')
    })
    const tg = createTelegramFullscreen()
    await tg.start()
    expect(tg.state.isTelegram).toBe(false)
    expect(sdk.viewport.requestFullscreen).not.toHaveBeenCalled()
    tg.dispose()
  })

  it('stops reacting after dispose', async () => {
    sdk.viewport.requestFullscreen.mockRejectedValueOnce(timeout())
    const tg = createTelegramFullscreen()
    await tg.start()
    await settle()
    tg.dispose()
    pointerUp()
    sdk.miniApp.isActive.set(false)
    sdk.miniApp.isActive.set(true)
    await settle()
    expect(sdk.viewport.requestFullscreen).toHaveBeenCalledTimes(1)
  })
})

describe('sanitizeStartParam', () => {
  it.each([
    ['home', 'home'],
    ['order_abc-DEF_1', 'order_abc-DEF_1'],
    ['', null],
    ['a'.repeat(65), null],
    ['has space', null],
    ['<script>', null],
    [undefined, null],
  ])('%j → %j', (input, expected) => {
    expect(sanitizeStartParam(input)).toBe(expected)
  })
})
