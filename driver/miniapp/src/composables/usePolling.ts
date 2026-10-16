// Polling fallback for screens fed by realtime hints: runs `fn` every
// `intervalMs` while the screen is mounted and the Mini App is visible, and
// once right away when it becomes visible again.

import { onBeforeUnmount, onMounted } from 'vue'
import { useTelegramFullscreen } from '../telegram'

export function usePolling(fn: () => unknown, intervalMs: number, opts: { immediate?: boolean } = {}): void {
  const tg = useTelegramFullscreen()
  let timer: ReturnType<typeof setInterval> | undefined
  let offActive: (() => void) | undefined

  const visible = (): boolean => (typeof document === 'undefined' || document.visibilityState !== 'hidden') && tg.state.isActive

  function stop(): void {
    clearInterval(timer)
    timer = undefined
  }

  function start(): void {
    stop()
    if (visible()) timer = setInterval(() => void fn(), intervalMs)
  }

  function onVisibility(): void {
    if (visible()) {
      void fn()
      start()
    } else {
      stop()
    }
  }

  onMounted(() => {
    if (opts.immediate) void fn()
    start()
    document.addEventListener('visibilitychange', onVisibility)
    offActive = tg.onActiveChange(onVisibility)
  })

  onBeforeUnmount(() => {
    stop()
    document.removeEventListener('visibilitychange', onVisibility)
    offActive?.()
  })
}
