<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Full-bleed MapLibre map. One instance per screen, created on mount and kept
  alive while the screen's bottom sheet changes (§14). Overlays, layers and
  markers go into the default slot and reach the map through useMap().

  center/zoom are initial values only: after creation the user owns the camera,
  and code moves it through the exposed map (fitToPoints, easeTo).
-->
<script setup lang="ts">
import { AttributionControl, GPUInitializationError, Map as MapLibreMap, type ExpressionSpecification } from 'maplibre-gl'
import { markRaw, onBeforeUnmount, onMounted, provide, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useTelegramFullscreen } from '../telegram'
import { KZ_MAX_BOUNDS, type LatLon } from '../utils/geo'
import { motionMs } from '../utils/motion'
import { DEFAULT_MAP_ZOOM } from './constants'
import { MAP_KEY, type MapPadding } from './context'
import { setupMapLibre } from './setup'

const props = withDefaults(
  defineProps<{
    styleUrl: string
    center: LatLon
    zoom?: number
    /** Camera padding (e.g. bottom sheet height); the visual centre moves with it. */
    padding?: MapPadding
    interactive?: boolean
    minZoom?: number
    maxZoom?: number
  }>(),
  { zoom: DEFAULT_MAP_ZOOM, padding: undefined, interactive: true, minZoom: 4, maxZoom: 19 },
)

const emit = defineEmits<{
  ready: [map: MapLibreMap]
  movestart: [userInitiated: boolean]
  move: [center: LatLon, zoom: number]
  moveend: [center: LatLon, zoom: number, userInitiated: boolean]
  click: [point: LatLon]
  error: [error: unknown]
}>()

const container = ref<HTMLDivElement | null>(null)
const map = shallowRef<MapLibreMap | null>(null)
const unavailable = ref(false)
provide(MAP_KEY, map)

const tg = useTelegramFullscreen()
const { locale } = useI18n()
let resizeObserver: ResizeObserver | null = null
let resizeTimer: ReturnType<typeof setTimeout> | undefined
const RESIZE_DEBOUNCE_MS = 120

function scheduleResize(): void {
  clearTimeout(resizeTimer)
  resizeTimer = setTimeout(() => {
    // resize() keeps the current centre and zoom; only the canvas size changes.
    map.value?.resize()
  }, RESIZE_DEBOUNCE_MS)
}

/**
 * Place and street labels in the app language where OpenStreetMap has it
 * (name:kk / name:ru / name:en), else the local name — instead of the style's
 * default two-line "Latin + local" labels.
 */
function localizeLabels(m: MapLibreMap): void {
  const field: ExpressionSpecification = ['coalesce', ['get', `name:${locale.value}`], ['get', 'name']]
  for (const layer of m.getStyle().layers) {
    if (layer.type !== 'symbol') continue
    const text = m.getLayoutProperty(layer.id, 'text-field') as unknown
    if (text === undefined || !JSON.stringify(text).includes('name')) continue
    m.setLayoutProperty(layer.id, 'text-field', field)
  }
}

function centerOf(m: MapLibreMap): LatLon {
  const c = m.getCenter()
  return { lat: c.lat, lon: c.lng }
}

