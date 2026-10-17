<!--
  Order detail (GET /driver/orders/{id}): an available order the driver may
  accept, or one of their own. Large white map (driver → A → … → E), every
  stop, comment / cargo, fare with commission and net earning, and the
  sticky "accept" button. Acceptance is atomic on the server; the outcomes
  are handled per openapi acceptOrder.
-->
<script setup lang="ts">
import { ChevronLeft, Clock, MapPin, Route } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { getOrder } from '../api/orders'
import type { DriverOrderDetail } from '../api/types'
import FareBreakdown from '../components/driver/FareBreakdown.vue'
import StopList from '../components/driver/StopList.vue'
import TripMap from '../components/driver/TripMap.vue'
import { JBadge, JButton, JEmptyState, JMapButton, JSheet, JSkeleton, toast, type SheetSnap } from '../components/ui'
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
import { normalizeTwoGisBase, planNavigation } from '../features/navigation/twogis'
import { useEnsureLocation } from '../features/location/useEnsureLocation'
import { effectiveSafeInsets, haptics, navigateBack, openExternal, useTelegramFullscreen } from '../telegram'
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
const gps = useEnsureLocation()

const order = shallowRef<DriverOrderDetail | null>(null)
const loading = ref(true)
const loadError = shallowRef<unknown>(null)
const gone = ref(false)
const accepting = ref(false)
const snap = ref<SheetSnap>('half')
const sheetHeight = ref(0)
const tg = useTelegramFullscreen()
// Inside Telegram the client shows its own back control (BackButton).
const inTelegram = tg.state.isTelegram
const backStyle = computed(() => {
  const safe = effectiveSafeInsets(tg.state)
  return { top: `${safe.top + 8}px`, left: `${safe.left + 16}px` }
})

/** 2GIS route through every stop (from the driver's position when known). */
const twoGisUrl = computed(() => {
  const o = order.value
  if (!o || o.stops.length === 0) return null
  const plan = planNavigation({
    base: normalizeTwoGisBase(session.config.navigation.twogis_base_url),
    platform: tg.state.platform,
    origin: location.current ? { lat: location.current.lat, lon: location.current.lon } : null,
    remaining: [...o.stops].sort((a, b) => a.seq - b.seq).map((st) => ({ lat: st.lat, lon: st.lon })),
  })
  return plan?.primaryUrl ?? null
})

function openTwoGis(): void {
  const url = twoGisUrl.value
  if (!url) return
  haptics.impact('light')
  if (!openExternal(url)) toast.error(t('trip.open_failed'))
}

const id = computed(() => String(route.params.id ?? ''))
const canAcceptNow = computed(() => Boolean(order.value?.can_accept && !gone.value))

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

/** Opens the location settings after a denial, or reads and sends a fresh fix. */
async function enableLocation(): Promise<void> {
  if (await gps.fix()) toast.show(t('order.location_ready'))
}

/**
 * Accepts the order. Without a recent location the API answers
 * location_required: a fresh fix is sent and the acceptance tried once more.
 */
async function acceptWithLocation(orderId: string): Promise<DriverOrderDetail> {
  try {
    return await trip.accept(orderId)
  } catch (err) {
    if (classifyAcceptError(err).kind !== 'location_required' || !(await gps.ensure())) throw err
    return trip.accept(orderId)
  }
}

function locationProblemText(): string {
  switch (gps.state.value) {
    case 'denied':
      return t('orders.location_denied')
    case 'failed':
      return t('orders.location_failed')
    case 'outside':
      return t('duty.outside_text')
    default:
      return t('order.location_toast')
  }
}

async function accept(): Promise<void> {
  const o = order.value
  if (!o || accepting.value) return
  accepting.value = true
  try {
    await acceptWithLocation(o.id)
    haptics.notification('success')
    orders.remove(o.id)
    toast.success(t('order.accepted_toast'))
    void router.replace({ name: ROUTE.trip })
  } catch (err) {
    const f = classifyAcceptError(err)
    haptics.notification('error')
    switch (f.kind) {
      case 'taken':
        // Another driver was faster: normal in dispatch, not an error.
        toast.show(t('order.taken'))
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
      case 'location_required':
        toast.show({
          message: locationProblemText(),
          action:
            gps.state.value === 'outside'
              ? undefined
              : { label: gps.state.value === 'denied' ? t('common.open_settings') : t('orders.enable_location'), run: () => void enableLocation() },
          duration: 8000,
        })
        break
      case 'not_approved':
        // The status changed (suspended, profile missing): re-reading /me re-routes the driver.
        if (!session.handleAccessError(err)) {
          toast.show(errorText(err))
          void session.refreshMe()
        }
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
    <div v-if="!inTelegram" class="odetail__back" :style="backStyle">
      <JMapButton :label="t('common.back')" @click="navigateBack(router)">
        <ChevronLeft :size="26" :stroke-width="2" aria-hidden="true" />
      </JMapButton>
    </div>

    <!-- Keyed: JSheet decides once whether it has a footer, and the accept button appears after loading. -->
    <JSheet
      :key="canAcceptNow ? 'accept' : 'view'"
      v-model:snap="snap"
      :snap-points="['peek', 'half', 'full']"
      :peek-height="190"
      :half-ratio="0.62"
      :aria-label="order ? t('order.title', { number: order.number }) : t('order.loading')"
      @height="(h) => (sheetHeight = h)"
    >
      <template #header>
        <div v-if="order" class="odetail__head">
          <h1 class="odetail__title">
            {{ t(`category.${order.category}`) }} · <span class="jt-tabular">{{ fmt.money(order.fare) }}</span>
          </h1>
          <JBadge v-if="order.is_mine" :tone="orderStatusTone(order.status)" :label="t(`trip.status.${order.status}`)" />
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
        <section class="odetail__stops">
          <StopList :stops="order.stops" :live="order.is_mine" />
        </section>

        <div class="odetail__chips jt-tabular">
          <span class="odetail__chip"><Route :size="18" :stroke-width="2" aria-hidden="true" />{{ fmt.distance(order.distance_m) }}</span>
          <span class="odetail__chip"><Clock :size="17" :stroke-width="2" aria-hidden="true" />{{ fmt.duration(order.duration_s) }}</span>
          <span v-if="order.distance_to_pickup_m !== null" class="odetail__chip">
            <MapPin :size="17" :stroke-width="2" aria-hidden="true" />{{ t('orders.from_you', { distance: fmt.distance(order.distance_to_pickup_m) }) }}
          </span>
        </div>

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
          :compact="!order.is_mine"
        />

        <JButton v-if="twoGisUrl" class="odetail__nav" variant="secondary" block @click="openTwoGis">
          <template #icon><MapPin :size="20" :stroke-width="2" aria-hidden="true" /></template>
          {{ t('trip.open_2gis') }}
        </JButton>

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

.odetail__back {
  position: fixed;
  z-index: var(--jt-z-floating);
}

.odetail__stops {
  margin-bottom: var(--jt-space-3);
  padding: var(--jt-space-1) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.odetail__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: var(--jt-space-3);
}

.odetail__chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 36px;
  padding: 0 12px;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
  font-weight: 600;
  white-space: nowrap;
}

.odetail__nav {
  margin-top: var(--jt-space-3);
}

.odetail__title {
  font: var(--jt-font-title1);
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
