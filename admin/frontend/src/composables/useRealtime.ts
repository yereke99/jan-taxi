// One realtime connection for the whole admin session. The layout starts it
// after sign-in; screens subscribe with useRealtimeEvents/useResync and get
// unsubscribed automatically when they unmount.

import { onBeforeUnmount, readonly, ref } from 'vue'
import { api, isApiError } from '../services/api'
import { AdminRealtime, type RealtimeEvent, type RealtimeStatus } from '../services/realtime'

const status = ref<RealtimeStatus>('idle')

export const realtime = new AdminRealtime({
  // A policy close may mean the session ended: /admin/auth/me answers 401 in
  // that case, which also triggers the global sign-out handler.
  checkSession: async () => {
    try {
      await api.get('/admin/auth/me')
      return true
    } catch (err) {
      return !(isApiError(err) && err.status === 401)
    }
  },
})
realtime.onStatus((s) => {
  status.value = s
})

export const realtimeStatus = readonly(status)

/**
 * Calls handler for the given event types (debounced, so a burst of events
 * triggers one refresh). Unsubscribes on unmount.
 */
export function useRealtimeEvents(types: readonly string[], handler: (ev: RealtimeEvent) => void, debounceMs = 400): void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let last: RealtimeEvent | null = null
  const fire = (ev: RealtimeEvent): void => {
    last = ev
    if (debounceMs <= 0) {
      handler(ev)
      return
    }
    clearTimeout(timer)
    timer = setTimeout(() => {
      if (last) handler(last)
    }, debounceMs)
  }
  const offs = types.map((t) => realtime.on(t, fire))
  onBeforeUnmount(() => {
    clearTimeout(timer)
    for (const off of offs) off()
  })
}

/** Re-fetch hook for reconnects and returning to the tab. */
export function useResync(cb: () => void): void {
  const off = realtime.onResync(cb)
  onBeforeUnmount(off)
}
