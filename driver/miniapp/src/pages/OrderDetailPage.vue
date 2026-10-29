<!--
  Order detail (GET /driver/orders/{id}): an available order the driver may
  accept, or one of their own. Large white map (driver → A → … → E), the
  settlement of pickup A above every stop, the requested vehicle class with passenger count or cargo size (§21),
  comment / cargo, fare with commission and net earning, and the sticky
  "accept" button with the commission charged on acceptance stated right
  above it (§22.2). Acceptance is atomic on the server; the outcomes are
  handled per openapi acceptOrder: 402 shows how much is missing with a
  top-up button, a taken order closes, a missing location is fixed in place.
-->
<script setup lang="ts">
import { ChevronLeft, Clock, MapPin, Navigation, Package, Route, Users } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { getOrder } from '../api/orders'
import type { DriverOrderDetail } from '../api/types'
import FareBreakdown from '../components/driver/FareBreakdown.vue'
import StopList from '../components/driver/StopList.vue'
import TripMap from '../components/driver/TripMap.vue'
import VehicleClassIcon from '../components/driver/VehicleClassIcon.vue'
import { JBadge, JButton, JEmptyState, JListGroup, JListRow, JMapButton, JSheet, JSkeleton, toast, type SheetSnap } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { classifyAcceptError, suggestedTopUpAmount } from '../features/orders/acceptOutcome'
import { findClass } from '../features/vehicle/classes'
import { useVehicleText } from '../features/vehicle/useVehicleText'
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
import { useWalletStore } from '../stores/wallet'
import { navigationPrefix, normalizeTwoGisBase, planNavigation } from '../features/navigation/twogis'
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
const wallet = useWalletStore()
const vt = useVehicleText()

const order = shallowRef<DriverOrderDetail | null>(null)
const loading = ref(true)
const loadError = shallowRef<unknown>(null)
const gone = ref(false)
const accepting = ref(false)
/** Set by a 402 insufficient_balance: what the commission needs and what is available. */
const shortfall = ref<{ required: number | null; available: number | null; topUp: number | null } | null>(null)
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
    remaining: navigationPrefix([...o.stops].sort((a, b) => a.seq - b.seq)),
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
/** Completed or cancelled: nothing left to drive to. */
const finished = computed(() => order.value?.status === 'completed' || order.value?.status === 'cancelled')

const classIcon = computed(() => findClass(session.extras.vehicleClasses, order.value?.vehicle_class)?.icon ?? null)
const className = computed(() => (order.value?.vehicle_class ? vt.className(order.value.vehicle_class, order.value.vehicle_class_name) : ''))
const loadText = computed(() => (order.value ? vt.load(order.value) : ''))

const shortfallText = computed(() => {
  const s = shortfall.value
  if (!s) return ''
  if (s.required === null || s.available === null) return t('order.insufficient_toast')
  return t('order.insufficient_text', { required: fmt.money(s.required), available: fmt.money(s.available) })
})

function topUp(): void {
  const amount = shortfall.value?.topUp
  void router.push({ name: ROUTE.topup, query: amount ? { amount: String(amount) } : {} })
}

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
    if (o.can_accept) clearShortfallIfCovered()
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

/** After a top-up the shortfall message goes away once the balance covers the commission. */
function clearShortfallIfCovered(): void {
  const w = wallet.wallet
  const o = order.value
  if (shortfall.value && w && o && w.available >= o.commission_amount) shortfall.value = null
}

