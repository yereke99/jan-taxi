<!--
  Order detail from "My orders": a static map of the route, the stops, the
  trip facts (the place first: the settlement of pickup A), the driver,
  rating / dispute actions and the status timeline.
-->
<script setup lang="ts">
import type { Map as MapLibreMap } from 'maplibre-gl'
import { computed, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import type { Order } from '../api/types'
import CategoryIcon from '../components/CategoryIcon.vue'
import DisputeForm from '../components/order/DisputeForm.vue'
import DriverCard from '../components/order/DriverCard.vue'
import OrderStopList from '../components/order/OrderStopList.vue'
import OrderTimeline from '../components/order/OrderTimeline.vue'
import RatingForm from '../components/order/RatingForm.vue'
import ScrollPage from '../components/ScrollPage.vue'
import { JBadge, JButton, JEmptyState, JSkeleton } from '../components/ui'
import VehicleClassIcon from '../components/VehicleClassIcon.vue'
import { useDriverPhoto } from '../composables/useDriverPhoto'
import { useLiveOrder } from '../composables/useLiveOrder'
import { useVehicleText } from '../composables/useVehicleText'
import { cancelInfo, orderLocality, orderMarkers } from '../domain/orderView'
import { placesOfStops } from '../domain/places'
import { useErrorText } from '../i18n/core'
import { MapView, useRouteLayer, useStopMarkers } from '../maps'
import { ROUTE } from '../router/names'
import { VEHICLE_ICONS, type VehicleIcon } from '../services/config'
import { isApiError } from '../services/http'
import { useBookingStore } from '../stores/booking'
import { useOrdersStore } from '../stores/orders'
import { useSessionStore } from '../stores/session'
import { formatDateTime, formatDistance, formatDuration, formatMoney } from '../utils/format'
import { boundsOf, type LatLon } from '../utils/geo'
import { formatKzPhone } from '../utils/phone'
import { isActiveOrderStatus, orderStatusTone } from '../utils/status'

const { t } = useI18n()
const errorText = useErrorText()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const booking = useBookingStore()
const orders = useOrdersStore()
const vehicleText = useVehicleText()

const orderId = computed(() => String(route.params.id ?? '').toLowerCase())
const live = useLiveOrder(orderId)
const order = live.order
const lang = computed(() => session.language)
const photoUrl = useDriverPhoto(
  computed(() => order.value?.id ?? null),
  computed(() => Boolean(order.value?.driver?.photo_url)),
)
const notFound = computed(() => isApiError(live.error.value) && live.error.value.status === 404)
const title = computed(() => (order.value ? t('order.number', { n: order.value.number }) : t('orders.title')))
const cancel = computed(() => (order.value?.status === 'cancelled' ? cancelInfo(order.value) : null))
const disputeOpen = ref(false)
const className = computed(() => (order.value ? vehicleText.className(order.value.vehicle_class, order.value.vehicle_class_name) : ''))
const classIcon = computed<VehicleIcon>(() => VEHICLE_ICONS.find((i) => i === order.value?.vehicle_class) ?? (order.value?.category === 'taxi' ? 'sedan' : 'cargo_van'))
const loadLine = computed(() => (order.value ? vehicleText.loadText(order.value) : ''))
/** Settlement of pickup A ("Алматы"), or the locality line seen while booking; never coordinates. */
const locality = computed(() => (order.value ? orderLocality(order.value, orders.pickupSubtitle(order.value.id)) : null))

// --- Static route map ------------------------------------------------------------

const map = shallowRef<MapLibreMap | null>(null)
const routeLayer = useRouteLayer(map)
const markers = useStopMarkers(map)

function drawRoute(o: Order, m: MapLibreMap): void {
  markers.setStops(orderMarkers(o).map((p) => ({ ...p, pulse: false })))
  routeLayer.setRoute(o.route.geometry)
  const pts: LatLon[] = [...o.stops]
  const b = o.route.bbox
  if (b) pts.push({ lon: b[0], lat: b[1] }, { lon: b[2], lat: b[3] })
  const bounds = boundsOf(pts)
  // Extra room at the bottom for the map attribution row.
  if (bounds) m.fitBounds(bounds, { padding: { top: 28, right: 28, bottom: 64, left: 28 }, maxZoom: 16, duration: 0 })
}

watch(
  () => [order.value, map.value] as const,
  ([o, m]) => {
    if (o && m) drawRoute(o, m)
  },
)

function onMapReady(m: MapLibreMap): void {
  map.value = m
}

function orderAgain(): void {
  const o = order.value
  if (!o) return
  booking.prefill(o.category, placesOfStops(o.stops), { vehicleClass: o.vehicle_class, passengerCount: o.passenger_count, cargoSize: o.cargo_size })
  void router.push({ name: ROUTE.book, query: { category: o.category } })
}

function onDisputeSent(o: Order): void {
  disputeOpen.value = false
  live.accept(o)
}
</script>

<template>
  <ScrollPage :title="title">
    <div v-if="!order && live.loading.value" class="detail-loading">
      <JSkeleton height="232px" radius="22px" />
      <JSkeleton :lines="3" />
    </div>

    <JEmptyState
      v-else-if="!order"
      class="detail-empty"
      :title="notFound ? t('order.not_found_title') : t('app.load_failed_title')"
      :description="notFound ? t('order.not_found_text') : errorText(live.error.value)"
    >
      <template v-if="!notFound" #action>
        <JButton block @click="live.refresh()">{{ t('common.retry') }}</JButton>
      </template>
    </JEmptyState>

    <template v-else>
      <div class="detail-status">
        <JBadge :tone="orderStatusTone(order.status)" :label="t(`order_status.${order.status}`)" />
        <span class="detail-status__date">{{ formatDateTime(order.created_at, lang) }}</span>
      </div>

      <div class="detail-map">
        <MapView
          :style-url="session.config.map.style_url"
          :center="order.stops[0] ?? session.config.map.default_center"
          :zoom="13"
          :interactive="false"
          @ready="onMapReady"
        />
      </div>

      <section class="detail-card">
        <OrderStopList :stops="order.stops" :show-done="order.status === 'in_progress'" />
      </section>

      <section class="detail-card detail-facts">
        <div v-if="locality" class="detail-fact">
          <span class="detail-fact__label">{{ t('order.locality') }}</span>
          <span class="detail-fact__value">{{ locality }}</span>
        </div>
        <div class="detail-fact">
          <span class="detail-fact__label">{{ t('confirm.service') }}</span>
          <span class="detail-fact__value detail-fact__service"><CategoryIcon :category="order.category" :size="18" />{{ t(`category.${order.category}`) }}</span>
        </div>
        <div class="detail-fact">
          <span class="detail-fact__label">{{ t('confirm.vehicle_class') }}</span>
          <span class="detail-fact__value detail-fact__service"><VehicleClassIcon :icon="classIcon" :size="28" />{{ className }}</span>
        </div>
        <div v-if="loadLine" class="detail-fact">
          <span class="detail-fact__label">{{ order.category === 'taxi' ? t('confirm.passengers') : t('confirm.cargo_size') }}</span>
          <span class="detail-fact__value jt-tabular">{{ order.cargo_size ? vehicleText.cargoText(order.cargo_size, order.cargo_volume_l) : order.passenger_count }}</span>
        </div>
        <div class="detail-fact">
          <span class="detail-fact__label">{{ t('detail.fare') }}</span>
          <span class="detail-fact__value jt-tabular">{{ formatMoney(order.fare, lang) }}</span>
        </div>
        <div class="detail-fact">
          <span class="detail-fact__label">{{ t('detail.payment') }}</span>
          <span class="detail-fact__value">{{ t(`payment_method.${order.payment_method}`) }}</span>
        </div>
        <div class="detail-fact">
          <span class="detail-fact__label">{{ t('confirm.distance') }}</span>
          <span class="detail-fact__value jt-tabular">{{ formatDistance(order.distance_m, lang) }}</span>
        </div>
        <div class="detail-fact">
          <span class="detail-fact__label">{{ t('confirm.duration') }}</span>
          <span class="detail-fact__value jt-tabular">{{ formatDuration(order.duration_s, lang) }}</span>
        </div>
        <div v-if="order.cargo_description" class="detail-fact">
          <span class="detail-fact__label">{{ t('confirm.description') }}</span>
          <span class="detail-fact__value">{{ order.cargo_description }}</span>
        </div>
        <div v-if="order.recipient_phone" class="detail-fact">
          <span class="detail-fact__label">{{ t('order.recipient') }}</span>
          <span class="detail-fact__value jt-tabular">{{ formatKzPhone(order.recipient_phone) }}</span>
        </div>
        <div v-if="order.comment" class="detail-fact">
          <span class="detail-fact__label">{{ t('detail.comment') }}</span>
          <span class="detail-fact__value">{{ order.comment }}</span>
        </div>
      </section>

      <section v-if="cancel" class="detail-card detail-text">
        <p v-if="cancel.byKey">{{ t(cancel.byKey) }}</p>
        <p v-if="cancel.reasonKey" :class="{ 'is-secondary': cancel.byKey }">{{ t(cancel.reasonKey) }}</p>
        <p v-if="cancel.labelKey" class="is-secondary">{{ t('order.reason_text', { reason: t(cancel.labelKey) }) }}</p>
        <p v-if="cancel.text" class="is-secondary">{{ cancel.labelKey ? cancel.text : t('order.reason_text', { reason: cancel.text }) }}</p>
      </section>

      <section v-if="order.driver" class="detail-card">
        <DriverCard :driver="order.driver" :vehicle="order.vehicle" :photo-url="photoUrl" :contact="false" />
      </section>

      <section v-if="order.dispute" class="detail-card detail-text">
        <p>{{ order.dispute.status === 'open' ? t('dispute.status_open') : t('dispute.status_resolved') }}</p>
        <p class="is-secondary">{{ order.dispute.reason }}</p>
        <p v-if="order.dispute.resolution" class="is-secondary">{{ t('dispute.resolution', { text: order.dispute.resolution }) }}</p>
      </section>

      <section v-if="order.can_rate" class="detail-card">
        <RatingForm :order-id="order.id" @rated="live.accept" />
      </section>
      <section v-else-if="order.rating" class="detail-card detail-text">
        <p>{{ t('rating.your_rating') }}: <span class="jt-tabular">{{ '★'.repeat(order.rating.rating) }}</span></p>
        <p v-if="order.rating.comment" class="is-secondary">{{ order.rating.comment }}</p>
      </section>

      <section v-if="disputeOpen && order.can_dispute" class="detail-card">
        <DisputeForm :order-id="order.id" @sent="onDisputeSent" @cancel="disputeOpen = false" />
      </section>

      <section v-if="order.events.length > 0" class="detail-section">
        <h2 class="detail-section__title">{{ t('detail.timeline') }}</h2>
        <div class="detail-card">
          <OrderTimeline :events="order.events" :locale="lang" />
        </div>
      </section>

      <div class="detail-actions">
        <JButton v-if="isActiveOrderStatus(order.status)" block @click="router.push({ name: ROUTE.order, params: { id: order.id } })">
          {{ t('detail.open_live') }}
        </JButton>
        <JButton v-else block @click="orderAgain">{{ t('order.order_again') }}</JButton>
        <JButton v-if="order.can_dispute && !disputeOpen" block variant="secondary" @click="disputeOpen = true">
          {{ t('dispute.action') }}
        </JButton>
      </div>
    </template>
  </ScrollPage>
</template>

<style scoped>
.detail-loading {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin);
}

.detail-empty {
  margin-top: var(--jt-space-8);
  padding: 0 var(--jt-side-margin);
}

.detail-status {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
}

.detail-status__date {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.detail-map {
  position: relative;
  height: 232px;
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  overflow: hidden;
  isolation: isolate;
}

.detail-card {
  margin: 0 var(--jt-side-margin) var(--jt-space-4);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.detail-facts {
  padding-top: var(--jt-space-1);
  padding-bottom: var(--jt-space-1);
}

.detail-fact {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-4);
  padding: 11px 0;
  font: var(--jt-font-subheadline);
}

.detail-fact + .detail-fact {
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.detail-fact__label {
  flex: none;
  color: var(--jt-color-label-secondary);
}

.detail-fact__value {
  min-width: 0;
  text-align: right;
  overflow-wrap: anywhere;
}

.detail-fact__service {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.detail-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font: var(--jt-font-callout);
}

.detail-text .is-secondary {
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.detail-section__title {
  margin: 0 var(--jt-side-margin);
  padding: 0 var(--jt-space-4) 7px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
}

.detail-actions {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: var(--jt-space-2) var(--jt-side-margin) 0;
}
</style>
