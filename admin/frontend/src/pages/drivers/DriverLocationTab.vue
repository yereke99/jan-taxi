<!-- Last known driver position with its accuracy radius and age. -->
<script setup lang="ts">
import { Marker, type GeoJSONSource, type Map as MapLibreMap } from 'maplibre-gl'
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverLastLocation } from '../../api/drivers'
import { Card, KeyValueList } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { featureCollection, type FeatureCollection } from '../../maps/geojson'
import MapView from '../../maps/MapView.vue'
import { driverMarkerElement } from '../../maps/markers'
import { circleRing } from '../../maps/polygon'

const props = defineProps<{ location: DriverLastLocation | null; busy: boolean }>()

const { t, te } = useI18n()
const fmt = useFormat()
const map = shallowRef<MapLibreMap | null>(null)
let marker: Marker | null = null

function ageText(s: number): string {
  if (s < 60) return t('drivers.age_seconds', { n: s })
  if (s < 3600) return t('drivers.age_minutes', { n: Math.round(s / 60) })
  if (s < 86400) return t('drivers.age_hours', { n: Math.round(s / 3600) })
  return t('drivers.age_days', { n: Math.round(s / 86400) })
}

const items = computed(() => {
  const l = props.location
  if (!l) return []
  return [
    { key: 'coords', label: t('drivers.coordinates'), value: `${l.lat.toFixed(6)}, ${l.lon.toFixed(6)}`, mono: true },
    { key: 'accuracy', label: t('drivers.accuracy'), value: l.accuracy_m === null ? null : t('drivers.accuracy_value', { n: Math.round(l.accuracy_m) }) },
    { key: 'age', label: t('drivers.age'), value: ageText(l.age_s) },
    { key: 'recorded', label: t('drivers.recorded_at'), value: fmt.dateTime(l.recorded_at) },
    { key: 'received', label: t('drivers.received_at'), value: fmt.dateTime(l.received_at) },
    { key: 'source', label: t('drivers.location_source'), value: te(`drivers.source_${l.source}`, 'kk') ? t(`drivers.source_${l.source}`) : l.source },
    { key: 'speed', label: t('drivers.speed'), value: l.speed_mps === null ? null : t('drivers.speed_value', { n: Math.round(l.speed_mps * 3.6) }) },
    { key: 'heading', label: t('drivers.heading'), value: l.heading === null ? null : `${Math.round(l.heading)}°` },
  ]
})

function accuracyData(): FeatureCollection {
  const l = props.location
  if (!l || !l.accuracy_m) return featureCollection()
  return featureCollection([{ type: 'Feature', properties: {}, geometry: { type: 'Polygon', coordinates: [circleRing(l, l.accuracy_m)] } }])
}

function draw(): void {
  const m = map.value
  const l = props.location
  if (!m || !l) return
  const src = m.getSource('accuracy') as GeoJSONSource | undefined
  if (src) src.setData(accuracyData())
  else {
    m.addSource('accuracy', { type: 'geojson', data: accuracyData() })
    m.addLayer({ id: 'accuracy-fill', type: 'fill', source: 'accuracy', paint: { 'fill-color': '#007AFF', 'fill-opacity': 0.12 } })
    m.addLayer({ id: 'accuracy-line', type: 'line', source: 'accuracy', paint: { 'line-color': '#007AFF', 'line-width': 1, 'line-opacity': 0.5 } })
  }
  marker?.remove()
  marker = new Marker({ element: driverMarkerElement(t('map.driver'), props.busy) }).setLngLat([l.lon, l.lat]).addTo(m)
  m.jumpTo({ center: [l.lon, l.lat], zoom: Math.max(m.getZoom(), 14) })
}

function onReady(m: MapLibreMap): void {
  map.value = m
  draw()
}

watch(() => props.location, draw)
onBeforeUnmount(() => marker?.remove())
</script>

<template>
  <div class="loc">
    <Card :title="t('drivers.last_location')">
      <KeyValueList v-if="location" :items="items" />
      <p v-else class="jt-muted">{{ t('drivers.no_location') }}</p>
    </Card>
    <Card flush>
      <MapView v-if="location" height="420px" :center="{ lat: location.lat, lon: location.lon }" :zoom="15" @ready="onReady" />
      <p v-else class="jt-muted loc__empty">{{ t('drivers.no_location') }}</p>
    </Card>
  </div>
</template>

<style scoped>
.loc {
  display: grid;
  grid-template-columns: 340px minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.loc__empty {
  padding: var(--jt-space-6);
  text-align: center;
}
</style>
