import './styles/tokens.css'
import './styles/base.css'
import './styles/transitions.css'

import { createPinia } from 'pinia'
import { createApp, watch } from 'vue'
import App from './App.vue'
import { installDriverRuntime } from './app/runtime'
import { i18n } from './i18n'
import { router } from './router'
import { intentLocation, parseStartParam } from './router/startParam'
import { getToken, reauthenticate } from './services/auth'
import { initRealtime } from './services/realtime'
import { useSessionStore } from './stores/session'
import { useTripStore } from './stores/trip'
import { bootTelegram, useBackButton } from './telegram'

const START_HANDLED_KEY = 'jt.driver.start_param_handled'

/** A start parameter is applied once per launch, not again on WebView reloads. */
function takeStartParam(param: string | null): string | null {
  if (!param) return null
  try {
    if (sessionStorage.getItem(START_HANDLED_KEY) === param) return null
    sessionStorage.setItem(START_HANDLED_KEY, param)
  } catch {
    // Storage unavailable: apply it; a reload re-applying it is harmless.
  }
  return param
}

async function main(): Promise<void> {
  // Presentation build only: a mocked Telegram client and in-memory API. The
  // condition is a build-time constant, so production bundles omit the import.
  const demo = import.meta.env.VITE_DEMO === '1' ? await import('./demo') : null
  const demoRuntime = demo?.installDemo() ?? null

  // Shell first: safe areas and viewport height must be final before the first render.
  const tg = await bootTelegram()

  const app = createApp(App)
  const pinia = createPinia()
  app.use(pinia).use(i18n)

  const session = useSessionStore(pinia)
  // The router's entry guard needs the driver status, so sign in first.
  await session.bootstrap()
  app.use(router)

  const realtime = initRealtime({ getToken, refreshToken: reauthenticate, createSocket: demoRuntime?.createSocket })
  // Also covers a session that becomes ready later (retry after a failed start).
  watch(
    () => session.isReady,
    (ready) => {
      if (ready) realtime.start()
    },
    { immediate: true },
  )
  // Returning from 2GIS, Kaspi or the chat: reconnect if needed and re-fetch.
  tg.onActiveChange((active) => {
    if (active) realtime.notifyForeground()
  })
  installDriverRuntime(realtime, router)

  useBackButton(router)
  await router.isReady()
  const param = takeStartParam(tg.state.startParam)
  if (param) {
    const intent = parseStartParam(param)
    let activeOrderId: string | null = null
    if (intent.kind === 'order' && session.isApproved) {
      // The driver's own trip (opened from the bot after «Алу») goes straight to the trip screen.
      const trip = useTripStore(pinia)
      await trip.refresh()
      activeOrderId = trip.active?.id ?? null
    }
    await router.replace(intentLocation(intent, router, activeOrderId))
  }
  if (demo) await demo.openDemoScreen(pinia, router)

  app.mount('#app')
}

void main()
