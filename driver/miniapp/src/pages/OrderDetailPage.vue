<!--
  Order detail (GET /driver/orders/{id}): an available order the driver may
  accept, or one of their own. Large white map (driver → A → … → E), every
  stop, comment / cargo, fare with commission and net earning, and the
  sticky "accept" button. Acceptance is atomic on the server; the outcomes
  are handled per openapi acceptOrder.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { getOrder } from '../api/orders'
import type { DriverOrderDetail } from '../api/types'
import CategoryIcon from '../components/driver/CategoryIcon.vue'
import FareBreakdown from '../components/driver/FareBreakdown.vue'
import StopList from '../components/driver/StopList.vue'
import TripMap from '../components/driver/TripMap.vue'
import { JBadge, JButton, JEmptyState, JListGroup, JListRow, JSheet, JSkeleton, toast, type SheetSnap } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { classifyAcceptError } from '../features/orders/acceptOutcome'
import { isActiveTripStatus } from '../features/trip/sequence'
import { useDriverErrorText } from '../composables/useDriverErrorText'
import { ROUTE } from '../router/names'
import { isApiError } from '../services/http'
import { useRealtime } from '../services/realtime'
import { useDutyStore } from '../stores/duty'
import { useLocationStore } from '../stores/location'
import { useOrdersStore } from '../stores/orders'
import { useSessionStore } from '../stores/session'
import { useTripStore } from '../stores/trip'
import { haptics } from '../telegram'
import { orderStatusTone } from '../utils/status'

const { t } = useI18n()
const fmt = useFormat()
const errorText = useDriverErrorText()
const route = useRoute()
const router = useRouter()
const orders = useOrdersStore()
const trip = useTripStore()
const duty = useDutyStore()
const session = useSessionStore()
const location = useLocationStore()
const realtime = useRealtime()

const order = shallowRef<DriverOrderDetail | null>(null)
const loading = ref(true)
const loadError = shallowRef<unknown>(null)
const gone = ref(false)
const accepting = ref(false)
const snap = ref<SheetSnap>('half')
const sheetHeight = ref(0)

const id = computed(() => String(route.params.id ?? ''))
const canAcceptNow = computed(() => Boolean(order.value?.can_accept && !gone.value))
const extraStops = computed(() => Math.max(0, (order.value?.stop_count ?? 0) - 2))

async function load(): Promise<void> {
  loading.value = true
  try {
    const o = await getOrder(id.value)
    if (o.is_mine && isActiveTripStatus(o.status)) {
      // The driver's own live order is driven from the trip screen.
      trip.apply(o)
      void router.replace({ name: ROUTE.trip })
      return
    }
    order.value = o
    loadError.value = null
    gone.value = false
  } catch (err) {
    if (isApiError(err) && err.status === 404) {
      gone.value = true
      orders.remove(id.value)
    } else {
      loadError.value = err
    }
  } finally {
    loading.value = false
  }
}

function markGone(): void {
  if (order.value && !order.value.is_mine) {
    gone.value = true
    orders.remove(id.value)
  }
}

async function accept(): Promise<void> {
  const o = order.value
  if (!o || accepting.value) return
  accepting.value = true
  try {
    await trip.accept(o.id)
    haptics.notification('success')
    orders.remove(o.id)
    toast.success(t('order.accepted_toast'))
    void router.replace({ name: ROUTE.trip })
  } catch (err) {
    const f = classifyAcceptError(err)
    haptics.notification('error')
    switch (f.kind) {
      case 'taken':
        toast.error(t('order.taken'))
        markGone()
        gone.value = true
        break
      case 'insufficient_balance':
        toast.show(t('order.insufficient_toast'))
        void router.push({ name: ROUTE.topup, query: f.suggestedTopUp ? { amount: String(f.suggestedTopUp) } : {} })
        break
      case 'has_active_order':
        await trip.refresh()
        void router.replace({ name: ROUTE.trip })
        break
      case 'offline':
        toast.show({
          message: t('order.offline_toast'),
          action: { label: t('orders.go_online'), run: () => void goOnline() },
          duration: 6000,
        })
        break
      case 'not_approved':
        toast.error(errorText(err))
        void session.refreshMe()
        break
      default:
        toast.error(errorText(f.error))
    }
  } finally {
    accepting.value = false
  }
}

async function goOnline(): Promise<void> {
  const res = await duty.setOnline(true)
  if (!res.ok) void router.replace({ name: ROUTE.home })
}

const offs: Array<() => void> = []
onMounted(() => {
  void load()
  offs.push(
    realtime.on('order.taken', (ev) => {
      if (ev.data.order_id === id.value) markGone()
    }),
    realtime.on('order.cancelled', (ev) => {
      if (ev.data.order_id === id.value) markGone()
    }),
    realtime.onResync(() => void load()),
  )
})
onBeforeUnmount(() => offs.forEach((off) => off()))

watch(id, () => void load())
</script>

