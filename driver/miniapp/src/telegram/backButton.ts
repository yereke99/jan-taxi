// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Wires Telegram's native BackButton to vue-router. Route meta:
//   back: false        never show the button on this route
//   backTo: location   where "back" goes when there is no in-app history
//                      (e.g. a screen opened directly by a start parameter)
//   backReplace: true  always replace with backTo instead of history.back()
//                      (e.g. a live order must not go back to its booking form)

import { backButton } from '@tma.js/sdk'
import type { RouteLocationNormalizedLoaded, RouteLocationRaw, Router } from 'vue-router'
import { safeBool, safeCall } from './safe'

declare module 'vue-router' {
  interface RouteMeta {
    /** false hides the Telegram BackButton on this route. */
    back?: boolean
    /** Fallback target when there is no history entry to go back to. */
    backTo?: RouteLocationRaw
    /** With backTo: always navigate there (replace) instead of going back in history. */
    backReplace?: boolean
  }
}

/** vue-router keeps the history index in history.state.position. */
function historyPosition(): number {
  const pos = (window.history.state as { position?: unknown } | null)?.position
  return typeof pos === 'number' ? pos : 0
}

/**
 * Performs the same navigation as the Telegram BackButton (for in-page back
 * controls such as the floating map back button).
 */
export function navigateBack(router: Router): void {
  const route = router.currentRoute.value
  if (route.meta.backReplace && route.meta.backTo !== undefined) void router.replace(route.meta.backTo)
  else if (historyPosition() > 0) router.back()
  else if (route.meta.backTo !== undefined) void router.replace(route.meta.backTo)
}

export function shouldShowBack(route: RouteLocationNormalizedLoaded, position: number): boolean {
  if (route.meta.back === false) return false
  return position > 0 || route.meta.backTo !== undefined
}

/**
 * Shows/hides the Telegram BackButton on every navigation and handles its
 * clicks. Returns a disposer. No-op outside Telegram (the browser has its own
 * back navigation).
 */
export function useBackButton(router: Router): () => void {
  if (!safeBool(() => backButton.mount.isAvailable())) return () => {}
  safeCall(() => backButton.mount())

  const update = (route: RouteLocationNormalizedLoaded): void => {
    if (shouldShowBack(route, historyPosition())) safeCall(() => backButton.show.ifAvailable())
    else safeCall(() => backButton.hide.ifAvailable())
  }

  const onClick = (): void => navigateBack(router)

  const offClick = safeCall(() => backButton.onClick(onClick))
  const offAfterEach = router.afterEach((to) => update(to))
  update(router.currentRoute.value)

  return () => {
    offAfterEach()
    offClick?.()
    safeCall(() => backButton.hide.ifAvailable())
    safeCall(() => backButton.unmount())
  }
}
