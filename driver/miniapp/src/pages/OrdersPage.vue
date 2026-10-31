<!--
  Available orders (GET /driver/orders): orders within the driver's dispatch
  radius around pickup A (5 km, growing to 10 km while an order waits),
  nearest first, refreshed by realtime hints (order.available adds,
  order.taken / order.unavailable remove within about a second),
  pull-to-refresh and a slow polling fallback. Every card takes its order in
  one tap («Тапсырысты алу», features/orders/useAcceptOrder); the rest of the
  card opens the order. The header carries the driver, the online switch and
  the available balance. When the driver cannot take orders, the reason and
  the way out (go online, top up, open the trip) come first; a missing or
  stale location is fixed right away by sending a fresh GPS fix.
-->
<script setup lang="ts">
import { Inbox } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DriverIdentity from '../components/driver/DriverIdentity.vue'
import DriverPage from '../components/driver/DriverPage.vue'
import OnlineCard from '../components/driver/OnlineCard.vue'
import OrderCard from '../components/driver/OrderCard.vue'
import { JButton, JEmptyState, JSkeleton, JSpinner, JSwitch, toast } from '../components/ui'
import { usePolling } from '../composables/usePolling'
import { usePullToRefresh } from '../composables/usePullToRefresh'
import { useDriverErrorText } from '../composables/useDriverErrorText'
import { ROUTE } from '../router/names'
import { useDutyStore } from '../stores/duty'
import { useOrdersStore } from '../stores/orders'
import { useWalletStore } from '../stores/wallet'
import { useFormat } from '../composables/useFormat'
import { useEnsureLocation } from '../features/location/useEnsureLocation'
import { useAcceptOrder } from '../features/orders/useAcceptOrder'
import { useRealtimeStatus } from '../services/realtime'
import { haptics } from '../telegram'

/** Fallback refresh while the realtime connection is down. */
const POLL_MS = 20_000
/** While the connection is up, hints keep the list current; a rare full refresh is a safety net. */
const POLL_CONNECTED_MS = 60_000
/** Dispatch radius defaults (dispatch.radius_base_m / radius_max_m, ARCHITECTURE §7). */
const RADIUS_BASE_KM = 5
const RADIUS_MAX_KM = 10

const { t } = useI18n()
const errorText = useDriverErrorText()
const router = useRouter()
const orders = useOrdersStore()
const duty = useDutyStore()
const wallet = useWalletStore()
const fmt = useFormat()
const gps = useEnsureLocation()
const acceptor = useAcceptOrder()
const realtimeStatus = useRealtimeStatus()

const page = ref<InstanceType<typeof DriverPage> | null>(null)
const scrollEl = computed(() => page.value?.scrollEl ?? null)
const now = ref(Date.now())
const clock = setInterval(() => (now.value = Date.now()), 30_000)
onBeforeUnmount(() => clearInterval(clock))

const { pull, armed, refreshing } = usePullToRefresh(scrollEl, () => orders.refresh())
let lastPoll = Date.now()
usePolling(() => {
  const now = Date.now()
  if (realtimeStatus.value === 'open' && now - lastPoll < POLL_CONNECTED_MS) return
  lastPoll = now
  return orders.refresh()
}, POLL_MS)

const reason = computed(() => {
  const e = orders.eligibility
  return e && !e.eligible ? e.reason : null
})
const firstLoad = computed(() => !orders.loaded && orders.loading)

function open(id: string): void {
  void router.push({ name: ROUTE.order, params: { id } })
}

function accept(id: string): void {
  void acceptor.accept(id)
}

const needsLocation = computed(() => reason.value === 'location_required' || reason.value === 'location_stale')

/** Sends a fresh fix and reloads the list (the API needs a recent location). */
async function sendLocation(): Promise<void> {
  if (await gps.ensure()) await orders.refresh()
}

async function enableLocation(): Promise<void> {
  if (await gps.fix()) await orders.refresh()
}

const locationText = computed(() => {
  switch (gps.state.value) {
    case 'locating':
      return t('orders.locating')
    case 'denied':
      return t('orders.location_denied')
    case 'failed':
      return t('orders.location_failed')
    case 'outside':
      return t('duty.outside_text')
    default:
      return reason.value ? t(`orders.eligibility.${reason.value}`) : ''
  }
})

// No recent fix on the server: read and send one at once instead of asking.
watch(
  needsLocation,
  (need) => {
    if (need && gps.state.value === 'idle') void sendLocation()
  },
  { immediate: true },
)