function create(el: HTMLDivElement): void {
  setupMapLibre()
  let m: MapLibreMap
  try {
    m = new MapLibreMap({
      container: el,
      style: props.styleUrl,
      center: [props.center.lon, props.center.lat],
      zoom: props.zoom,
      minZoom: props.minZoom,
      maxZoom: props.maxZoom,
      maxBounds: KZ_MAX_BOUNDS,
      interactive: props.interactive,
      // Added below at bottom-left so it never sits under the floating buttons.
      attributionControl: false,
      maplibreLogo: false,
      dragRotate: false,
      pitchWithRotate: false,
      touchPitch: false,
      maxPitch: 0,
      rollEnabled: false,
      renderWorldCopies: false,
    })
  } catch (err) {
    // No WebGL2 (old iOS WebViews) → GPUInitializationError. Any other
    // construction failure gets the same fallback rather than a blank screen.
    if (!(err instanceof GPUInitializationError)) console.error('[map] failed to create map', err)
    unavailable.value = true
    emit('error', err)
    return
  }
  m.addControl(new AttributionControl({ compact: true }), 'bottom-left')
  m.touchZoomRotate.disableRotation()
  m.keyboard.disableRotation()
  if (props.padding) m.setPadding(props.padding)

  m.on('style.load', () => localizeLabels(m))
  m.on('load', () => {
    // Start with the attribution collapsed to its (i) button, as on iOS maps.
    el.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show')
    emit('ready', m)
  })
  m.on('movestart', (e) => emit('movestart', e.originalEvent !== undefined))
  m.on('move', () => emit('move', centerOf(m), m.getZoom()))
  m.on('moveend', (e) => emit('moveend', centerOf(m), m.getZoom(), e.originalEvent !== undefined))
  m.on('click', (e) => emit('click', { lat: e.lngLat.lat, lon: e.lngLat.lng }))
  m.on('error', (e) => emit('error', e.error))
  map.value = markRaw(m)
  // Presentation build only (constant-folded away otherwise): lets the
  // screenshot script wait until tiles are loaded and the camera is at rest.
  if (import.meta.env.VITE_DEMO === '1') (window as unknown as { __jtMap?: unknown }).__jtMap = m
}

onMounted(() => {
  if (!container.value) return
  create(container.value)
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver(scheduleResize)
    resizeObserver.observe(container.value)
  }
})

// Telegram viewport changes (expand, fullscreen, keyboard) are not always
// window resizes, so react to them explicitly.
watch(
  () => [tg.state.viewportHeight, tg.state.stableHeight, tg.state.isFullscreen, tg.state.safeArea, tg.state.contentSafeArea],
  scheduleResize,
)

watch(
  () => props.padding,
  (p) => {
    if (p && map.value) map.value.easeTo({ padding: p, duration: motionMs(250) })
  },
  { deep: true },
)

watch(locale, () => {
  if (map.value?.isStyleLoaded()) localizeLabels(map.value)
})

watch(
  () => props.styleUrl,
  (url) => map.value?.setStyle(url),
)

onBeforeUnmount(() => {
  if (import.meta.env.VITE_DEMO === '1') {
    const w = window as unknown as { __jtMap?: unknown }
    if (w.__jtMap === map.value) w.__jtMap = undefined
  }
  clearTimeout(resizeTimer)
  resizeObserver?.disconnect()
  resizeObserver = null
  map.value?.remove()
  map.value = null
})

defineExpose({
  /** The MapLibre instance, or null before creation / when unavailable. */
  getMap: (): MapLibreMap | null => map.value,
  isUnavailable: (): boolean => unavailable.value,
})
</script>

<template>
  <div class="jt-map">
    <div v-show="!unavailable" ref="container" class="jt-map__canvas" />
    <div v-if="unavailable" class="jt-map__fallback" role="status">
      <slot name="fallback" />
    </div>
    <slot v-if="!unavailable" />
  </div>
</template>

<style scoped>
.jt-map {
  position: absolute;
  inset: 0;
  overflow: hidden;
  background: var(--jt-color-grouped-bg);
}

.jt-map__canvas {
  position: absolute;
  inset: 0;
}

.jt-map__fallback {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: calc(var(--jt-safe-top) + var(--jt-space-4)) var(--jt-side-margin) var(--jt-space-4);
}

/* Required attribution (OpenFreeMap/OSM), kept above the bottom sheet: pages
   set --jt-map-attribution-offset to the sheet height. */
.jt-map :deep(.maplibregl-ctrl-bottom-left) {
  bottom: var(--jt-map-attribution-offset, 0px);
  left: var(--jt-safe-left, 0px);
}
</style>
