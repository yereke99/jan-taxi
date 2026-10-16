<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import DriverTabBar from './components/driver/DriverTabBar.vue'
import { JActionSheetHost, JButton, JEmptyState, JStatusBanner, JToast } from './components/ui'
import { useErrorText } from './i18n/core'
import { useRealtimeStatus } from './services/realtime'
import { useSessionStore } from './stores/session'
import { useOnlineStatus } from './utils/network'

/** Brief reconnects are normal (network handover); only show longer ones. */
const RECONNECT_BANNER_DELAY_MS = 2000

const { t } = useI18n()
const errorText = useErrorText()
const router = useRouter()
const route = useRoute()
const session = useSessionStore()
const online = useOnlineStatus()
const realtimeStatus = useRealtimeStatus()

// iOS-like push/pop direction from vue-router's history position.
const transitionName = ref('jt-fade')
let lastPosition = (window.history.state as { position?: number } | null)?.position ?? 0
const removeAfterEach = router.afterEach(() => {
  const pos = (window.history.state as { position?: number } | null)?.position ?? 0
  transitionName.value = pos > lastPosition ? 'jt-push' : pos < lastPosition ? 'jt-pop' : 'jt-fade'
  lastPosition = pos
})
onBeforeUnmount(removeAfterEach)

const showReconnecting = ref(false)
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
watch(
  realtimeStatus,
  (s) => {
    clearTimeout(reconnectTimer)
    if (s === 'reconnecting' || s === 'connecting') {
      reconnectTimer = setTimeout(() => (showReconnecting.value = true), RECONNECT_BANNER_DELAY_MS)
    } else {
      showReconnecting.value = false
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => clearTimeout(reconnectTimer))

const banner = computed(() => {
  if (!online.value) return { text: t('network.offline'), tone: 'danger' as const, busy: false }
  if (session.isReady && showReconnecting.value) return { text: t('network.reconnecting'), tone: 'neutral' as const, busy: true }
  return null
})

// Outside Telegram the app cannot authenticate; in development the UI still
// renders so screens can be built in a desktop browser.
/** The floating tab bar belongs to the main sections of an approved driver. */
const showTabBar = computed(() => session.isApproved && Boolean(route.meta.tab) && !route.meta.hideTabBar)

const blocked = computed(() => session.status === 'outside_telegram' && !import.meta.env.DEV)
const failed = computed(() => session.status === 'error')
const retrying = ref(false)

async function retry(): Promise<void> {
  retrying.value = true
  try {
    await session.bootstrap()
  } finally {
    retrying.value = false
  }
}
</script>

<template>
  <div class="jt-app">
    <div v-if="blocked" class="jt-app__state">
      <JEmptyState :title="t('app.not_in_telegram_title')" :description="t('app.not_in_telegram_text')" />
    </div>
    <div v-else-if="failed" class="jt-app__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="errorText(session.error)">
        <template #action>
          <JButton block :loading="retrying" @click="retry">{{ t('common.retry') }}</JButton>
        </template>
      </JEmptyState>
    </div>
    <RouterView v-else v-slot="{ Component, route }">
      <Transition :name="transitionName">
        <component :is="Component" :key="route.path" class="jt-page" />
      </Transition>
    </RouterView>

    <DriverTabBar v-if="!blocked && !failed" :visible="showTabBar" />

    <Transition name="jt-fade">
      <JStatusBanner v-if="banner" :text="banner.text" :tone="banner.tone" :busy="banner.busy" />
    </Transition>
    <JToast />
    <JActionSheetHost />
  </div>
</template>

<style scoped>
.jt-app {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--jt-color-bg);
}

.jt-app__state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  padding: var(--jt-safe-top) var(--jt-side-margin) var(--jt-safe-bottom);
}
</style>
