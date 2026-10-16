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
  // Shell first: safe areas and viewport height must be final before the first render.
  const tg = await bootTelegram()

  const app = createApp(App)
  const pinia = createPinia()
  app.use(pinia).use(i18n)

  const session = useSessionStore(pinia)
  // The router's entry guard needs the driver status, so sign in first.
  await session.bootstrap()
  app.use(router)

  const realtime = initRealtime({ getToken, refreshToken: reauthenticate })
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
  if (param) await router.replace(intentLocation(parseStartParam(param), router))

  app.mount('#app')
}

void main()
