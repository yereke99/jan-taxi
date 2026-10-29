<!--
  Home ("Тапсырыс беру" tab): the map dominates; a black pickup pin sits at
  the map centre with a callout above it («Алып кететін жер» + the address).
  Moving the map moves the pickup: the pin lifts, the callout shows a loading
  bar, and once the camera settles the point is reverse geocoded (debounced;
  a newer movement aborts the older request). Tapping the callout edits the
  pickup. The sheet carries "Where to?", recent places and the three service
  tiles. Embi is only the initial map centre until the device position is known.
-->
<script setup lang="ts">
import { CarFront, ChevronRight, MapPin, Package, Search, Truck } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch, watchEffect, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { reverseGeocode } from '../../api/geo'
import type { GeoReverse, OrderSummary } from '../../api/types'
import CategoryIcon from '../../components/CategoryIcon.vue'
import { JSheet } from '../../components/ui'
import { createLatest } from '../../domain/latest'
import type { PlaceValue } from '../../domain/route'
import { BOOKING_FROM_HOME, ROUTE } from '../../router/names'
import { useRealtime } from '../../services/realtime'
import { useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { haptics } from '../../telegram'
import { formatMoney } from '../../utils/format'
import { hasCoordinates, haversineMeters, roundCoord, type LatLon } from '../../utils/geo'
import { CATEGORIES, type Category } from '../../utils/status'
import { useAddressText } from '../../i18n/core'

/** Pin movements shorter than this keep the resolved address (GPS jitter, follow mode). */
const PIN_SAME_PLACE_M = 12
/** A pin this close to the device position counts as "pickup from GPS". */
const PIN_IS_GPS_M = 25
const RECENT_LIMIT = 2

const { t } = useI18n()
const addr = useAddressText()
const router = useRouter()
const stage = useStageScreen()
const session = useSessionStore()
const booking = useBookingStore()
const orders = useOrdersStore()
const location = useLocationStore()
const realtime = useRealtime()

stage.controls.menu = true
stage.controls.locate = true
stage.pin.visible = true
stage.pin.label = t('home.pickup_caption')
stage.pin.onCallout = editPickup

const SERVICE_ICON: Record<Category, Component> = { taxi: CarFront, parcel: Package, other: Truck }

const whereTo = computed(() =>
  booking.category === 'parcel' ? t('home.where_to_parcel') : booking.category === 'other' ? t('home.where_to_other') : t('home.where_to'),
)
const recent = computed(() => orders.destinations.slice(0, RECENT_LIMIT))

// --- Pickup pin ----------------------------------------------------------------

const pinLoading = ref(true)
let pending: LatLon | null = null

watchEffect(() => {
  const place = booking.stops[0]?.place
  stage.pin.callout = {
    caption: t('home.pickup_caption'),
    title: !place ? '' : place.inServiceArea === false ? t('home.outside_kz') : addr(place.address),
    loading: pinLoading.value,
  }
})

/** One retry, 2 s after a failed lookup (slow public geocoder). */
const RETRY_MS = 2000
let retryTimer: ReturnType<typeof setTimeout> | undefined
let retried: LatLon | null = null

const runner = createLatest<LatLon, GeoReverse>({
  delayMs: 250,
  run: (p, signal) => reverseGeocode(p, { lang: session.language, signal }),
  onResult: (r, p) => {
    if (p !== pending) return
    pinLoading.value = false
    const device = location.current
    const fromGps = device !== null && haversineMeters(device, p) <= PIN_IS_GPS_M
    booking.setStopPlace(0, {
      lat: p.lat,
      lon: p.lon,
      address: r.title,
      subtitle: r.title ? r.subtitle : null,
      source: fromGps ? 'gps' : 'map',
      inServiceArea: r.in_service_area,
    })
    if (fromGps) booking.pickupAccuracy = device.accuracy_m
    if (!r.in_service_area) haptics.notification('warning')
  },
  onError: (_err, p) => {
    if (p !== pending) return
    if (retried !== p) {
      retried = p
      retryTimer = setTimeout(() => {
        if (pending === p) runner.schedule(p)
      }, RETRY_MS)
      return
    }
    pinLoading.value = false
    // Still unnamed: the pickup is the point itself ("Point on the map").
    booking.setStopPlace(0, { lat: p.lat, lon: p.lon, address: '', subtitle: null, source: 'map', inServiceArea: null })
  },
})

function resolvePin(center: LatLon): void {
  // Already the pickup (resolved earlier, chosen in search or filled from GPS).
  const place = booking.stops[0]?.place
  if (place && hasCoordinates(place) && haversineMeters(place, center) < PIN_SAME_PLACE_M) {
    runner.cancel()
    pending = null
    pinLoading.value = false
    return
  }
  // The lookup for (practically) this point is already running.
  if (pending && runner.busy && haversineMeters(pending, center) < PIN_SAME_PLACE_M) return
  pending = { lat: roundCoord(center.lat), lon: roundCoord(center.lon) }
  pinLoading.value = true
  runner.schedule(pending)
}

/** The camera was centred on the pickup / passenger, or the passenger moved it. */
let centred = false
let userMoved = false

// A drag supersedes the lookup in flight; camera animations (follow mode,
// sheet changes) do not, or a moving passenger would never get an address.
const offMoveStart = stage.onMoveStart((user) => {
  if (user) runner.cancel()
})
const offMoveEnd = stage.onMoveEnd((center) => {
  // Camera settling (sheet padding) before the first fix is not a pickup.
  if (!centred && !userMoved && location.locating) return
  resolvePin(center)
})

// Centre once: on the chosen pickup, else on the passenger as soon as the
// position is known. The initial map centre (the default city) is named only
// after the GPS attempt failed, so it never becomes the pickup of a passenger
// elsewhere just because the first fix was slow.
const offUserMove = stage.onMoveStart((user) => {
  if (user) userMoved = true
})
watch(
  () => [stage.map.value, location.current, location.locating] as const,
  ([map, loc, locating]) => {
    if (!map || centred || userMoved) return
    const target = booking.stops[0]?.place ?? loc
    if (target && hasCoordinates(target)) {
      centred = true
      stage.centerOn(target, 16)
    } else if (!locating && pending === null) {
      const c = stage.center()
      if (c) resolvePin(c)
    }
  },
  { immediate: true },
)

// --- Actions -------------------------------------------------------------------

function editPickup(): void {
  void router.push({ name: ROUTE.bookSearch, params: { index: '0' } })
}

async function openBooking(): Promise<void> {
  const last = booking.stops.length - 1
  if (booking.stops[last]?.place) {
    await router.push({ name: ROUTE.book, query: { category: booking.category } })
    return
  }
  // "Where to?" opens the search straight from home: back returns home, a
  // chosen place continues to the route editor (BOOKING_FROM_HOME).
  await router.push({ name: ROUTE.bookSearch, params: { index: String(last) }, query: { from: BOOKING_FROM_HOME } })
}

function openService(c: Category): void {
  haptics.selection()
  booking.setCategory(c)
  void openBooking()
}

async function openRecent(place: PlaceValue): Promise<void> {
  booking.setStopPlace(booking.stops.length - 1, place)
  await router.push({ name: ROUTE.book, query: { category: booking.category } })
}

function openOrder(o: OrderSummary): void {
  void router.push({ name: ROUTE.order, params: { id: o.id } })
}

function refreshLists(): void {
  void orders.loadActive()
}

const offs: Array<() => void> = []
onMounted(() => {
  // Home starts a new booking: the vehicle class goes back to the default.
  booking.startNewBooking()
  void orders.loadActive()
  if (!orders.historyLoaded) void orders.loadHistory()
  offs.push(
    realtime.on('*', (e) => {
      if (e.type.startsWith('order.')) refreshLists()
    }),
    realtime.onResync(refreshLists),
  )
})
onBeforeUnmount(() => {
  clearTimeout(retryTimer)
  runner.cancel()
  offMoveStart()
  offMoveEnd()
  offUserMove()
  for (const off of offs.splice(0)) off()
})
</script>

<template>
  <JSheet snap="content" :snap-points="['content']" tab-bar :aria-label="t('home.where_to')" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <div class="home-head">
        <button type="button" class="home-where" @click="openBooking">
          <Search :size="22" :stroke-width="2.25" aria-hidden="true" />
          <span>{{ whereTo }}</span>
        </button>
      </div>
    </template>

    <section v-if="orders.active.length > 0" class="home-section" :aria-label="t('home.active_title')">
      <button v-for="o in orders.active" :key="o.id" type="button" class="home-active" @click="openOrder(o)">
        <span class="home-active__icon"><CategoryIcon :category="o.category" /></span>
        <span class="home-active__text">
          <span class="home-active__status">{{ t(`order_status.${o.status}`) }}</span>
          <span class="home-active__route">{{ addr(o.pickup.address) }} → {{ addr(o.destination.address) }}</span>
        </span>
        <span class="home-active__fare jt-tabular">{{ formatMoney(o.fare, session.language) }}</span>
        <ChevronRight :size="20" class="home-chevron" aria-hidden="true" />
      </button>
    </section>

    <section v-if="recent.length > 0" class="home-recent-list" :aria-label="t('home.recent')">
      <button v-for="p in recent" :key="`${p.lat},${p.lon}`" type="button" class="home-recent" @click="openRecent(p)">
        <MapPin :size="22" :stroke-width="1.9" class="home-recent__icon" aria-hidden="true" />
        <span class="home-recent__title">{{ addr(p.address) }}</span>
      </button>
    </section>

    <nav class="home-services" :aria-label="t('home.services')">
      <button
        v-for="c in CATEGORIES"
        :key="c"
        type="button"
        class="home-service"
        :class="{ 'is-selected': booking.category === c }"
        @click="openService(c)"
      >
        <span class="home-service__label">{{ t(`category.${c}`) }}</span>
        <component :is="SERVICE_ICON[c]" :size="40" :stroke-width="1.4" class="home-service__icon" aria-hidden="true" />
      </button>
    </nav>
  </JSheet>
</template>

<style scoped>
.home-head {
  padding-bottom: var(--jt-space-2);
}

.home-where {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: 100%;
  height: 56px;
  padding: 0 var(--jt-space-5);
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-title3);
  text-align: left;
}

