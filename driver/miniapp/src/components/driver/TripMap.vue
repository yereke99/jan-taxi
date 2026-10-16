<!--
  Full-screen white map of one order (§14/§15): the passenger route in black,
  lettered stop markers A–E (completed stops greyed), the driver's real GPS
  position as the car marker and the approach leg from the driver to the
  current target as a dashed gray line. One map instance for the screen.
-->
<script setup lang="ts">
import { LocateFixed, Route } from '@lucide/vue'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderDetail } from '../../api/types'
import type { Fix } from '../../features/location/locationSender'
import { currentTarget, sortedStops } from '../../features/trip/sequence'
import { fitToPoints, MapView, straightLine, useCarMarker, useRouteLayer, useStopMarkers, type LineStringGeometry } from '../../maps'
import { useSessionStore } from '../../stores/session'
import { effectiveSafeInsets, useTelegramFullscreen } from '../../telegram'
import type { LatLon } from '../../utils/geo'
import { motionMs } from '../../utils/motion'
import { JEmptyState, JMapButton } from '../ui'

const props = defineProps<{
  order: DriverOrderDetail
  /** The driver's current real position (null without GPS). */
  position: Fix | null
  /** Height of the bottom sheet covering the map, px. */
  sheetHeight: number
  /** Greys completed stops and draws the approach to the current target. */
  live?: boolean
}>()

const emit = defineEmits<{ locate: [] }>()

const { t } = useI18n()
const session = useSessionStore()
const tg = useTelegramFullscreen()

const map = shallowRef<MapLibreMap | null>(null)
const route = useRouteLayer(map)
const markers = useStopMarkers(map, {
  label: (_i, letter) => t('common.stop_letter', { letter }),
})
const car = useCarMarker(map, t('order.your_location'))

const safe = computed(() => effectiveSafeInsets(tg.state))
const stops = computed(() => sortedStops(props.order.stops))
const target = computed(() => (props.live ? currentTarget(props.order) : (stops.value[0] ?? null)))
const padding = computed(() => ({ top: safe.value.top, right: 0, bottom: props.sheetHeight, left: 0 }))
const pageStyle = computed(() => ({ '--jt-map-attribution-offset': `${props.sheetHeight}px` }))

function geometry(): LineStringGeometry | null {
  const g = props.order.route?.geometry
  if (g && g.type === 'LineString' && Array.isArray(g.coordinates) && g.coordinates.length >= 2) return g as LineStringGeometry
  // Without a stored route, connect the stops in order (still never invented points).
  const pts = stops.value
  return pts.length >= 2 ? { type: 'LineString', coordinates: pts.map((s) => [s.lon, s.lat] as [number, number]) } : null
}

function draw(): void {
  if (!map.value) return
  route.setRoute(geometry())
  markers.setStops(stops.value.map((s) => ({ lat: s.lat, lon: s.lon, done: props.live ? s.completed_at !== null : false })))
  const p = props.position
  car.set(p ? { lat: p.lat, lon: p.lon, heading: p.heading } : null)
  route.setApproach(p && target.value ? straightLine(p, target.value) : null)
}

/**
 * Frames the whole order (and the driver when known) above the sheet, below
 * the top safe area; fitToPoints subtracts what the map's camera padding
 * (sheet and safe area, see `padding`) already covers.
 */
function fit(animate = true): void {
  const m = map.value
  if (!m) return
  const pts: LatLon[] = stops.value.map((s) => ({ lat: s.lat, lon: s.lon }))
  if (props.position) pts.push({ lat: props.position.lat, lon: props.position.lon })
  fitToPoints(m, pts, { sheetHeight: props.sheetHeight, safeArea: safe.value, animate, padding: { top: 56, bottom: 32, left: 40, right: 40 } })
}

function followDriver(): void {
  const m = map.value
  const p = props.position
  if (!m) return
  if (!p) {
    emit('locate')
    return
  }
  m.easeTo({ center: [p.lon, p.lat], zoom: Math.max(m.getZoom(), 15), duration: motionMs(400) })
}

function onReady(m: MapLibreMap): void {
  map.value = m
  draw()
  fit(false)
}

watch(() => [props.order, props.position] as const, draw)
watch(
  () => props.order.id,
  () => fit(),
)
// The sheet reports its height after the first layout: frame the order above
// it once MapView has applied the new camera padding (a 250 ms ease).
let refit: ReturnType<typeof setTimeout> | undefined
watch(
  () => props.sheetHeight,
  (h, old) => {
    if (old !== 0 || h <= 0) return
    clearTimeout(refit)
    refit = setTimeout(() => fit(false), 300)
  },
)
onBeforeUnmount(() => clearTimeout(refit))
// The first real fix after opening: include the driver in the frame once.
watch(
  () => props.position !== null,
  (has, had) => {
    if (has && !had) fit()
  },
)

defineExpose({ fit })
</script>

<template>
  <div class="tmap" :style="pageStyle">
    <MapView
      :style-url="session.config.map.style_url"
      :center="stops[0] ?? session.config.map.default_center"
      :zoom="session.config.map.default_zoom"
      :padding="padding"
      @ready="onReady"
    >
      <template #fallback>
        <JEmptyState :title="t('map.unavailable_title')" :description="t('trip.map_unavailable')" />
      </template>
    </MapView>

    <div class="tmap__controls" :style="{ bottom: `${sheetHeight + 44}px` }">
      <JMapButton :label="t('order.fit_route')" @click="fit()">
        <Route :size="22" :stroke-width="2" aria-hidden="true" />
      </JMapButton>
      <JMapButton :label="t('map.locate_me')" :active="position !== null" @click="followDriver">
        <LocateFixed :size="22" :stroke-width="2" aria-hidden="true" />
      </JMapButton>
    </div>
  </div>
</template>

<style scoped>
.tmap {
  position: absolute;
  inset: 0;
}

.tmap__controls {
  position: absolute;
  right: calc(var(--jt-safe-right) + var(--jt-side-margin));
  z-index: var(--jt-z-floating);
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  transition: bottom var(--jt-duration-slow) var(--jt-ease);
}
</style>