async function accept(): Promise<void> {
  const o = order.value
  if (!o || accepting.value) return
  accepting.value = true
  try {
    await acceptWithLocation(o.id)
    haptics.notification('success')
    shortfall.value = null
    orders.remove(o.id)
    toast.success(t('order.accepted_toast'))
    // The commission was charged with the acceptance.
    void wallet.refreshWallet()
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
        // The order stays open: say what is missing and offer the top-up right here.
        shortfall.value = {
          required: f.required ?? o.commission_amount,
          available: f.available,
          topUp: f.suggestedTopUp ?? suggestedTopUpAmount(o.commission_amount, f.available, session.config.topup?.min ?? null),
        }
        snap.value = 'half'
        void wallet.refreshWallet()
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

watch(() => wallet.wallet?.available, clearShortfallIfCovered)

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
          <div class="odetail__head-text">
            <h1 class="odetail__title">
              {{ t(`category.${order.category}`) }} · <span class="jt-tabular">{{ fmt.money(order.fare) }}</span>
            </h1>
            <p v-if="className || loadText" class="odetail__need">
              <VehicleClassIcon v-if="classIcon" :icon="classIcon" :width="34" />
              <span v-if="className" class="odetail__need-class">{{ className }}</span>
              <span v-if="loadText" class="odetail__need-load">
                <Users v-if="order.passenger_count" :size="16" :stroke-width="2" aria-hidden="true" />
                <Package v-else :size="16" :stroke-width="2" aria-hidden="true" />
                {{ loadText }}
              </span>
            </p>
          </div>
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
          <p v-if="order.locality" class="odetail__locality">
            <MapPin :size="16" :stroke-width="2.2" aria-hidden="true" />
            <span class="jt-visually-hidden">{{ t('order.locality') }}:</span>
            <span class="odetail__locality-name">{{ order.locality }}</span>
          </p>
          <StopList :stops="order.stops" :live="order.is_mine" />
        </section>

        <div class="odetail__chips jt-tabular">
          <span class="odetail__chip"><Route :size="18" :stroke-width="2" aria-hidden="true" />{{ fmt.distance(order.distance_m) }}</span>
          <span class="odetail__chip"><Clock :size="17" :stroke-width="2" aria-hidden="true" />{{ fmt.duration(order.duration_s) }}</span>
          <span v-if="order.distance_to_pickup_m !== null && !finished" class="odetail__chip">
            <Navigation :size="16" :stroke-width="2" aria-hidden="true" />{{ t('orders.from_you', { distance: fmt.distance(order.distance_to_pickup_m) }) }}
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

        <!-- A cancelled trip earned nothing; only the commission taken at acceptance remains (§22.2). -->
        <JListGroup
          v-if="order.is_mine && order.status === 'cancelled'"
          class="odetail__group"
          :footer="order.commission_charged > 0 ? t('order.cancelled_commission_kept') : t('order.cancelled_commission_none')"
        >
          <JListRow :title="t('history.commission')">
            <template #accessory><span class="odetail__minus jt-tabular">−{{ fmt.money(order.commission_charged) }}</span></template>
          </JListRow>
        </JListGroup>
        <FareBreakdown
          v-else
          class="odetail__group"
          :fare="order.fare"
          :commission="order.commission_amount"
          :payment-method="order.payment_method"
          :compact="!order.is_mine"
          :charged="order.is_mine"
        />

        <JButton v-if="twoGisUrl && !finished" class="odetail__nav" variant="secondary" block @click="openTwoGis">
          <template #icon><MapPin :size="20" :stroke-width="2" aria-hidden="true" /></template>
          {{ t('trip.open_2gis') }}
        </JButton>

        <p v-if="!order.is_mine" class="odetail__hidden">{{ t('order.details_hidden') }}</p>
      </template>

      <template v-if="canAcceptNow" #footer>
        <p v-if="shortfall" class="odetail__short" role="alert">{{ shortfallText }}</p>
        <p v-else-if="order" class="odetail__fee jt-tabular">{{ t('order.commission_on_accept', { amount: fmt.money(order.commission_amount) }) }}</p>
        <div class="odetail__actions">
          <JButton v-if="shortfall" block @click="topUp">
            {{ shortfall.topUp ? t('order.top_up_amount', { amount: fmt.money(shortfall.topUp) }) : t('orders.top_up') }}
          </JButton>
          <JButton block :variant="shortfall ? 'secondary' : 'primary'" :loading="accepting" @click="accept">{{ t('order.accept') }}</JButton>
        </div>
      </template>
    </JSheet>
  </div>
</template>

<style scoped>
.odetail__minus {
  color: var(--jt-color-label-secondary);
}

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

.odetail__locality {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: var(--jt-space-2) 0 2px;
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.odetail__locality svg {
  flex: none;
}

.odetail__locality-name {
  min-width: 0;
  overflow-wrap: anywhere;
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

.odetail__head-text {
  min-width: 0;
}

.odetail__need {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px var(--jt-space-2);
  margin-top: var(--jt-space-1);
  font: var(--jt-font-subheadline);
}

.odetail__need-class {
  font-weight: 600;
}

.odetail__need-load {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--jt-color-label-secondary);
}

.odetail__fee,
.odetail__short {
  margin-bottom: var(--jt-space-2);
  font: var(--jt-font-subheadline);
  text-align: center;
}

.odetail__fee {
  color: var(--jt-color-label-secondary);
}

.odetail__short {
  color: var(--jt-color-red-text);
}

.odetail__actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
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
