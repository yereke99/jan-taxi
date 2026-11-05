// Shared with the other Mini App — keep both copies identical (make verify-shared).
// Foreground-only heartbeat: an active Mini App shows the gift instead of
// receiving a duplicate campaign announcement in its bot. Failures are silent.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { http } from '../services/http'
import { useTelegramFullscreen } from './useTelegramFullscreen'

export function usePromotionPresence(ready: () => boolean, endpoint: string,
  position?: () => { lat: number; lon: number } | null, updated?: () => void): void {
  const tg = useTelegramFullscreen()
  const visible = ref(document.visibilityState !== 'hidden')
  const active = computed(() => ready() && visible.value && tg.state.isActive)
  let timer: ReturnType<typeof setInterval> | undefined
  let lastLocationPing = 0
  const positionKey = computed(() => { const p = position?.(); return p ? `${p.lat.toFixed(3)},${p.lon.toFixed(3)}` : '' })
  const ping = (on: boolean) => {
    const p = on ? position?.() : null
    if (p) lastLocationPing = Date.now()
    void http.post(endpoint, { active: on, ...(p ? { position: { lat: p.lat, lon: p.lon } } : {}) }, { timeoutMs: 5000 })
      .then(() => { if (on && ready()) updated?.() }).catch(() => undefined)
  }
  const visibility = () => { visible.value = document.visibilityState !== 'hidden' }
  watch(active, (on, before) => {
    clearInterval(timer)
    if (ready() || before) ping(on)
    if (on) timer = setInterval(() => ping(true), 45_000)
  }, { immediate: true })
  watch(positionKey, (key, before) => {
    if (active.value && key && (!before || Date.now() - lastLocationPing >= 30_000)) ping(true)
  })
  onMounted(() => document.addEventListener('visibilitychange', visibility))
  onBeforeUnmount(() => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', visibility)
    if (ready()) ping(false)
  })
}
