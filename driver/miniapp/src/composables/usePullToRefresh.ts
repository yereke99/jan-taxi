// iOS-style pull-to-refresh on a scroll container: pulling down at the top
// past the threshold runs `refresh`; the indicator height follows the finger
// with resistance.

import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { haptics } from '../telegram'

export interface PullToRefresh {
  /** Current indicator height, px. */
  pull: Ref<number>
  /** True once the pull passed the threshold (release refreshes). */
  armed: Ref<boolean>
  refreshing: Ref<boolean>
}

const THRESHOLD = 64
const MAX_PULL = 110
const RESISTANCE = 0.5
const HOLD = 52

export function usePullToRefresh(el: Ref<HTMLElement | null | undefined>, refresh: () => Promise<unknown>): PullToRefresh {
  const pull = ref(0)
  const armed = ref(false)
  const refreshing = ref(false)
  let startY: number | null = null

  function onStart(e: TouchEvent): void {
    const target = el.value
    if (!target || refreshing.value || target.scrollTop > 0 || e.touches.length !== 1) {
      startY = null
      return
    }
    startY = e.touches[0]!.clientY
  }

  function onMove(e: TouchEvent): void {
    if (startY === null || refreshing.value) return
    const dy = e.touches[0]!.clientY - startY
    if (dy <= 0 || (el.value?.scrollTop ?? 0) > 0) {
      pull.value = 0
      armed.value = false
      return
    }
    // Keep the WebView from bouncing the whole page while we pull.
    if (e.cancelable) e.preventDefault()
    pull.value = Math.min(MAX_PULL, dy * RESISTANCE)
    const nowArmed = pull.value >= THRESHOLD
    if (nowArmed && !armed.value) haptics.impact('light')
    armed.value = nowArmed
  }

  async function onEnd(): Promise<void> {
    if (startY === null) return
    startY = null
    if (!armed.value) {
      pull.value = 0
      return
    }
    armed.value = false
    refreshing.value = true
    pull.value = HOLD
    try {
      await refresh()
    } finally {
      refreshing.value = false
      pull.value = 0
    }
  }

  function attach(target: HTMLElement): () => void {
    target.addEventListener('touchstart', onStart, { passive: true })
    target.addEventListener('touchmove', onMove, { passive: false })
    target.addEventListener('touchend', onEnd)
    target.addEventListener('touchcancel', onEnd)
    return () => {
      target.removeEventListener('touchstart', onStart)
      target.removeEventListener('touchmove', onMove)
      target.removeEventListener('touchend', onEnd)
      target.removeEventListener('touchcancel', onEnd)
    }
  }

  let detach: (() => void) | undefined
  const stop = watch(
    el,
    (target) => {
      detach?.()
      detach = target ? attach(target) : undefined
    },
    { immediate: true },
  )

  onBeforeUnmount(() => {
    stop()
    detach?.()
  })

  return { pull, armed, refreshing }
}
