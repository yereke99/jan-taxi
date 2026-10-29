<!--
  "Choose on map": the map moves under a fixed centre pin. When the camera
  settles the pin position is reverse geocoded (debounced; a newer movement
  supersedes the older request) and the address appears in the callout above
  the pin and in the sheet. Camera changes that do not move the centre (sheet
  padding) never re-query. If geocoding fails the point can still be chosen
  and is named by its coordinates; a point outside Kazakhstan cannot.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { reverseGeocode } from '../../api/geo'
import type { GeoReverse } from '../../api/types'
import { JButton, JSheet, JSkeleton } from '../../components/ui'
import { createLatest } from '../../domain/latest'
import { useAddressText } from '../../i18n/core'
import { BOOKING_FROM_HOME, ROUTE } from '../../router/names'
import { useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { haptics, navigateBack } from '../../telegram'
import { hasCoordinates, haversineMeters, isInKazakhstanBBox, roundCoord, type LatLon } from '../../utils/geo'

/** Centre shifts below this (padding changes, rounding) keep the current address. */
const SAME_POINT_M = 2
const RESOLVE_DELAY_MS = 250

const { t } = useI18n()
const addr = useAddressText()
const route = useRoute()
const router = useRouter()
const stage = useStageScreen()
const booking = useBookingStore()
const location = useLocationStore()
const session = useSessionStore()

const index = computed(() => Number(route.params.index))
const title = computed(() =>
  index.value === 0 ? t('pick.title_pickup') : index.value === booking.stops.length - 1 ? t('pick.title_destination') : t('pick.title_stop'),
)

stage.pin.visible = true
stage.pin.label = t('map.selected_point')
stage.controls.locate = true

const point = ref<LatLon | null>(null)
const result = shallowRef<GeoReverse | null>(null)
const resolving = ref(true)
const failed = ref(false)

const outside = computed(() => result.value?.in_service_area === false || (point.value !== null && !isInKazakhstanBBox(point.value)))
const canConfirm = computed(() => !resolving.value && point.value !== null && !outside.value)
const address = computed(() => (result.value || (point.value && failed.value) ? addr(result.value?.title) : ''))
/** Subtitle under the name: the area line, unless it repeats the label. */
const subtitle = computed(() => {
  const sub = result.value?.subtitle ?? ''
  return sub && sub !== address.value ? sub : ''
})

/** One retry, 2 s after a failed lookup (slow public geocoder). */
const RETRY_MS = 2000
let retryTimer: ReturnType<typeof setTimeout> | undefined
let retried: LatLon | null = null

watchEffect(() => {
  stage.pin.callout = { caption: title.value, title: outside.value ? t('home.outside_kz') : address.value, loading: resolving.value }
})

const runner = createLatest<LatLon, GeoReverse>({
  delayMs: RESOLVE_DELAY_MS,
  run: (p, signal) => reverseGeocode(p, { lang: session.language, signal }),
  onResult: (r, p) => {
    if (p !== point.value) return
    result.value = r
    resolving.value = false
    failed.value = false
    if (!r.in_service_area) haptics.notification('warning')
  },
  onError: (_err, p) => {
    if (p !== point.value) return
    if (retried !== p) {
      retried = p
      retryTimer = setTimeout(() => {
        if (point.value === p) runner.schedule(p)
      }, RETRY_MS)
      return
    }
    resolving.value = false
    failed.value = true
  },
})

/** Resolves the pin position unless it is (practically) the point already shown. */
function resolve(c: LatLon): void {
  const current = point.value
  if (current && haversineMeters(current, c) < SAME_POINT_M && (result.value !== null || failed.value || resolving.value)) return
  point.value = { lat: roundCoord(c.lat), lon: roundCoord(c.lon) }
  result.value = null
  failed.value = false
  resolving.value = true
  runner.schedule(point.value)
}

const offEnd = stage.onMoveEnd((center) => resolve(center))

let stopMapWatch: (() => void) | null = null
let firstTimer: ReturnType<typeof setTimeout> | undefined

/** Moves the map to the stop's current point (or the passenger) and resolves the pin. */
function init(): boolean {
  if (!stage.map.value) return false
  const existing = booking.stops[index.value]?.place
  const start = existing && hasCoordinates(existing) ? existing : (index.value === 0 ? location.current : null)
  if (start) stage.centerOn(start, 17)
  // When the camera does not move (already there), resolve the centre directly.
  firstTimer = setTimeout(() => {
    const c = stage.center()
    if (point.value === null && c) resolve(c)
  }, start ? 900 : 0)
  return true
}

onMounted(() => {
  if (!Number.isInteger(index.value) || index.value < 0 || index.value >= booking.stops.length) {
    void router.replace({ name: ROUTE.book })
    return
  }
  if (!init()) {
    stopMapWatch = watch(stage.map, (m) => {
      if (m && init()) stopMapWatch?.()
    })
  }
})

onBeforeUnmount(() => {
  clearTimeout(firstTimer)
  clearTimeout(retryTimer)
  stopMapWatch?.()
  runner.cancel()
  offEnd()
})

function choosePoint(): void {
  const p = point.value
  if (!p || !canConfirm.value) return
  const r = result.value
  booking.setStopPlace(index.value, {
    lat: p.lat,
    lon: p.lon,
    address: r?.title ?? '',
    subtitle: r?.title ? r.subtitle : null,
    source: 'map',
    inServiceArea: r ? r.in_service_area : null,
  })
  if (route.query.from === BOOKING_FROM_HOME) void router.replace({ name: ROUTE.book, query: { category: booking.category } })
  else navigateBack(router)
}
</script>

<template>
  <JSheet :snap="'content'" :snap-points="['content']" :aria-label="title" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <h1 class="pick-title">{{ title }}</h1>
    </template>

    <div class="pick-address" aria-live="polite">
      <template v-if="resolving">
        <span class="jt-visually-hidden">{{ t('pick.resolving') }}</span>
        <JSkeleton width="72%" height="20px" />
        <JSkeleton width="44%" height="14px" />
      </template>
      <template v-else-if="failed && point">
        <p class="pick-address__title">{{ address }}</p>
        <p class="pick-address__error" role="alert">{{ t('pick.failed') }}</p>
      </template>
      <template v-else-if="result">
        <p class="pick-address__title">{{ address }}</p>
        <p v-if="subtitle" class="pick-address__subtitle">{{ subtitle }}</p>
      </template>
      <p v-if="outside && !resolving" class="pick-address__error" role="alert">{{ t('pick.outside') }}</p>
      <p v-else-if="!resolving && !failed" class="pick-address__hint">{{ t('pick.hint') }}</p>
    </div>

    <template #footer>
      <JButton block :disabled="!canConfirm" @click="choosePoint">{{ t('pick.confirm') }}</JButton>
    </template>
  </JSheet>
</template>

<style scoped>
.pick-title {
  padding-bottom: var(--jt-space-3);
  font: var(--jt-font-title3);
}

.pick-address {
  display: flex;
  flex-direction: column;
  gap: 6px;
  /* Same height while resolving and resolved: a changing sheet height would
     move the camera padding and with it the pin. */
  min-height: 72px;
  padding-bottom: var(--jt-space-2);
}

.pick-address__title {
  font: var(--jt-font-headline);
  overflow-wrap: anywhere;
}

.pick-address__subtitle,
.pick-address__hint {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.pick-address__error {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-red-text);
}
</style>
