<!--
  Online drivers on the map: GET /admin/drivers/locations every 15 s, plus
  driver.location_updated events moving markers immediately. Blue dot =
  free, black = on an order (§15 accents for meaning only).
-->
<script setup lang="ts">
import { LngLatBounds, Marker, Popup, type Map as MapLibreMap } from 'maplibre-gl'
import { computed, onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { driversApi, type DriverLocationItem } from '../../api/drivers'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import MapView from '../../maps/MapView.vue'
import { driverMarkerElement } from '../../maps/markers'

const props = defineProps<{ cityId: string }>()

const REFRESH_MS = 15_000
const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()
const map = shallowRef<MapLibreMap | null>(null)
const markers = new Map<string, { marker: Marker; busy: boolean }>()
let timer: ReturnType<typeof setInterval> | undefined
let fittedFor: string | null = null

const res = useResource((signal) => driversApi.locations(props.cityId || undefined, signal))
const items = computed<DriverLocationItem[]>(() => (res.data.value?.items ?? []).filter((i) => i.is_online))
const busyCount = computed(() => items.value.filter((i) => i.active_order).length)

function popupContent(item: DriverLocationItem): HTMLElement {
  const root = document.createElement('div')
  const title = document.createElement('strong')
  title.textContent = `${item.driver.code} · ${item.driver.first_name} ${item.driver.last_name}`
  const line = document.createElement('div')
  line.textContent = item.active_order ? `${t('orders.order')} #${item.active_order.number} · ${t(`order_status.${item.active_order.status}`)}` : t('dashboard.free')
  const age = document.createElement('div')
  age.textContent = fmt.relative(item.location.recorded_at)
  age.style.opacity = '0.6'
  const link = document.createElement('a')
  link.href = router.resolve({ name: 'driver-detail', params: { id: item.driver.id } }).href
  link.textContent = t('common.open')
  link.addEventListener('click', (e) => {
    e.preventDefault()
    void router.push({ name: 'driver-detail', params: { id: item.driver.id } })
  })
  root.append(title, line, age, link)
  return root
}

function sync(): void {
  const m = map.value
  if (!m) return
  const seen = new Set<string>()
  for (const item of items.value) {
    const id = item.driver.id
    seen.add(id)
    const busy = item.active_order !== null
    const existing = markers.get(id)
    if (existing && existing.busy === busy) {
      // Keep the marker (and an open popup); refresh its position and text.
      existing.marker.setLngLat([item.location.lon, item.location.lat])
      existing.marker.getPopup()?.setDOMContent(popupContent(item))
      continue
    }
    existing?.marker.remove()
    const label = `${item.driver.code} · ${item.driver.first_name} ${item.driver.last_name}`
    const marker = new Marker({ element: driverMarkerElement(label, busy) })
      .setLngLat([item.location.lon, item.location.lat])
      .setPopup(new Popup({ offset: 12, closeButton: false }).setDOMContent(popupContent(item)))
      .addTo(m)
    markers.set(id, { marker, busy })
  }
  for (const [id, entry] of markers) {
    if (!seen.has(id)) {
      entry.marker.remove()
      markers.delete(id)
    }
  }
  if (fittedFor !== props.cityId && items.value.length > 0) {
    const b = new LngLatBounds()
    for (const i of items.value) b.extend([i.location.lon, i.location.lat])
    m.fitBounds(b, { padding: 60, maxZoom: 14, duration: 0 })
    fittedFor = props.cityId
  }
}

function onReady(m: MapLibreMap): void {
  map.value = m
  sync()
}

watch(items, sync)
watch(
  () => props.cityId,
  () => {
    fittedFor = null
    void res.reload()
  },
)

onMounted(() => {
  void res.reload()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') void res.reload({ silent: true })
  }, REFRESH_MS)
})

onBeforeUnmount(() => {
  clearInterval(timer)
  for (const entry of markers.values()) entry.marker.remove()
  markers.clear()
})

useRealtimeEvents(
  ['driver.location_updated'],
  (ev) => {
    const id = typeof ev.data.driver_id === 'string' ? ev.data.driver_id : ''
    const lat = Number(ev.data.lat)
    const lon = Number(ev.data.lon)
    const entry = markers.get(id)
    if (entry && Number.isFinite(lat) && Number.isFinite(lon)) entry.marker.setLngLat([lon, lat])
  },
  0,
)
useRealtimeEvents(['driver.status_changed', 'order.accepted', 'order.completed', 'order.cancelled'], () => void res.reload({ silent: true }), 2000)
useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <div class="live-map">
    <MapView height="420px" @ready="onReady" />
    <div class="live-map__legend jt-glass">
      <span><span class="dot dot--free" aria-hidden="true" />{{ t('dashboard.free') }} · {{ items.length - busyCount }}</span>
      <span><span class="dot dot--busy" aria-hidden="true" />{{ t('dashboard.on_order') }} · {{ busyCount }}</span>
      <span v-if="res.error.value" class="jt-negative">{{ t('pages.load_failed') }}</span>
    </div>
  </div>
</template>

<style scoped>
.live-map {
  position: relative;
}

.live-map__legend {
  position: absolute;
  left: 12px;
  top: 12px;
  display: flex;
  gap: var(--jt-space-3);
  padding: 6px 12px;
  border-radius: var(--jt-radius-round);
  font: var(--jt-font-small);
}

.dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  margin-right: 6px;
  border-radius: 50%;
  border: 2px solid #fff;
  box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.15);
  vertical-align: -1px;
}

.dot--free {
  background: var(--jt-color-blue);
}

.dot--busy {
  background: #000;
}
</style>
