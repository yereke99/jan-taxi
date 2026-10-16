<!--
  Floating glass tab bar of the driver app's main sections (§15.1): Home,
  Orders, Wallet, Profile. Tab switches replace the current entry (no back
  history between sections, like an iOS tab bar).
-->
<script setup lang="ts">
import { ClipboardList, House, UserRound, Wallet } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { JTabBar, type TabBarItem } from '../ui'
import { ROUTE } from '../../router/names'
import { useDutyStore } from '../../stores/duty'
import { useOrdersStore } from '../../stores/orders'
import { useTelegramFullscreen } from '../../telegram'

type Tab = 'home' | 'orders' | 'wallet' | 'profile'

const props = defineProps<{ visible: boolean }>()

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const orders = useOrdersStore()
const duty = useDutyStore()
const tg = useTelegramFullscreen()

const TARGET: Record<Tab, string> = {
  home: ROUTE.home,
  orders: ROUTE.orders,
  wallet: ROUTE.wallet,
  profile: ROUTE.profile,
}

const current = computed<Tab>(() => route.meta.tab ?? 'home')

const items = computed<TabBarItem<Tab>[]>(() => [
  { value: 'home', label: t('nav.home'), icon: House },
  { value: 'orders', label: t('nav.orders'), icon: ClipboardList, badge: duty.isOnline ? orders.count : 0 },
  { value: 'wallet', label: t('nav.wallet'), icon: Wallet },
  { value: 'profile', label: t('nav.profile'), icon: UserRound },
])

const hidden = computed(() => !props.visible || tg.state.keyboardOpen)

function select(tab: Tab): void {
  const name = TARGET[tab]
  if (route.name === name) return
  void router.replace({ name })
}
</script>

<template>
  <JTabBar :model-value="current" :items="items" :hidden="hidden" :aria-label="t('nav.main')" @select="select" />
</template>