<template>
  <div class="odetail">
    <TripMap v-if="order" :order="order" :position="location.current" :sheet-height="sheetHeight" @locate="location.locateOnce().catch(() => undefined)" />

    <!-- Keyed: JSheet decides once whether it has a footer, and the accept button appears after loading. -->
    <JSheet
      :key="canAcceptNow ? 'accept' : 'view'"
      v-model:snap="snap"
      :snap-points="['peek', 'half', 'full']"
      :peek-height="190"
      :aria-label="order ? t('order.title', { number: order.number }) : t('order.loading')"
      @height="(h) => (sheetHeight = h)"
    >
      <template #header>
        <div v-if="order" class="odetail__head">
          <div class="odetail__titles">
            <p class="odetail__cat"><CategoryIcon :category="order.category" :size="18" /> {{ t(`category.${order.category}`) }}</p>
            <h1 class="odetail__title">{{ t('order.title', { number: order.number }) }}</h1>
          </div>
          <div class="odetail__fare">
            <span class="odetail__amount jt-tabular">{{ fmt.money(order.fare) }}</span>
            <span class="odetail__route jt-tabular">{{ fmt.distance(order.distance_m) }} · {{ fmt.duration(order.duration_s) }}</span>
          </div>
        </div>
      </template>

      <div v-if="loading && !order" class="odetail__skeleton" aria-busy="true">
        <JSkeleton height="28px" width="60%" />
        <JSkeleton height="64px" />
        <JSkeleton height="120px" radius="22px" />
      </div>

      <div v-else-if="gone" class="odetail__state">
        <JEmptyState :title="t('order.unavailable_title')" :description="t('order.unavailable_text')">
          <template #action>
            <JButton block @click="router.replace({ name: ROUTE.orders })">{{ t('order.back_to_orders') }}</JButton>
          </template>
        </JEmptyState>
      </div>

      <div v-else-if="loadError && !order" class="odetail__state">
        <JEmptyState :title="t('app.load_failed_title')" :description="errorText(loadError)">
          <template #action>
            <JButton block @click="load">{{ t('common.retry') }}</JButton>
          </template>
        </JEmptyState>
      </div>

      <template v-else-if="order">
        <JListGroup class="odetail__group">
          <JListRow
            :title="t('order.to_pickup')"
            :value="order.distance_to_pickup_m !== null ? fmt.distance(order.distance_to_pickup_m) : t('order.unknown')"
          />
          <JListRow :title="t('order.distance')" :value="fmt.distance(order.distance_m)" />
          <JListRow :title="t('order.duration')" :value="fmt.duration(order.duration_s)" />
          <JListRow v-if="order.is_mine" :title="t('order.status_title')">
            <template #accessory><JBadge :tone="orderStatusTone(order.status)" :label="t(`trip.status.${order.status}`)" /></template>
          </JListRow>
        </JListGroup>

        <section class="odetail__card">
          <h2 class="odetail__section">
            {{ t('order.route_title') }}
            <span v-if="extraStops > 0" class="odetail__more">{{ t('orders.more_stops', { n: extraStops }) }}</span>
          </h2>
          <StopList :stops="order.stops" :live="order.is_mine" />
        </section>

        <section v-if="order.cargo_description || order.comment" class="odetail__card">
          <template v-if="order.cargo_description">
            <h2 class="odetail__section">{{ t('order.cargo') }}</h2>
            <p class="odetail__text">{{ order.cargo_description }}</p>
          </template>
          <template v-if="order.comment">
            <h2 class="odetail__section">{{ t('order.comment') }}</h2>
            <p class="odetail__text">{{ order.comment }}</p>
          </template>
        </section>

        <FareBreakdown
          class="odetail__group"
          :fare="order.fare"
          :commission="order.commission_amount"
          :commission-bps="order.commission_bps"
          :payment-method="order.payment_method"
        />

        <p v-if="!order.is_mine" class="odetail__hidden">{{ t('order.details_hidden') }}</p>
      </template>

      <template v-if="canAcceptNow" #footer>
        <JButton block :loading="accepting" @click="accept">{{ t('order.accept') }}</JButton>
      </template>
    </JSheet>
  </div>
</template>

<style scoped>
.odetail {
  position: absolute;
  inset: 0;
  overflow: hidden;
  background: var(--jt-color-grouped-bg);
}

.odetail__head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: var(--jt-space-3);
  padding: var(--jt-space-1) 0 var(--jt-space-3);
}

.odetail__titles {
  min-width: 0;
}

.odetail__cat {
  display: flex;
  align-items: center;
  gap: 6px;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.odetail__title {
  font: var(--jt-font-title3);
}

.odetail__fare {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.odetail__amount {
  font: var(--jt-font-title1);
}

.odetail__route {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

/* Inside the sheet the side margin comes from the sheet itself. */
.odetail__group {
  margin: 0 0 var(--jt-space-4);
}

.odetail__group :deep(.jt-list-group__body) {
  background: var(--jt-color-fill-secondary);
}

.odetail__group :deep(.jt-row) {
  background: transparent;
}

.odetail__card {
  margin-bottom: var(--jt-space-4);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.odetail__section {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  margin: var(--jt-space-1) 0 var(--jt-space-1);
  font: var(--jt-font-headline);
}

.odetail__more {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.odetail__text {
  margin-bottom: var(--jt-space-2);
  font: var(--jt-font-body);
  overflow-wrap: anywhere;
}

.odetail__hidden {
  margin-bottom: var(--jt-space-4);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.odetail__skeleton {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.odetail__state {
  padding: var(--jt-space-6) 0;
}
</style>
