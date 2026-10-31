<!--
  Live order ("Сапарым" tab). Searching: pulsing pickup, elapsed time, the
  route summary with the vehicle class, cancel. Accepted / driver arriving /
  in progress: the trip screen — driver, car and plate over the live map, the
  arrival estimate ("arrives in N min", §22.6), safety / share / call, the
  parcel protection note for deliveries, the order card (price, date,
  category and class, stops, comment) and cancel + "more" at the bottom.
  Cancelling opens the reasons sheet (§22.3). Completed: summary, rating and
  "report a problem". Cancelled: the reason and "order again". State follows
  realtime events with REST re-sync and a polling fallback (useLiveOrder).
  Pickup A carries the order's place under its address: the settlement from
  the API (`locality`), else the pickup's locality line seen while booking.
-->
<script setup lang="ts">
import { Banknote, ChevronRight, Ellipsis, ShieldCheck } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { Order } from '../../api/types'
import CancelOrderSheet from '../../components/order/CancelOrderSheet.vue'
import DisputeForm from '../../components/order/DisputeForm.vue'
import OrderStopList from '../../components/order/OrderStopList.vue'
import RatingForm from '../../components/order/RatingForm.vue'
import TripDriverCard from '../../components/order/TripDriverCard.vue'
import { JButton, JEmptyState, JSheet, JSkeleton, JSpinner, showActionSheet, toast, type SheetSnap } from '../../components/ui'
import VehicleClassIcon from '../../components/VehicleClassIcon.vue'
import { useDriverPhoto } from '../../composables/useDriverPhoto'
import { useLiveOrder } from '../../composables/useLiveOrder'
import { useVehicleText } from '../../composables/useVehicleText'
import { arrivalMinutes, cancelInfo, framingPoints, hasActiveDriver, headlineKey, isDeliveryOrder, orderLocality, orderMarkers } from '../../domain/orderView'
import { placesOfStops } from '../../domain/places'
import { useAddressText, useErrorText } from '../../i18n/core'
import { straightLine } from '../../maps'
import { ROUTE } from '../../router/names'
import { isApiError } from '../../services/http'
import { useAutoFit, useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { callPhone, haptics, openTelegram } from '../../telegram'
import { VEHICLE_ICONS, type VehicleIcon } from '../../services/config'
import { hasCoordinates } from '../../utils/geo'
import { formatDateTime, formatMoney } from '../../utils/format'
import { formatKzPhone, telHref } from '../../utils/phone'

const { t } = useI18n()
const addr = useAddressText()
const vehicleText = useVehicleText()
const errorText = useErrorText()
const route = useRoute()
const router = useRouter()
const stage = useStageScreen()
const session = useSessionStore()
const booking = useBookingStore()
const orders = useOrdersStore()

const orderId = computed(() => String(route.params.id ?? '').toLowerCase())
const live = useLiveOrder(orderId)
const order = live.order
const photoUrl = useDriverPhoto(
  computed(() => order.value?.id ?? null),
  computed(() => Boolean(order.value?.driver?.photo_url)),
)

const lang = computed(() => session.language)
const notFound = computed(() => isApiError(live.error.value) && (live.error.value.code === 'not_found' || live.error.value.status === 404))
const delivery = computed(() => (order.value ? isDeliveryOrder(order.value) : false))
const activeDriver = computed(() => (order.value ? hasActiveDriver(order.value) : false))
const cancel = computed(() => (order.value ? cancelInfo(order.value) : null))
/** The trip screen layout: a driver is assigned and the trip is not over. */
const liveTrip = computed(() => Boolean(order.value?.driver) && activeDriver.value)

// The live trip opens with the driver card and actions above the map; the
// order card is one swipe up. Other states size the sheet to their content.
const snap = ref<SheetSnap>('content')
const snapPoints = computed<SheetSnap[]>(() => (liveTrip.value ? ['header', 'content'] : ['content']))
watch(liveTrip, (on) => (snap.value = on ? 'header' : 'content'), { immediate: true })
const firstStop = computed(() => order.value?.stops[0] ?? null)
const lastStop = computed(() => {
  const stops = order.value?.stops ?? []
  return stops.length > 1 ? stops[stops.length - 1]! : null
})
const extraStops = computed(() => Math.max(0, (order.value?.stops.length ?? 0) - 2))
const createdAt = computed(() => (order.value ? formatDateTime(order.value.accepted_at ?? order.value.created_at, lang.value) : ''))
const categoryChip = computed(() => {
  const c = order.value?.category
  return c === 'parcel' ? t('order.chip_parcel') : c === 'other' ? t('order.chip_other') : t('order.chip_taxi')
})
/** The ordered vehicle class and its load ("Minivan", "Passengers: 6"). */
const className = computed(() => (order.value ? vehicleText.className(order.value.vehicle_class, order.value.vehicle_class_name) : ''))
const classIcon = computed<VehicleIcon>(() => VEHICLE_ICONS.find((i) => i === order.value?.vehicle_class) ?? (delivery.value ? 'cargo_van' : 'sedan'))
const loadLine = computed(() => (order.value ? vehicleText.loadText(order.value) : ''))
/** Settlement of pickup A ("Алматы"), or the locality line seen while booking; never coordinates. */
const locality = computed(() => (order.value ? orderLocality(order.value, orders.pickupSubtitle(order.value.id)) : null))

const headline = computed(() => {
  const o = order.value
  if (!o) return ''
  // The API's road estimate (§22.6), counted down between updates.
  const minutes = arrivalMinutes(o, now.value)
  if (minutes !== null) return t('order.arrives_in', { n: minutes })
  return t(headlineKey(o))
})

const farePayment = computed(() =>
  order.value
    ? t('order.fare_payment', { fare: formatMoney(order.value.fare, lang.value), payment: t(`payment_method.${order.value.payment_method}`) })
    : '',
)

// --- Map ---------------------------------------------------------------------

const autoFit = useAutoFit(stage, () => (order.value ? framingPoints(order.value) : []))
stage.controls.recenter = () => autoFit.refit()

let framedStatus: string | null = null
let framedWithDriver = false
watch(
  order,
  (o) => {
    if (!o) return
    stage.stops.setStops(orderMarkers(o))
    stage.route.setRoute(o.route.geometry)
    const driver = hasActiveDriver(o) ? o.driver_location : null
    stage.car.set(driver ? { lat: driver.lat, lon: driver.lon, heading: driver.heading } : null)
    const pickup = o.stops[0]
    const approaching = (o.status === 'accepted' || o.status === 'driver_arriving') && driver && pickup && hasCoordinates(pickup)
    stage.route.setApproach(approaching ? straightLine(driver, pickup) : null)
    // Re-frame on a new phase (or when the driver first appears), never on
    // every position update: the passenger may be looking around.
    if (framedStatus !== o.status || (driver !== null && !framedWithDriver)) {
      framedStatus = o.status
      framedWithDriver = driver !== null
      autoFit.refit()
    }
  },
  { immediate: true },
)

watch(
  () => order.value?.status,
  (s, prev) => {
    if (!s || !prev || s === prev) return
    if (s === 'accepted' || s === 'driver_arriving' || s === 'completed') haptics.notification('success')
    if (s === 'cancelled') haptics.notification('warning')
    void orders.loadActive()
  },
)

// --- Searching timer and arrival countdown ------------------------------------------

const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | undefined
watch(
  () => order.value?.status === 'searching' || order.value?.status === 'accepted',
  (ticking) => {
    clearInterval(ticker)
    now.value = Date.now()
    if (ticking) ticker = setInterval(() => (now.value = Date.now()), 1000)
  },
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(ticker))

const elapsed = computed(() => {
  const created = Date.parse(order.value?.created_at ?? '')
  if (!Number.isFinite(created)) return ''
  const s = Math.max(0, Math.floor((now.value - created) / 1000))
  const mm = Math.floor(s / 60)
  const ss = s % 60
  return `${mm}:${ss < 10 ? '0' : ''}${ss}`
})

// --- Actions -----------------------------------------------------------------------

// Only the passenger cancels, with a reason (§22.3).
const cancelOpen = ref(false)
function cancelTrip(): void {
  if (order.value?.can_cancel) cancelOpen.value = true
}

function onCancelled(o: Order): void {
  cancelOpen.value = false
  live.accept(o)
  toast.show(t('order.cancelled_toast'))
}

function onCancelFailed(): void {
  cancelOpen.value = false
  live.refresh()
}

// The order may stop being cancellable while the sheet is open (driver arrived and started).
watch(
  () => order.value?.can_cancel,
  (can) => {
    if (!can) cancelOpen.value = false
  },
)

const disputeOpen = ref(false)
function onDisputeSent(o: Order): void {
  disputeOpen.value = false
  live.accept(o)
}

function orderAgain(): void {
  const o = order.value
  if (!o) return
  booking.prefill(o.category, placesOfStops(o.stops), { vehicleClass: o.vehicle_class, passengerCount: o.passenger_count, cargoSize: o.cargo_size })
  void router.replace({ name: ROUTE.book, query: { category: o.category } })
}

function protection(): void {
  toast.show({ message: t('order.parcel_protection_text'), duration: 6000 })
}

function shareTrip(): void {
  const o = order.value
  if (!o || !o.driver) return
  const text = t('order.share_text', {
    from: addr(firstStop.value?.address),
    to: addr(lastStop.value?.address),
    driver: o.driver.first_name,
    vehicle: [o.vehicle?.make, o.vehicle?.model].filter(Boolean).join(' '),
    plate: o.vehicle?.plate ?? '',
  })
  openTelegram(`https://t.me/share/url?url=${encodeURIComponent('https://t.me')}&text=${encodeURIComponent(text)}`)
}

async function safety(): Promise<void> {
  const support = session.config.support
  const choice = await showActionSheet({
    title: t('order.safety'),
    actions: [
      { id: 'call', label: t('order.safety_call') },
      ...(support.telegram_link ? [{ id: 'support', label: t('order.safety_support') }] : []),
    ],
  })
  if (choice === 'call') callPhone('112')
  else if (choice === 'support' && support.telegram_link) openTelegram(support.telegram_link)
}

async function more(): Promise<void> {
  const choice = await showActionSheet({
    actions: [
      { id: 'share', label: t('order.share') },
      { id: 'support', label: t('menu.support') },
    ],
  })
  if (choice === 'share') shareTrip()
  else if (choice === 'support') void router.push({ name: ROUTE.support })
}

function toHome(): void {
  void router.replace({ name: ROUTE.home })
}
</script>

<template>
  <JSheet
    v-model:snap="snap"
    :snap-points="snapPoints"
    tab-bar
    :aria-label="order ? headline : t('common.loading')"
    @height="(h) => (stage.sheetHeight.value = h)"
  >
    <template #header>
      <div v-if="order" class="trip-head" aria-live="polite">
        <div class="trip-head__row">
          <JSpinner v-if="order.status === 'searching'" size="small" />
          <h1 class="trip-head__title">{{ headline }}</h1>
        </div>
        <p class="trip-head__meta">
          <template v-if="order.status === 'searching'">
            <span class="jt-tabular">{{ t('order.searching_sent', { time: elapsed }) }}</span>
          </template>
          <template v-else>
            <span class="jt-tabular">{{ createdAt }}</span>
            <span aria-hidden="true">·</span>
            <span>{{ t('order.number', { n: order.number }) }}</span>
          </template>
        </p>
      </div>
      <div v-else-if="live.loading.value" class="trip-head">
        <JSkeleton width="60%" height="24px" />
        <JSkeleton width="30%" height="14px" />
      </div>
      <div v-if="order && liveTrip && order.driver" class="trip-driver">
        <p v-if="order.status === 'driver_arriving'" class="trip-text">{{ t('order.arriving_text') }}</p>
        <TripDriverCard :driver="order.driver" :vehicle="order.vehicle" :photo-url="photoUrl" @safety="safety" @share="shareTrip" />
      </div>
    </template>

    <!-- Not found / failed first load -->
    <div v-if="!order && !live.loading.value" class="trip-empty">
      <JEmptyState
        :title="notFound ? t('order.not_found_title') : t('app.load_failed_title')"
        :description="notFound ? t('order.not_found_text') : errorText(live.error.value)"
      >
        <template #action>
          <JButton v-if="!notFound" block @click="live.refresh()">{{ t('common.retry') }}</JButton>
          <JButton block :variant="notFound ? 'primary' : 'plain'" @click="toHome">{{ t('order.to_home') }}</JButton>
        </template>
      </JEmptyState>
    </div>

    <div v-else-if="!order" class="trip-body">
      <JSkeleton :lines="3" />
    </div>

    <!-- Live trip: driver, actions, order card -->
    <div v-else-if="liveTrip && order.driver" class="trip-body">

      <button v-if="delivery" type="button" class="trip-note" @click="protection">
        <ShieldCheck :size="24" :stroke-width="1.8" aria-hidden="true" />
        <span>{{ t('order.parcel_protection') }}</span>
        <ChevronRight :size="20" :stroke-width="2.25" aria-hidden="true" />
      </button>

      <section class="trip-order">
        <p class="trip-order__fare jt-tabular">{{ formatMoney(order.fare, lang) }} · {{ t(`payment_method.${order.payment_method}`) }}</p>
        <span class="trip-order__chips">
          <span class="trip-order__chip">{{ categoryChip }}</span>
          <span class="trip-order__chip">{{ className }}</span>
          <span v-if="loadLine" class="trip-order__chip">{{ loadLine }}</span>
        </span>
        <ol class="trip-route">
          <li v-if="firstStop" class="trip-route__row">
            <span class="trip-route__dot is-start" aria-hidden="true" />
            <span class="trip-route__address">
              {{ addr(firstStop.address) }}
              <span v-if="locality" class="trip-place">{{ locality }}</span>
            </span>
          </li>
          <li v-if="lastStop" class="trip-route__row">
            <span class="trip-route__dot is-end" aria-hidden="true" />
            <span class="trip-route__address">{{ addr(lastStop.address) }}</span>
            <span v-if="extraStops > 0" class="trip-route__extra">{{ t('order.extra_stops', { n: extraStops }, extraStops) }}</span>
          </li>
        </ol>
        <p v-if="order.comment" class="trip-order__comment">{{ order.comment }}</p>
        <p v-if="delivery && order.cargo_description" class="trip-order__comment">{{ order.cargo_description }}</p>
        <p v-if="order.recipient_phone" class="trip-order__comment">
          <span class="trip-card__label">{{ t('order.recipient') }}</span>
          <a class="jt-tabular" :href="telHref(order.recipient_phone)" target="_blank" rel="noopener noreferrer" data-sheet-no-drag>{{ formatKzPhone(order.recipient_phone) }}</a>
        </p>
      </section>
    </div>

    <div v-else class="trip-body">
      <!-- Searching: compact route, fare and payment -->
      <section v-if="order.status === 'searching'" class="trip-list">
        <div v-if="firstStop" class="trip-list__row">
          <span class="trip-list__badge" aria-hidden="true">A</span>
          <span class="trip-list__text">
            {{ addr(firstStop.address) }}
            <span v-if="locality" class="trip-place">{{ locality }}</span>
          </span>
        </div>
        <div v-if="lastStop" class="trip-list__row">
          <span class="trip-list__badge" aria-hidden="true">{{ String.fromCharCode(64 + order.stops.length) }}</span>
          <span class="trip-list__text">{{ addr(lastStop.address) }}</span>
          <span v-if="extraStops > 0" class="trip-list__meta">{{ t('order.extra_stops', { n: extraStops }, extraStops) }}</span>
        </div>
        <div class="trip-list__row">
          <VehicleClassIcon :icon="classIcon" :size="28" class="trip-list__icon" />
          <span class="trip-list__text">{{ className }}</span>
          <span v-if="loadLine" class="trip-list__meta">{{ loadLine }}</span>
        </div>
        <div class="trip-list__row">
          <Banknote :size="24" :stroke-width="1.8" class="trip-list__icon" aria-hidden="true" />
          <span class="trip-list__text jt-tabular"><strong>{{ formatMoney(order.fare, lang) }}</strong> · {{ t(`payment_method.${order.payment_method}`) }}</span>
        </div>
      </section>

      <!-- Cancelled -->
      <div v-if="order.status === 'cancelled' && cancel" class="trip-cancel">
        <p v-if="cancel.byKey" class="trip-text">{{ t(cancel.byKey) }}</p>
        <p v-if="cancel.reasonKey" class="trip-text" :class="{ 'is-secondary': cancel.byKey }">{{ t(cancel.reasonKey) }}</p>
        <p v-if="cancel.labelKey" class="trip-text is-secondary">{{ t('order.reason_text', { reason: t(cancel.labelKey) }) }}</p>
        <p v-if="cancel.text" class="trip-text is-secondary">{{ cancel.labelKey ? cancel.text : t('order.reason_text', { reason: cancel.text }) }}</p>
      </div>

      <!-- Disputed -->
      <div v-if="order.dispute" class="trip-dispute">
        <p class="trip-text">{{ order.dispute.status === 'open' ? t('dispute.status_open') : t('dispute.status_resolved') }}</p>
        <p v-if="order.status === 'disputed'" class="trip-text is-secondary">{{ t('order.disputed_text') }}</p>
        <p v-if="order.dispute.resolution" class="trip-text is-secondary">{{ t('dispute.resolution', { text: order.dispute.resolution }) }}</p>
      </div>

      <!-- Finished orders: route and facts -->
      <section v-if="order.status !== 'searching'" class="trip-card">
        <OrderStopList :stops="order.stops" :show-done="order.status === 'in_progress'" :pickup-locality="locality" />
        <p class="trip-card__fare jt-tabular">{{ farePayment }}</p>
        <p class="trip-card__line">
          <span class="trip-card__label">{{ t('confirm.vehicle_class') }}</span> {{ className }}<template v-if="loadLine"> · {{ loadLine }}</template>
        </p>
        <p v-if="delivery && order.cargo_description" class="trip-card__line">
          <span class="trip-card__label">{{ t('confirm.description') }}</span> {{ order.cargo_description }}
        </p>
        <p v-if="order.comment" class="trip-card__line">
          <span class="trip-card__label">{{ t('detail.comment') }}</span> {{ order.comment }}
        </p>
      </section>

      <!-- Completed: rating and problems -->
      <template v-if="order.status === 'completed'">
        <RatingForm v-if="order.can_rate" :order-id="order.id" @rated="live.accept" />
        <p v-else-if="order.rating" class="trip-rated">
          {{ t('rating.your_rating') }}: <span class="jt-tabular">{{ '★'.repeat(order.rating.rating) }}</span>
        </p>
      </template>

      <DisputeForm v-if="disputeOpen && order.can_dispute" :order-id="order.id" @sent="onDisputeSent" @cancel="disputeOpen = false" />

      <div class="trip-actions">
        <JButton v-if="order.status === 'cancelled'" block @click="orderAgain">{{ t('order.order_again') }}</JButton>
        <JButton v-if="order.can_dispute && !disputeOpen" block variant="secondary" @click="disputeOpen = true">
          {{ t('dispute.action') }}
        </JButton>
        <JButton v-if="order.can_cancel" block variant="secondary" @click="cancelTrip">
          {{ t('order.cancel') }}
        </JButton>
        <JButton v-if="order.status === 'completed' || order.status === 'cancelled' || order.status === 'disputed'" block variant="plain" @click="toHome">
          {{ t('order.to_home') }}
        </JButton>
      </div>
    </div>

    <template v-if="liveTrip && order" #footer>
      <div class="trip-footer">
        <JButton v-if="order.can_cancel" class="trip-footer__main" variant="secondary" @click="cancelTrip">
          {{ t('order.cancel_trip') }}
        </JButton>
        <button type="button" class="trip-footer__more" :aria-label="t('order.more')" @click="more">
          <Ellipsis :size="24" :stroke-width="2.25" aria-hidden="true" />
        </button>
      </div>
    </template>
  </JSheet>
  <CancelOrderSheet v-if="order" :open="cancelOpen" :order="order" @close="cancelOpen = false" @cancelled="onCancelled" @failed="onCancelFailed" />
</template>

<style scoped>
.trip-head {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: var(--jt-space-3);
}

.trip-head__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
}