.home-where:active {
  opacity: var(--jt-opacity-pressed);
}

.home-section {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin: var(--jt-space-2) 0;
}

.home-active {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 60px;
  padding: var(--jt-space-3) var(--jt-space-3) var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  text-align: left;
}

.home-active:active {
  opacity: 0.8;
}

.home-active__icon {
  flex: none;
  display: flex;
}

.home-active__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.home-active__status {
  font: var(--jt-font-headline);
}

.home-active__route {
  font: var(--jt-font-footnote);
  opacity: 0.75;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-active__fare {
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.home-chevron {
  flex: none;
  opacity: 0.6;
}

.home-recent-list {
  display: flex;
  flex-direction: column;
}

.home-recent {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
  min-height: 50px;
  padding: 0 var(--jt-space-1);
  text-align: left;
}

.home-recent:active {
  opacity: var(--jt-opacity-pressed);
}

.home-recent__icon {
  flex: none;
  color: var(--jt-color-label-secondary);
}

.home-recent__title {
  flex: 1;
  min-width: 0;
  font: var(--jt-font-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-services {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-3);
}

.home-service {
  position: relative;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  height: 92px;
  padding: var(--jt-space-3) var(--jt-space-3) var(--jt-space-2);
  border-radius: 20px;
  background: var(--jt-color-fill-secondary);
  text-align: left;
  overflow: hidden;
  transition: transform var(--jt-duration-spring) var(--jt-ease-spring);
}

.home-service:active {
  transform: scale(0.96);
}

.home-service.is-selected {
  box-shadow: inset 0 0 0 1.5px var(--jt-color-label);
}

.home-service__label {
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.home-service__icon {
  align-self: flex-end;
  color: var(--jt-color-label);
}
</style>
