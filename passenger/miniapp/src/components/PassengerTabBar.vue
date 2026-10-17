<!--
  Floating Liquid Glass tab bar of the passenger app's main sections:
  «Тапсырыс беру» (the map home), «Сапарым» (the current trip, or an empty
  state when there is none) and «Тарих» (order history). Switching replaces
  the current entry, like an iOS tab bar. Screens without a `tab` in their
  route meta (booking steps, confirmation) hide the bar, so it never covers
  their primary action.
-->
<script setup lang="ts">
import { CarFront, History, Route } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { JTabBar, type TabBarItem } from './ui'
import { ROUTE } from '../router/names'
import { useOrdersStore } from '../stores/orders'
import { useTelegramFullscreen } from '../telegram'

type Tab = 'order' | 'trip' | 'history'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const orders = useOrdersStore()
const tg = useTelegramFullscreen()

const current = computed<Tab>(() => route.meta.tab ?? 'order')
const hidden = computed(() => !route.meta.tab || tg.state.keyboardOpen)

const items = computed<TabBarItem<Tab>[]>(() => [
  { value: 'order', label: t('tabs.order'), icon: CarFront },
  { value: 'trip', label: t('tabs.trip'), icon: Route, badge: orders.active.length > 0 },
  { value: 'history', label: t('tabs.history'), icon: History },
])

function select(tab: Tab): void {
  if (tab === current.value) return
  if (tab === 'order') void router.replace({ name: ROUTE.home })
  else if (tab === 'history') void router.replace({ name: ROUTE.orders })
  else {
    const active = orders.active[0]
    void router.replace(active ? { name: ROUTE.order, params: { id: active.id } } : { name: ROUTE.trip })
  }
}
</script>

<template>
  <JTabBar :model-value="current" :items="items" :hidden="hidden" :aria-label="t('tabs.label')" @select="select" />
</template>
