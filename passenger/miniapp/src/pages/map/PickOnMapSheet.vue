<!--
  "Choose on map": the map moves under a fixed centre pin. When the camera
  settles the point is reverse geocoded (a newer movement aborts the older
  request); a point outside Kazakhstan is reported at once and cannot be
  chosen. The pin's exact position becomes the stop; the address names it.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { reverseGeocode } from '../../api/geo'
import type { GeoReverse } from '../../api/types'
import { JButton, JSheet, JSkeleton } from '../../components/ui'
import { createLatest } from '../../domain/latest'
import { ROUTE } from '../../router/names'
import { useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { haptics, navigateBack } from '../../telegram'
import { isInKazakhstanBBox, roundCoord, type LatLon } from '../../utils/geo'

const { t } = useI18n()
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
const canConfirm = computed(() => !resolving.value && !failed.value && result.value !== null && !outside.value)

const runner = createLatest<LatLon, GeoReverse>({
  delayMs: 150,
  run: (p, signal) => reverseGeocode(p, { lang: session.language, signal }),
  onResult: (r) => {
    result.value = r
    resolving.value = false
    failed.value = false
    if (!r.in_service_area) haptics.notification('warning')
  },
  onError: () => {
    resolving.value = false
    failed.value = true
  },
})

function resolve(p: LatLon): void {
  point.value = { lat: roundCoord(p.lat), lon: roundCoord(p.lon) }
  failed.value = false
  resolving.value = true
  result.value = null
  runner.schedule(point.value)
}

const offStart = stage.onMoveStart(() => {
  runner.cancel()
  resolving.value = true
  result.value = null
})
const offEnd = stage.onMoveEnd((center) => resolve(center))

let fallbackTimer: ReturnType<typeof setTimeout> | undefined
let stopMapWatch: (() => void) | null = null

/** Moves the map to the stop's current point (or the passenger) and resolves the pin. */
function init(): boolean {
  if (!stage.map.value) return false
  const existing = booking.stops[index.value]?.place
  const start = existing ?? (index.value === 0 ? location.current : null)
  if (start) stage.centerOn(start, 17)
  // When the camera does not move (already there), resolve the centre directly.
  fallbackTimer = setTimeout(() => {
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
  clearTimeout(fallbackTimer)
  stopMapWatch?.()
  runner.cancel()
  offStart()
  offEnd()
})

function choosePoint(): void {
  const p = point.value
  const r = result.value
  if (!p || !r || !canConfirm.value) return
  booking.setStopPlace(index.value, {
    lat: p.lat,
    lon: p.lon,
    address: r.title,
    subtitle: r.subtitle,
    source: 'map',
    inServiceArea: r.in_service_area,
  })
  navigateBack(router)
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
      <p v-else-if="failed" class="pick-address__error" role="alert">{{ t('pick.failed') }}</p>
      <template v-else-if="result">
        <p class="pick-address__title">{{ result.title }}</p>
        <p v-if="result.subtitle" class="pick-address__subtitle">{{ result.subtitle }}</p>
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
  min-height: 64px;
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
