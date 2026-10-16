<!--
  Available orders (GET /driver/orders): live list refreshed by realtime
  order.available / order.taken hints, pull-to-refresh and a polling
  fallback. When the driver cannot take orders, the reason and the way out
  (go online, top up, open the trip, enable location) come first.
-->
<script setup lang="ts">
import { Inbox, RefreshCw } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DriverPage from '../components/driver/DriverPage.vue'
import OnlineCard from '../components/driver/OnlineCard.vue'
import OrderCard from '../components/driver/OrderCard.vue'
import { JButton, JEmptyState, JSkeleton, JSpinner } from '../components/ui'
import { usePolling } from '../composables/usePolling'
import { usePullToRefresh } from '../composables/usePullToRefresh'
import { useDriverErrorText } from '../composables/useDriverErrorText'
import { ROUTE } from '../router/names'
import { useDutyStore } from '../stores/duty'
import { useLocationStore } from '../stores/location'
import { useOrdersStore } from '../stores/orders'
import { canOpenLocationSettings, openLocationSettings } from '../telegram'

/** Fallback refresh while realtime hints may be missing. */
const POLL_MS = 20_000

const { t } = useI18n()
const errorText = useDriverErrorText()
const router = useRouter()
const orders = useOrdersStore()
const duty = useDutyStore()
const location = useLocationStore()

const page = ref<InstanceType<typeof DriverPage> | null>(null)
const scrollEl = computed(() => page.value?.scrollEl ?? null)
const now = ref(Date.now())
const clock = setInterval(() => (now.value = Date.now()), 30_000)
onBeforeUnmount(() => clearInterval(clock))

const { pull, armed, refreshing } = usePullToRefresh(scrollEl, () => orders.refresh())
usePolling(() => orders.refresh(), POLL_MS)

const reason = computed(() => {
  const e = orders.eligibility
  return e && !e.eligible ? e.reason : null
})
const firstLoad = computed(() => !orders.loaded && orders.loading)

function open(id: string): void {
  void router.push({ name: ROUTE.order, params: { id } })
}

function enableLocation(): void {
  if (canOpenLocationSettings()) void openLocationSettings()
  else void location.locateOnce().then(() => orders.refresh()).catch(() => undefined)
}

onMounted(() => void orders.refresh())
</script>

<template>
  <DriverPage ref="page" :title="t('orders.title')" tab-bar>
    <template #trailing>
      <button type="button" class="orders__refresh" :aria-label="t('common.refresh')" :disabled="orders.loading" @click="orders.refresh()">
        <JSpinner v-if="orders.loading && orders.loaded" size="small" />
        <RefreshCw v-else :size="20" :stroke-width="2.25" aria-hidden="true" />
      </button>
    </template>

    <template #top>
      <div class="ptr" :style="{ height: `${pull}px` }" aria-hidden="true">
        <JSpinner v-if="refreshing" size="small" />
        <span v-else-if="pull > 8" class="ptr__text">{{ armed ? t('orders.release_hint') : t('orders.refresh_hint') }}</span>
      </div>
    </template>

    <OnlineCard v-if="!duty.isOnline || reason === 'offline'" />

    <section v-if="reason && reason !== 'offline'" class="elig" role="status">
      <p class="elig__text">{{ t(`orders.eligibility.${reason}`) }}</p>
      <JButton v-if="reason === 'insufficient_balance'" size="medium" block @click="router.push({ name: ROUTE.topup })">{{ t('orders.top_up') }}</JButton>
      <JButton v-else-if="reason === 'has_active_order'" size="medium" block @click="router.push({ name: ROUTE.trip })">{{ t('orders.open_trip') }}</JButton>
      <JButton v-else-if="reason === 'location_required' || reason === 'location_stale'" size="medium" variant="secondary" block @click="enableLocation">
        {{ t('orders.enable_location') }}
      </JButton>
    </section>

    <div v-if="firstLoad" class="orders__skeleton" aria-busy="true">
      <JSkeleton v-for="i in 3" :key="i" height="148px" radius="22px" />
    </div>

    <div v-else-if="orders.error && !orders.loaded" class="orders__state">
      <JEmptyState :title="t('app.load_failed_title')" :description="errorText(orders.error)">
        <template #action>
          <JButton block @click="orders.refresh()">{{ t('common.retry') }}</JButton>
        </template>
      </JEmptyState>
    </div>

    <template v-else>
      <TransitionGroup name="ocard" tag="div" class="orders__list">
        <OrderCard v-for="o in orders.items" :key="o.id" :order="o" :now="now" @open="open" />
      </TransitionGroup>
      <div v-if="orders.loaded && orders.items.length === 0 && (!reason || reason === 'insufficient_balance')" class="orders__state">
        <JEmptyState :title="t('orders.empty_title')" :description="t('orders.empty_text')">
          <template #icon><Inbox :size="40" :stroke-width="1.5" /></template>
        </JEmptyState>
      </div>
    </template>
  </DriverPage>
</template>

<style scoped>
.orders__refresh {
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--jt-tap-target);
  height: var(--jt-tap-target);
}

.orders__refresh:active {
  opacity: var(--jt-opacity-pressed);
}

.ptr {
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  transition: height var(--jt-duration-fast) var(--jt-ease);
}

.ptr__text {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.elig {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.elig__text {
  font: var(--jt-font-body);
}

.orders__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  padding: 0 var(--jt-side-margin);
}

.orders__state {
  padding: var(--jt-space-8) var(--jt-side-margin);
}

.ocard-enter-active,
.ocard-leave-active {
  transition:
    opacity var(--jt-duration-base) var(--jt-ease),
    transform var(--jt-duration-base) var(--jt-ease);
}

.ocard-enter-from,
.ocard-leave-to {
  opacity: 0;
  transform: scale(0.96);
}

.ocard-move {
  transition: transform var(--jt-duration-base) var(--jt-ease);
}
</style>
