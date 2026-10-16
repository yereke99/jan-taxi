<!--
  My orders: active orders on top, then the history with cursor pagination
  (loads the next page when the end of the list scrolls into view, with a
  button as a fallback). Lists refresh on realtime order events.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { OrderSummary } from '../api/types'
import OrderSummaryRow from '../components/order/OrderSummaryRow.vue'
import ScrollPage from '../components/ScrollPage.vue'
import { JButton, JEmptyState, JSkeleton, JSpinner } from '../components/ui'
import { useErrorText } from '../i18n/core'
import { ROUTE } from '../router/names'
import { useRealtime } from '../services/realtime'
import { useOrdersStore } from '../stores/orders'
import { useSessionStore } from '../stores/session'
import { isActiveOrderStatus } from '../utils/status'

const { t } = useI18n()
const errorText = useErrorText()
const router = useRouter()
const orders = useOrdersStore()
const session = useSessionStore()
const realtime = useRealtime()

const sentinel = ref<HTMLElement | null>(null)
const empty = computed(
  () => orders.historyLoaded && orders.activeLoaded && orders.history.length === 0 && orders.active.length === 0,
)
const firstLoad = computed(() => !orders.historyLoaded && orders.historyLoading)

function open(o: OrderSummary): void {
  if (isActiveOrderStatus(o.status)) void router.push({ name: ROUTE.order, params: { id: o.id } })
  else void router.push({ name: ROUTE.orderDetail, params: { id: o.id } })
}

function refresh(): void {
  void orders.loadActive()
  void orders.loadHistory()
}

let observer: IntersectionObserver | null = null
const offs: Array<() => void> = []

onMounted(() => {
  refresh()
  offs.push(
    realtime.on('*', (e) => {
      if (e.type.startsWith('order.')) refresh()
    }),
    realtime.onResync(refresh),
  )
  if (typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting) && orders.hasMore && !orders.historyError) void orders.loadMore()
    })
  }
})

watch(sentinel, (el, old) => {
  if (old) observer?.unobserve(old)
  if (el) observer?.observe(el)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  for (const off of offs.splice(0)) off()
})
</script>

<template>
  <ScrollPage :title="t('orders.title')">
    <div v-if="firstLoad" class="orders-skeleton">
      <div v-for="n in 4" :key="n" class="orders-skeleton__row">
        <JSkeleton circle height="36px" />
        <span class="orders-skeleton__text"><JSkeleton width="70%" /><JSkeleton width="45%" height="12px" /></span>
      </div>
    </div>

    <JEmptyState v-else-if="empty" class="orders-empty" :title="t('orders.empty_title')" :description="t('orders.empty_text')">
      <template #action>
        <JButton block @click="router.push({ name: ROUTE.home })">{{ t('orders.order_now') }}</JButton>
      </template>
    </JEmptyState>

    <template v-else>
      <section v-if="orders.active.length > 0" class="orders-group">
        <h2 class="orders-group__header">{{ t('orders.active') }}</h2>
        <div class="orders-group__body">
          <OrderSummaryRow v-for="o in orders.active" :key="o.id" :order="o" :locale="session.language" @open="open" />
        </div>
      </section>

      <section v-if="orders.history.length > 0 || orders.historyError" class="orders-group">
        <h2 class="orders-group__header">{{ t('orders.history') }}</h2>
        <div v-if="orders.history.length > 0" class="orders-group__body">
          <OrderSummaryRow v-for="o in orders.history" :key="o.id" :order="o" :locale="session.language" @open="open" />
        </div>
        <div v-if="orders.historyError" class="orders-error" role="alert">
          <p>{{ errorText(orders.historyError) }}</p>
          <JButton variant="secondary" size="medium" @click="orders.history.length ? orders.loadMore() : orders.loadHistory()">
            {{ t('common.retry') }}
          </JButton>
        </div>
        <div v-else-if="orders.hasMore" ref="sentinel" class="orders-more">
          <JSpinner v-if="orders.historyLoading" />
          <JButton v-else variant="plain" size="medium" @click="orders.loadMore()">{{ t('orders.load_more') }}</JButton>
        </div>
      </section>
    </template>
  </ScrollPage>
</template>

<style scoped>
.orders-group {
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
}

.orders-group__header {
  padding: 0 var(--jt-space-4) 7px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
}

.orders-group__body {
  border-radius: var(--jt-radius-card);
  overflow: hidden;
  background: var(--jt-color-bg);
}

.orders-skeleton {
  margin: 0 var(--jt-side-margin);
  padding: var(--jt-space-2) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.orders-skeleton__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  padding: var(--jt-space-3) 0;
}

.orders-skeleton__text {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.orders-empty {
  margin-top: var(--jt-space-8);
  padding: 0 var(--jt-side-margin);
}

.orders-more {
  display: flex;
  justify-content: center;
  padding: var(--jt-space-4) 0;
}

.orders-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-3);
  padding: var(--jt-space-4);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  text-align: center;
}
</style>