.trip-head__title {
  font: var(--jt-font-title2);
}

.trip-driver {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding-bottom: var(--jt-space-4);
}

.trip-note {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 56px;
  padding: 0 var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-headline);
  font-weight: 500;
  text-align: left;
}

.trip-note span {
  flex: 1;
}

.trip-note:active {
  opacity: var(--jt-opacity-pressed);
}

.trip-order {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--jt-space-2);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.trip-order__fare {
  font: var(--jt-font-headline);
  font-size: 19px;
}

.trip-order__chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--jt-space-2);
}

.trip-order__chip {
  padding: 4px 12px;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-tertiary);
  font: var(--jt-font-subheadline);
}

.trip-order__comment {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.trip-order__comment a {
  color: var(--jt-color-label);
  font-weight: 600;
}

.trip-route {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  width: 100%;
  margin-top: 2px;
}

.trip-route__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 26px;
}

.trip-route__dot {
  flex: none;
  width: 12px;
  height: 12px;
  margin: 0 3px;
  border-radius: 50%;
  background: var(--jt-color-gray);
}

.trip-route__dot.is-start {
  background: var(--jt-color-blue);
  box-shadow: 0 0 0 4px rgba(0, 122, 255, 0.18);
}

.trip-route__dot.is-end {
  background: var(--jt-color-green);
  box-shadow: 0 0 0 4px rgba(52, 199, 89, 0.18);
}

