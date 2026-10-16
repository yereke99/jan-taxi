<!--
  MapLibre map that fills its parent. Layers and markers are added by the
  parent through the `ready` event (or the exposed getMap). Without WebGL2
  (GPUInitializationError) a text fallback is shown instead of a blank box.
-->
<script setup lang="ts">
import { AttributionControl, GPUInitializationError, Map as MapLibreMap, NavigationControl } from 'maplibre-gl'
import { markRaw, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LatLon } from '../api/common'
import { usePublicConfig } from '../composables/usePublicConfig'
import { DEFAULT_MAP_ZOOM, KZ_MAX_BOUNDS } from './constants'
import './markers.css'
import { setupMapLibre } from './setup'

const props = withDefaults(defineProps<{ center?: LatLon | null; zoom?: number; height?: string; cursor?: string }>(), {
  center: null,
  zoom: DEFAULT_MAP_ZOOM,
  height: '360px',
  cursor: '',
})

const emit = defineEmits<{ ready: [map: MapLibreMap]; click: [point: LatLon] }>()

const { t } = useI18n()
const cfg = usePublicConfig()
const container = ref<HTMLDivElement | null>(null)
const map = shallowRef<MapLibreMap | null>(null)
const unavailable = ref(false)

onMounted(() => {
  if (!container.value) return
  setupMapLibre()
  const c = props.center ?? cfg.center.value
  let m: MapLibreMap
  try {
    m = new MapLibreMap({
      container: container.value,
      style: cfg.styleUrl.value,
      center: [c.lon, c.lat],
      zoom: props.center ? props.zoom : cfg.zoom.value,
      maxBounds: KZ_MAX_BOUNDS,
      attributionControl: false,
      maplibreLogo: false,
      dragRotate: false,
      pitchWithRotate: false,
      maxPitch: 0,
      renderWorldCopies: false,
    })
  } catch (err) {
    if (!(err instanceof GPUInitializationError)) console.error('[map] failed to create map', err)
    unavailable.value = true
    return
  }
  m.addControl(new AttributionControl({ compact: true }), 'bottom-right')
  m.addControl(new NavigationControl({ showCompass: false }), 'top-right')
  m.touchZoomRotate.disableRotation()
  m.keyboard.disableRotation()
  if (props.cursor) m.getCanvas().style.cursor = props.cursor
  m.on('load', () => emit('ready', m))
  m.on('click', (e) => emit('click', { lat: e.lngLat.lat, lon: e.lngLat.lng }))
  map.value = markRaw(m)
})

watch(
  () => props.cursor,
  (c) => {
    const canvas = map.value?.getCanvas()
    if (canvas) canvas.style.cursor = c
  },
)

onBeforeUnmount(() => {
  map.value?.remove()
  map.value = null
})

defineExpose({ getMap: (): MapLibreMap | null => map.value })
</script>

<template>
  <div class="map" :style="{ height }">
    <div v-show="!unavailable" ref="container" class="map__canvas" />
    <p v-if="unavailable" class="map__fallback" role="status">{{ t('map.unavailable') }}</p>
    <slot />
  </div>
</template>

<style scoped>
.map {
  position: relative;
  width: 100%;
  overflow: hidden;
  background: var(--jt-color-fill-secondary);
}

.map__canvas {
  position: absolute;
  inset: 0;
}

.map__fallback {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--jt-space-4);
  color: var(--jt-color-label-secondary);
  text-align: center;
}
</style>
