// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Owns the Telegram Mini App shell: SDK init, expanded/fullscreen presentation,
// safe areas, viewport height and keyboard detection. The result is a reactive
// state object plus CSS custom properties on :root that layouts use instead of
// 100vh/env() (ARCHITECTURE §14, §15):
//
//   --jt-safe-top      safe + content-safe top in fullscreen, content-safe top otherwise
//   --jt-safe-bottom   --jt-safe-left   --jt-safe-right
//   --jt-vh            stable viewport height in px (falls back to window.innerHeight)
//   --jt-vh-live       live viewport height in px
//   --jt-keyboard      on-screen keyboard overlap in px (0 when closed)
//
// Fullscreen policy: request once at start; never loop. A client reply of
// UNSUPPORTED is final for the session. Any other failure (e.g. timeout) gets a
// single retry on the next pointerup, because some clients only honour the
// request after a user gesture. When the app comes back to the foreground it is
// expanded again and, if it was fullscreen before it was deactivated, fullscreen
// is requested once more.

import { init, initData, isTMA, miniApp, retrieveLaunchParams, swipeBehavior, themeParams, viewport } from '@tma.js/sdk'
import { reactive, readonly } from 'vue'
import { errorMessage, errorName, safeBool, safeCall } from './safe'

export interface Insets {
  top: number
  right: number
  bottom: number
  left: number
}

export interface TelegramViewportState {
  /** Running inside a Telegram client with valid launch parameters. */
  isTelegram: boolean
  /** Telegram reports the Mini App as visible/active (always true in a browser). */
  isActive: boolean
  isFullscreen: boolean
  isExpanded: boolean
  /** Live viewport height, px. */
  viewportHeight: number
  /** Stable viewport height, px (does not change during expand/keyboard animations). */
  stableHeight: number
  /** Device safe area (notch, home indicator), px. */
  safeArea: Insets
  /** Telegram UI overlap inside the webview (fullscreen header controls), px. */
  contentSafeArea: Insets
  keyboardOpen: boolean
  /** Keyboard overlap, px (0 when closed). */
  keyboardHeight: number
  /** Telegram platform id (ios, android, tdesktop, macos, web, ...) or "browser". */
  platform: string
  /** Validated start parameter (ARCHITECTURE §18) or null. */
  startParam: string | null
  /** Mini Apps API version reported by the client, "" outside Telegram. */
  version: string
  /** The client answered UNSUPPORTED to a fullscreen request; it is never requested again. */
  fullscreenUnsupported: boolean
}

export type FullscreenResult = 'entered' | 'unsupported' | 'failed' | 'skipped'

export interface TelegramFullscreen {
  readonly state: Readonly<TelegramViewportState>
  /** Initialises the SDK and the shell. Idempotent; never throws. */
  start(): Promise<void>
  /** Removes every subscription and listener. */
  dispose(): void
  /** Requests fullscreen (deduplicated, honours the UNSUPPORTED rule). */
  requestFullscreen(): Promise<FullscreenResult>
  /** Called with false when the app is minimised/hidden and true when it returns. */
  onActiveChange(cb: (active: boolean) => void): () => void
}

const START_PARAM_RE = /^[A-Za-z0-9_-]{1,64}$/
const KEYBOARD_MIN_PX = 120
const VIEWPORT_MOUNT_TIMEOUT_MS = 3000
const FULLSCREEN_TIMEOUT_MS = 5000
/** How long start() waits for the fullscreen transition before rendering anyway. */
const FULLSCREEN_START_WAIT_MS = 1500
/** A fullscreen exit this close to deactivation is treated as caused by it. */
const FULLSCREEN_LOSS_GRACE_MS = 1000
const WHITE = '#FFFFFF'

const NON_TEXT_INPUTS = new Set(['checkbox', 'radio', 'button', 'submit', 'reset', 'range', 'color', 'file', 'image', 'hidden'])

/** Returns the start parameter when it matches §18's charset/length, else null. */
export function sanitizeStartParam(v: unknown): string | null {
  return typeof v === 'string' && START_PARAM_RE.test(v) ? v : null
}

function zeroInsets(): Insets {
  return { top: 0, right: 0, bottom: 0, left: 0 }
}

function copyInsets(i: Partial<Insets> | undefined): Insets {
  return { top: i?.top ?? 0, right: i?.right ?? 0, bottom: i?.bottom ?? 0, left: i?.left ?? 0 }
}

/**
 * Insets the UI must keep clear of, in px, as also written to --jt-safe-*.
 * In fullscreen the Telegram header controls float over the content, so the
 * content-safe area adds to the device safe area at the top. Outside Telegram
 * the CSS env() fallback applies and this returns zeros.
 */
