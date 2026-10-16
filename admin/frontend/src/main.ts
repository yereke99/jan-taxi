import './styles/tokens.css'
import './styles/base.css'

import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { i18n } from './i18n'
import { router } from './router'
import { setUnauthorizedHandler } from './services/api'
import { useAuthStore } from './stores/auth'

async function main(): Promise<void> {
  const app = createApp(App)
  const pinia = createPinia()
  app.use(pinia).use(i18n).use(router)

  const auth = useAuthStore(pinia)
  // An API 401 while signed in means the session expired (idle/absolute
  // timeout or revoked): drop it locally and return to the login page.
  setUnauthorizedHandler(() => {
    if (!auth.isAuthenticated) return
    auth.clear()
    const current = router.currentRoute.value.fullPath
    void router.replace({ name: 'login', query: { reason: 'expired', redirect: current } })
  })

  await router.isReady()
  app.mount('#app')
}

void main()
