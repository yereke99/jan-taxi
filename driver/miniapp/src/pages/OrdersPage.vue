<!--
  Available orders (GET /driver/orders): orders within the driver's dispatch
  radius, nearest first, refreshed by realtime order.available / order.taken
  hints, pull-to-refresh and a polling fallback. The header carries the
  driver, the online switch and the available balance. When the driver cannot take orders, the reason and the way out
  (go online, top up, open the trip, enable location) come first.
-->
<script setup lang="ts">
import { Inbox } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
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
import { useLocationStore } from '../stores/location'
import { useOrdersStore } from '../stores/orders'
import { useWalletStore } from '../stores/wallet'
import { useFormat } from '../composables/useFormat'
import { canOpenLocationSettings, haptics, openLocationSettings } from '../telegram'

/** Fallback refresh while realtime hints may be missing. */
const POLL_MS = 20_000
/** Dispatch offers orders within this radius first (the API widens it when empty). */
const DISPATCH_RADIUS_KM = 5

const { t } = useI18n()
const errorText = useDriverErrorText()
const router = useRouter()
const orders = useOrdersStore()
const duty = useDutyStore()
const location = useLocationStore()
const wallet = useWalletStore()
const fmt = useFormat()

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
    toast.show({ message: t('orders.eligibility.location_required'), action: { label: t('orders.enable_location'), run: enableLocation }, duration: 6000 })
  } else if (res.reason === 'insufficient_balance') {
    toast.show({ message: t('orders.eligibility.insufficient_balance'), action: { label: t('orders.top_up'), run: () => void router.push({ name: ROUTE.topup }) } })
  } else {
    toast.error(t(`orders.eligibility.${res.reason === 'not_approved' ? 'not_approved' : 'location_required'}`))
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
    <template #trailing>
      <label class="orders__online">
        <span>{{ duty.isOnline ? t('orders.online') : t('orders.offline') }}</span>
        <JSwitch :model-value="duty.isOnline" :busy="duty.switching" :label="t('orders.online')" @update:model-value="toggleOnline" />
      </label>
    </template>
    <template #large-trailing>
      <button v-if="wallet.wallet" type="button" class="orders__balance" @click="router.push({ name: ROUTE.wallet })">
        <span>{{ t('orders.balance') }}</span>
        <strong class="jt-tabular">{{ fmt.money(wallet.wallet.available) }}</strong>
      </button>
    </template>

    <template #top>
      <div class="ptr" :style="{ height: `${pull}px` }" aria-hidden="true">
        <JSpinner v-if="refreshing" size="small" />
        <span v-else-if="pull > 8" class="ptr__text">{{ armed ? t('orders.release_hint') : t('orders.refresh_hint') }}</span>
      </div>
      <p v-if="duty.isOnline" class="orders__radius">{{ t('orders.nearby', { km: DISPATCH_RADIUS_KM }) }}</p>
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
  padding: 0 calc(var(--jt-side-margin) + var(--jt-safe-right)) var(--jt-space-2) calc(var(--jt-side-margin) + var(--jt-safe-left));
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