export function effectiveSafeInsets(s: Readonly<TelegramViewportState>): Insets {
  if (!s.isTelegram) return zeroInsets()
  const a = s.safeArea
  const c = s.contentSafeArea
  return {
    top: s.isFullscreen ? a.top + c.top : c.top,
    right: a.right + c.right,
    bottom: a.bottom + c.bottom,
    left: a.left + c.left,
  }
}

function isEditable(el: Element | null): boolean {
  if (!el) return false
  if (el instanceof HTMLTextAreaElement) return !el.readOnly && !el.disabled
  if (el instanceof HTMLInputElement) return !NON_TEXT_INPUTS.has(el.type) && !el.readOnly && !el.disabled
  return el instanceof HTMLElement && el.isContentEditable
}

function px(n: number): string {
  return `${Math.max(0, Math.round(n))}px`
}

function isUnsupportedFullscreen(err: unknown): boolean {
  return errorName(err) === 'FullscreenFailedError' && errorMessage(err).includes('UNSUPPORTED')
}

function readUrlStartParam(win: Window): string | null {
  return safeCall(() => new URL(win.location.href).searchParams.get('startapp')) ?? null
}

/** Creates an independent controller. Apps use the singleton via useTelegramFullscreen(). */
export function createTelegramFullscreen(win: Window = window): TelegramFullscreen {
  const state = reactive<TelegramViewportState>({
    isTelegram: false,
    isActive: true,
    isFullscreen: false,
    isExpanded: true,
    viewportHeight: win.innerHeight,
    stableHeight: win.innerHeight,
    safeArea: zeroInsets(),
    contentSafeArea: zeroInsets(),
    keyboardOpen: false,
    keyboardHeight: 0,
    platform: 'browser',
    startParam: null,
    version: '',
    fullscreenUnsupported: false,
  })

  const cleanups: Array<() => void> = []
  const activeListeners = new Set<(active: boolean) => void>()
  let started = false
  let disposed = false
  let viewportMounted = false
  let inFlight: Promise<FullscreenResult> | null = null
  let gestureRetryArmed = false
  let gestureRetryUsed = false
  let fullscreenBeforeDeactivation = false
  let fullscreenLostAt = 0
  let editableFocused = false
  let baselineHeight = 0

  // --- CSS variables -------------------------------------------------------

  function applyCssVars(): void {
    const style = win.document.documentElement.style
    if (state.isTelegram) {
      const e = effectiveSafeInsets(state)
      style.setProperty('--jt-safe-top', px(e.top))
      style.setProperty('--jt-safe-bottom', px(e.bottom))
      style.setProperty('--jt-safe-left', px(e.left))
      style.setProperty('--jt-safe-right', px(e.right))
    } else {
      style.setProperty('--jt-safe-top', 'env(safe-area-inset-top, 0px)')
      style.setProperty('--jt-safe-bottom', 'env(safe-area-inset-bottom, 0px)')
      style.setProperty('--jt-safe-left', 'env(safe-area-inset-left, 0px)')
      style.setProperty('--jt-safe-right', 'env(safe-area-inset-right, 0px)')
    }
    style.setProperty('--jt-vh', px(state.stableHeight || win.innerHeight))
    style.setProperty('--jt-vh-live', px(state.viewportHeight || win.innerHeight))
    style.setProperty('--jt-keyboard', px(state.keyboardHeight))
  }

  // --- Viewport and keyboard ----------------------------------------------

  function updateKeyboard(): void {
    const vv = win.visualViewport
    // iOS: the layout viewport keeps its height and the visual viewport shrinks.
    const visualOverlap = vv ? Math.max(0, win.innerHeight - vv.height - vv.offsetTop) : 0
    // Android: the webview itself (and Telegram's viewport height) shrinks.
    const current = state.viewportHeight || win.innerHeight
    if (!editableFocused) baselineHeight = current
    const drop = Math.max(0, baselineHeight - current)
    const overlap = Math.max(visualOverlap, drop)
    const open = editableFocused && overlap >= KEYBOARD_MIN_PX
    state.keyboardOpen = open
    // Only the iOS-style overlap needs compensating: on Android the layout has
    // already shrunk above the keyboard.
    state.keyboardHeight = open ? visualOverlap : 0
  }

  function sync(): void {
    if (disposed) return
    if (state.isTelegram && viewportMounted) {
      const s = safeCall(() => viewport.state())
      if (s) {
        state.isFullscreen = s.isFullscreen
        state.isExpanded = s.isExpanded
        state.viewportHeight = s.height > 0 ? s.height : win.innerHeight
        state.stableHeight = s.stableHeight > 0 ? s.stableHeight : state.viewportHeight
        state.safeArea = copyInsets(s.safeAreaInsets)
        state.contentSafeArea = copyInsets(s.contentSafeAreaInsets)
      }
    } else {
      state.viewportHeight = win.visualViewport?.height ?? win.innerHeight
      state.stableHeight = win.innerHeight
    }
    updateKeyboard()
    applyCssVars()
  }

  function onFocusIn(e: FocusEvent): void {
    if (isEditable(e.target as Element | null)) {
      editableFocused = true
      sync()
    }
  }

  function onFocusOut(): void {
    // The next focus target is only known after the event loop turn.
    win.setTimeout(() => {
      editableFocused = isEditable(win.document.activeElement)
      sync()
    }, 0)
  }

  function listen<K extends string>(target: EventTarget | null | undefined, type: K, fn: EventListener, opts?: AddEventListenerOptions): void {
    if (!target) return
    target.addEventListener(type, fn, opts)
    cleanups.push(() => target.removeEventListener(type, fn, opts))
  }

  function attachDomListeners(): void {
    const onResize: EventListener = () => sync()
    listen(win, 'resize', onResize, { passive: true })
    listen(win.visualViewport, 'resize', onResize, { passive: true })
    listen(win.visualViewport, 'scroll', onResize, { passive: true })
    listen(win.document, 'focusin', onFocusIn as EventListener)
    listen(win.document, 'focusout', onFocusOut as EventListener)
  }

  // --- Fullscreen ----------------------------------------------------------

  function onGesturePointerUp(): void {
    gestureRetryArmed = false
    gestureRetryUsed = true
    void requestFullscreen()
  }

  function armGestureRetry(): void {
    if (gestureRetryArmed || gestureRetryUsed || state.fullscreenUnsupported || disposed) return
    gestureRetryArmed = true
    win.addEventListener('pointerup', onGesturePointerUp, { capture: true, once: true })
  }

  function disarmGestureRetry(): void {
    if (!gestureRetryArmed) return
    gestureRetryArmed = false
    win.removeEventListener('pointerup', onGesturePointerUp, { capture: true })
  }

  async function runFullscreenRequest(): Promise<FullscreenResult> {
    try {
      await viewport.requestFullscreen({ timeout: FULLSCREEN_TIMEOUT_MS })
      return 'entered'
    } catch (err) {
      if (isUnsupportedFullscreen(err)) {
        state.fullscreenUnsupported = true
        disarmGestureRetry()
        return 'unsupported'
      }
      if (import.meta.env.DEV) console.debug('[telegram] fullscreen request failed:', err)
      armGestureRetry()
      return 'failed'
    } finally {
      sync()
    }
  }

  function requestFullscreen(): Promise<FullscreenResult> {
    if (!state.isTelegram || disposed) return Promise.resolve('skipped')
    if (state.fullscreenUnsupported) return Promise.resolve('unsupported')
    if (safeBool(() => viewport.isFullscreen())) return Promise.resolve('entered')
    if (!safeBool(() => viewport.requestFullscreen.isAvailable())) return Promise.resolve('skipped')
    if (inFlight) return inFlight
    const p = runFullscreenRequest()
    inFlight = p
    void p.finally(() => {
      if (inFlight === p) inFlight = null
    })
    return p
  }

  // --- Activation ----------------------------------------------------------

  function onActiveChanged(active: boolean): void {
    if (active === state.isActive) return
    if (!active) {
      const recentlyLost = fullscreenLostAt > 0 && Date.now() - fullscreenLostAt < FULLSCREEN_LOSS_GRACE_MS
      fullscreenBeforeDeactivation = state.isFullscreen || recentlyLost
      state.isActive = false
    } else {
      state.isActive = true
      if (viewportMounted && !safeBool(() => viewport.isExpanded())) safeCall(() => viewport.expand.ifAvailable())
      const restore =
        fullscreenBeforeDeactivation && !state.fullscreenUnsupported && !safeBool(() => viewport.isFullscreen())
      fullscreenBeforeDeactivation = false
      if (restore) void requestFullscreen()
      sync()
    }
    for (const cb of activeListeners) safeCall(() => cb(active))
  }

  // --- Start ---------------------------------------------------------------

  function readLaunchParams(): void {
    const lp = safeCall(() => retrieveLaunchParams())
    state.platform = lp?.tgWebAppPlatform ?? 'unknown'
    state.version = lp?.tgWebAppVersion ?? ''
    const candidates = [safeCall(() => initData.startParam()), lp?.tgWebAppStartParam, readUrlStartParam(win)]
    state.startParam = candidates.map(sanitizeStartParam).find((v) => v !== null) ?? null
  }

  function applyColors(): void {
    safeCall(() => {
      if (!miniApp.setHeaderColor.isAvailable()) return
      // RGB header colours need Mini Apps 6.9; older clients only take theme keys.
      if (miniApp.setHeaderColor.supports('rgb')) miniApp.setHeaderColor(WHITE)
      else miniApp.setHeaderColor('bg_color')
    })
    safeCall(() => miniApp.setBgColor.ifAvailable(WHITE))
    safeCall(() => miniApp.setBottomBarColor.ifAvailable(WHITE))
  }

  async function mountViewport(): Promise<void> {
    if (!safeBool(() => viewport.mount.isAvailable())) return
    try {
      await viewport.mount({ timeout: VIEWPORT_MOUNT_TIMEOUT_MS })
    } catch (err) {
      if (import.meta.env.DEV) console.debug('[telegram] viewport mount failed:', err)
    }
    viewportMounted = safeBool(() => viewport.isMounted())
  }

  function subscribe(): void {
    const subs = [
      safeCall(() =>
        viewport.isFullscreen.sub((on, prev) => {
          if (prev && !on) fullscreenLostAt = Date.now()
          sync()
        }),
      ),
      safeCall(() => viewport.state.sub(() => sync())),
      safeCall(() => viewport.safeAreaInsets.sub(() => sync())),
      safeCall(() => viewport.contentSafeAreaInsets.sub(() => sync())),
      safeCall(() => miniApp.isActive.sub((active) => onActiveChanged(active))),
    ]
    for (const off of subs) if (off) cleanups.push(off)
  }

  function detectTelegram(): boolean {
    return safeBool(() => isTMA())
  }

  async function start(): Promise<void> {
    if (started || disposed) return
    started = true
    attachDomListeners()

    if (!detectTelegram()) {
      state.startParam = sanitizeStartParam(readUrlStartParam(win))
      sync()
      return
    }

    try {
      cleanups.push(init())
    } catch (err) {
      console.warn('[telegram] SDK init failed, continuing as a browser app', err)
      state.startParam = sanitizeStartParam(readUrlStartParam(win))
      sync()
      return
    }
    state.isTelegram = true

    safeCall(() => initData.restore())
    readLaunchParams()

    // themeParams must be mounted before miniApp (miniApp derives colours from it).
    safeCall(() => themeParams.mount.ifAvailable())
    safeCall(() => miniApp.mount.ifAvailable())
    applyColors()
    safeCall(() => miniApp.ready.ifAvailable())

    await mountViewport()
    safeCall(() => viewport.expand.ifAvailable())

    // Vertical swipes would minimise the app while the user pans the map.
    safeCall(() => swipeBehavior.mount.ifAvailable())
    safeCall(() => swipeBehavior.disableVertical.ifAvailable())

    subscribe()
    if (safeBool(() => miniApp.isMounted())) state.isActive = safeCall(() => miniApp.isActive()) ?? true
    sync()

    let timer: ReturnType<typeof setTimeout> | undefined
    await Promise.race([
      requestFullscreen(),
      new Promise<void>((resolve) => {
        timer = setTimeout(resolve, FULLSCREEN_START_WAIT_MS)
      }),
    ])
    clearTimeout(timer)
  }

  function dispose(): void {
    if (disposed) return
    disposed = true
    disarmGestureRetry()
    for (const fn of cleanups.splice(0).reverse()) safeCall(fn)
    activeListeners.clear()
    if (state.isTelegram) {
      safeCall(() => swipeBehavior.unmount())
      safeCall(() => miniApp.unmount())
      safeCall(() => themeParams.unmount())
    }
  }

  function onActiveChange(cb: (active: boolean) => void): () => void {
    activeListeners.add(cb)
    return () => activeListeners.delete(cb)
  }

  return { state: readonly(state) as Readonly<TelegramViewportState>, start, dispose, requestFullscreen, onActiveChange }
}

let singleton: TelegramFullscreen | null = null

/**
 * The app-wide Telegram shell controller. bootTelegram() starts it before the
 * first route renders; components read `state` and never start it themselves.
 */
export function useTelegramFullscreen(): TelegramFullscreen {
  singleton ??= createTelegramFullscreen()
  return singleton
}