.trip-route__address {
  flex: 1;
  min-width: 0;
  font: var(--jt-font-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.trip-route__extra,
.trip-list__meta {
  flex: none;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.trip-list {
  display: flex;
  flex-direction: column;
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.trip-list__row {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 52px;
  padding: 0 var(--jt-space-4);
}

.trip-list__row + .trip-list__row::before {
  content: '';
  position: absolute;
  top: 0;
  left: 52px;
  right: 0;
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.trip-list__badge {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: 600 13px/1 var(--jt-font-family);
}

.trip-list__icon {
  flex: none;
}

.trip-list__text {
  flex: 1;
  min-width: 0;
  font: var(--jt-font-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The settlement of pickup A under its address ("Алматы"). */
.trip-place {
  display: block;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
}

.trip-footer {
  display: flex;
  gap: var(--jt-space-2);
}

.trip-footer__main {
  flex: 1;
}

.trip-footer__more {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 52px;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  transition: transform var(--jt-duration-spring) var(--jt-ease-spring);
}

.trip-footer__more:active {
  transform: scale(0.96);
}

.trip-head__meta {
  display: flex;
  gap: 6px;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.trip-body {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding-bottom: var(--jt-space-2);
}

.trip-empty {
  padding: var(--jt-space-4) 0;
}

.trip-text {
  font: var(--jt-font-callout);
}

.trip-text.is-secondary {
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.trip-cancel,
.trip-dispute {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.trip-card {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.trip-card__fare {
  padding-top: var(--jt-space-3);
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
  font: var(--jt-font-headline);
}

.trip-card__line {
  font: var(--jt-font-subheadline);
  overflow-wrap: anywhere;
}

.trip-card__label {
  color: var(--jt-color-label-secondary);
}

.trip-card__line a {
  color: var(--jt-color-label);
  font-weight: 600;
}

.trip-rated {
  font: var(--jt-font-callout);
  text-align: center;
}

.trip-actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}
</style>
