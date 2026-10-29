<!--
  Order route on the map (§14): black route line from the stored snapshot,
  lettered stop markers A–E and, while the order is active, the driver's last
  position as the blue GPS dot.
-->
<script setup lang="ts">
import { LngLatBounds, Marker, type GeoJSONSource, type Map as MapLibreMap } from 'maplibre-gl'
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverLocation, RouteSnapshot, Stop } from '../../api/orders'
import { hasStopCoordinates } from '../../api/orders'
import { STOP_LETTERS } from '../../maps/constants'
import { featureCollection, type FeatureCollection } from '../../maps/geojson'
import MapView from '../../maps/MapView.vue'
import { driverMarkerElement, stopMarkerElement } from '../../maps/markers'

const props = defineProps<{ stops: Stop[]; route: RouteSnapshot; driverLocation: DriverLocation | null }>()

const { t } = useI18n()
const map = shallowRef<MapLibreMap | null>(null)
let markers: Marker[] = []
let fitted = false

function routeData(): FeatureCollection {
  const g = props.route.geometry
  if (g && g.type === 'LineString' && g.coordinates.length > 1) {
    return featureCollection([
      { type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: g.coordinates.map((c): [number, number] => [c[0], c[1]]) } },
    ])
  }
  // A manual endpoint has no route; do not fabricate a final segment.
  if (props.stops.some((s) => !hasStopCoordinates(s))) return featureCollection()
  const coords = [...props.stops].filter(hasStopCoordinates).sort((a, b) => a.seq - b.seq).map((s): [number, number] => [s.lon, s.lat])
  return coords.length > 1
    ? featureCollection([{ type: 'Feature', properties: { fallback: true }, geometry: { type: 'LineString', coordinates: coords } }])
    : featureCollection()
}

function draw(): void {
  const m = map.value
  if (!m) return
  const src = m.getSource('route') as GeoJSONSource | undefined
  if (src) src.setData(routeData())
  else {
    m.addSource('route', { type: 'geojson', data: routeData() })
    m.addLayer({
      id: 'route-line',
      type: 'line',
      source: 'route',
      layout: { 'line-join': 'round', 'line-cap': 'round' },
      paint: { 'line-color': '#000000', 'line-width': 4, 'line-opacity': ['case', ['has', 'fallback'], 0.35, 1] },
    })
  }
  for (const mk of markers) mk.remove()
  markers = []
  const bounds = new LngLatBounds()
  for (const s of [...props.stops].sort((a, b) => a.seq - b.seq)) {
    if (!hasStopCoordinates(s)) continue
    const letter = STOP_LETTERS[s.seq] ?? String(s.seq + 1)
    markers.push(new Marker({ element: stopMarkerElement(letter, `${letter}: ${s.address}`) }).setLngLat([s.lon, s.lat]).addTo(m))
    bounds.extend([s.lon, s.lat])
  }
  const d = props.driverLocation
  if (d) {
    markers.push(new Marker({ element: driverMarkerElement(t('map.driver'), false) }).setLngLat([d.lon, d.lat]).addTo(m))
    bounds.extend([d.lon, d.lat])
  }
  const bbox = props.route.bbox
  if (bbox && bbox.length === 4) bounds.extend([[bbox[0], bbox[1]], [bbox[2], bbox[3]]])
  if (!fitted && !bounds.isEmpty()) {
    m.fitBounds(bounds, { padding: 48, maxZoom: 15, duration: 0 })
    fitted = true
  }
}

function onReady(m: MapLibreMap): void {
  map.value = m
  draw()
}

watch(() => [props.stops, props.route, props.driverLocation], draw)
onBeforeUnmount(() => {
  for (const mk of markers) mk.remove()
  markers = []
})
</script>

<template>
  <MapView height="360px" @ready="onReady" />
</template>