async function toggleOnline(next: boolean): Promise<void> {
  const res = await duty.setOnline(next)
  if (res.ok) {
    haptics.notification('success')
    void orders.refresh()
    return
  }
  haptics.notification('error')
  if (res.reason === 'error') {
    if (res.error) toast.error(errorText(res.error))
  } else if (res.reason === 'location_required') {
    toast.show({ message: t('duty.location_text'), action: { label: t('orders.enable_location'), run: () => void enableLocation() }, duration: 6000 })
  } else if (res.reason === 'insufficient_balance') {
    toast.show({ message: t('orders.eligibility.insufficient_balance'), action: { label: t('orders.top_up'), run: () => void router.push({ name: ROUTE.topup }) } })
  } else {
    toast.show(res.reason === 'outside_service_area' ? t('duty.outside_text') : t('orders.eligibility.not_approved'))
  }
}

onMounted(() => {
  void orders.refresh()
  if (!wallet.wallet) void wallet.refreshWallet()
})
</script>

<template>
  <DriverPage ref="page" class="orders-page" :title="t('orders.title')" tab-bar>
    <template #leading>
      <DriverIdentity />
    </template>
    <template v-if="duty.isOnline" #trailing>
      <label class="orders__online">
        <span>{{ t('orders.online') }}</span>
        <JSwitch :model-value="duty.isOnline" :busy="duty.switching" :label="t('orders.online')" @update:model-value="toggleOnline" />
      </label>
    </template>

    <template #top>
      <div class="ptr" :style="{ height: `${pull}px` }" aria-hidden="true">
        <JSpinner v-if="refreshing" size="small" />
        <span v-else-if="pull > 8" class="ptr__text">{{ armed ? t('orders.release_hint') : t('orders.refresh_hint') }}</span>
      </div>
      <div class="orders__meta">
        <button v-if="wallet.wallet" type="button" class="orders__balance" @click="router.push({ name: ROUTE.wallet })">
          <span>{{ t('orders.balance') }}</span>
          <strong class="jt-tabular">{{ fmt.money(wallet.wallet.available) }}</strong>
        </button>
        <p v-if="duty.isOnline" class="orders__radius">{{ t('orders.nearby', { min: RADIUS_BASE_KM, max: RADIUS_MAX_KM }) }}</p>
      </div>
    </template>

    <OnlineCard v-if="!duty.isOnline || reason === 'offline'" />

    <section v-if="reason && reason !== 'offline'" class="elig" role="status">
      <p class="elig__text" :class="{ 'is-busy': gps.state.value === 'locating' }">
        <JSpinner v-if="needsLocation && gps.state.value === 'locating'" size="small" />
        <span>{{ needsLocation ? locationText : t(`orders.eligibility.${reason}`) }}</span>
      </p>
      <JButton v-if="reason === 'insufficient_balance'" size="medium" block @click="router.push({ name: ROUTE.topup })">{{ t('orders.top_up') }}</JButton>
      <JButton v-else-if="reason === 'has_active_order'" size="medium" block @click="router.push({ name: ROUTE.trip })">{{ t('orders.open_trip') }}</JButton>
      <JButton
        v-else-if="needsLocation && gps.state.value !== 'locating' && gps.state.value !== 'outside'"
        size="medium"
        variant="secondary"
        block
        @click="enableLocation"
      >
        {{ gps.state.value === 'denied' ? t('common.open_settings') : t('orders.enable_location') }}
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
        <OrderCard
          v-for="o in orders.items"
          :key="o.id"
          :order="o"
          :now="now"
          :accepting="acceptor.acceptingId.value === o.id"
          :disabled="acceptor.busy.value"
          @open="open"
          @accept="accept"
        />
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
.orders__online {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  font: var(--jt-font-headline);
  white-space: nowrap;
}

/* The identity and the online switch share the top row; the compact title
   only appears once the list scrolls. */
.orders-page :deep(.jt-navbar__bar) {
  grid-template-columns: minmax(0, auto) auto minmax(0, auto);
  justify-content: space-between;
  column-gap: var(--jt-space-2);
}

.orders__meta {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
  padding: 0 calc(var(--jt-side-margin) + var(--jt-safe-right)) var(--jt-space-3) calc(var(--jt-side-margin) + var(--jt-safe-left));
}

.orders__meta:empty {
  display: none;
}

.orders__balance {
  display: flex;
  align-items: baseline;
  gap: 5px;
  height: 34px;
  padding: 0 12px;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-bg);
  box-shadow: 0 0 0 var(--jt-hairline) var(--jt-color-separator);
  font: var(--jt-font-footnote);
  font-size: 14px;
  color: var(--jt-color-label-secondary);
  line-height: 34px;
  white-space: nowrap;
}

.orders__balance strong {
  color: var(--jt-color-label);
  font-weight: 700;
}

.orders__radius {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

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
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
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
