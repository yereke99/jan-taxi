<!--
  Live order. Searching: pulsing pickup, elapsed time, summary, cancel.
  Accepted / driver arriving / in progress: the driver card with a large
  plate, the live car marker, the route with completed stops greyed, call
  and cancel (only when the API allows it). Completed: summary, rating and
  "report a problem". Cancelled: the reason and "order again". State follows
  realtime events with REST re-sync and a polling fallback (useLiveOrder).
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { cancelOrder } from '../../api/orders'
import type { Order } from '../../api/types'
import DisputeForm from '../../components/order/DisputeForm.vue'
import DriverCard from '../../components/order/DriverCard.vue'
import OrderStopList from '../../components/order/OrderStopList.vue'
import RatingForm from '../../components/order/RatingForm.vue'
import { confirmDestructive, JButton, JEmptyState, JSheet, JSkeleton, JSpinner, toast } from '../../components/ui'
import { useDriverPhoto } from '../../composables/useDriverPhoto'
import { useLiveOrder } from '../../composables/useLiveOrder'
import { cancelInfo, framingPoints, hasActiveDriver, headlineKey, isDeliveryOrder, orderMarkers } from '../../domain/orderView'
import { placesOfStops } from '../../domain/places'
import { useErrorText } from '../../i18n/core'
import { straightLine } from '../../maps'
import { ROUTE } from '../../router/names'
import { isApiError } from '../../services/http'
import { useAutoFit, useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { haptics } from '../../telegram'
import { formatMoney } from '../../utils/format'
import { formatKzPhone, telHref } from '../../utils/phone'

const { t } = useI18n()
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
    const approaching = (o.status === 'accepted' || o.status === 'driver_arriving') && driver && pickup
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

// --- Searching timer ---------------------------------------------------------------

const now = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | undefined
watch(
  () => order.value?.status === 'searching',
  (searching) => {
    clearInterval(ticker)
    if (searching) ticker = setInterval(() => (now.value = Date.now()), 1000)
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

const cancelling = ref(false)
async function cancelTrip(): Promise<void> {
  const o = order.value
  if (!o || !o.can_cancel || cancelling.value) return
  const ok = await confirmDestructive({
    title: t('order.cancel_confirm_title'),
    message: o.status === 'searching' ? t('order.cancel_confirm_searching') : t('order.cancel_confirm_assigned'),
    confirmLabel: t('order.cancel_confirm'),
    cancelLabel: t('order.cancel_keep'),
  })
  if (!ok) return
  cancelling.value = true
  try {
    live.accept(await cancelOrder(o.id))
    toast.show(t('order.cancelled_toast'))
  } catch (err) {
    toast.error(errorText(err))
    live.refresh()
  } finally {
    cancelling.value = false
  }
}

const disputeOpen = ref(false)
function onDisputeSent(o: Order): void {
  disputeOpen.value = false
  live.accept(o)
}

function orderAgain(): void {
  const o = order.value
  if (!o) return
  booking.prefill(o.category, placesOfStops(o.stops))
  void router.replace({ name: ROUTE.book, query: { category: o.category } })
}

function toHome(): void {
  void router.replace({ name: ROUTE.home })
}
</script>

<template>
  <JSheet :snap="'content'" :snap-points="['content']" :aria-label="order ? t(headlineKey(order)) : t('common.loading')" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <div v-if="order" class="trip-head" aria-live="polite">
        <div class="trip-head__row">
          <h1 class="trip-head__title">{{ t(headlineKey(order)) }}</h1>
          <JSpinner v-if="order.status === 'searching'" size="small" />
        </div>
        <p class="trip-head__meta">
          <span>{{ t('order.number', { n: order.number }) }}</span>
          <template v-if="order.status === 'searching' && elapsed">
            <span aria-hidden="true">·</span>
            <span class="jt-tabular">{{ t('order.searching_elapsed', { time: elapsed }) }}</span>
          </template>
        </p>
      </div>
      <div v-else-if="live.loading.value" class="trip-head">
        <JSkeleton width="60%" height="24px" />
        <JSkeleton width="30%" height="14px" />
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

    <div v-else class="trip-body">
      <!-- Searching -->
      <p v-if="order.status === 'searching'" class="trip-text">{{ t('order.searching_text') }}</p>

      <!-- Assigned driver -->
      <template v-if="order.driver && order.status !== 'searching' && order.status !== 'cancelled'">
        <p v-if="order.status === 'driver_arriving'" class="trip-text">{{ t('order.arriving_text') }}</p>
        <DriverCard :driver="order.driver" :vehicle="order.vehicle" :photo-url="photoUrl" :contact="activeDriver" />
      </template>

      <!-- Cancelled -->
      <div v-if="order.status === 'cancelled' && cancel" class="trip-cancel">
        <p v-if="cancel.reasonKey" class="trip-text">{{ t(cancel.reasonKey) }}</p>
        <p v-else-if="cancel.byKey" class="trip-text">{{ t(cancel.byKey) }}</p>
        <p v-if="cancel.text" class="trip-text is-secondary">{{ t('order.reason_text', { reason: cancel.text }) }}</p>
      </div>

      <!-- Disputed -->
      <div v-if="order.dispute" class="trip-dispute">
        <p class="trip-text">{{ order.dispute.status === 'open' ? t('dispute.status_open') : t('dispute.status_resolved') }}</p>
        <p v-if="order.status === 'disputed'" class="trip-text is-secondary">{{ t('order.disputed_text') }}</p>
        <p v-if="order.dispute.resolution" class="trip-text is-secondary">{{ t('dispute.resolution', { text: order.dispute.resolution }) }}</p>
      </div>

      <!-- Route and order facts -->
      <section class="trip-card">
        <OrderStopList :stops="order.stops" :show-done="order.status === 'in_progress'" />
        <p class="trip-card__fare jt-tabular">{{ farePayment }}</p>
        <p v-if="delivery && order.cargo_description" class="trip-card__line">
          <span class="trip-card__label">{{ t('confirm.description') }}</span> {{ order.cargo_description }}
        </p>
        <p v-if="order.recipient_phone" class="trip-card__line">
          <span class="trip-card__label">{{ t('order.recipient') }}</span>
          <a class="jt-tabular" :href="telHref(order.recipient_phone)">{{ formatKzPhone(order.recipient_phone) }}</a>
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
        <JButton v-if="order.can_cancel" block variant="destructive" :loading="cancelling" @click="cancelTrip">
          {{ t('order.cancel') }}
        </JButton>
        <JButton v-if="order.status === 'completed' || order.status === 'cancelled' || order.status === 'disputed'" block variant="plain" @click="toHome">
          {{ t('order.to_home') }}
        </JButton>
      </div>
    </div>
  </JSheet>
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
