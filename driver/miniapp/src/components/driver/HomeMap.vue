<!--
  Home mini-map: the driver's black car on the real device position, followed
  by the camera (composables/useFollowCar). Dragging or zooming the map pauses
  following; the locate button resumes it. Fixes come from the app's single
  location service (stores/location, about one per second while this map is
  on screen); without GPS the last known position is shown, dimmed, and never
  an invented one.
-->
<script setup lang="ts">
import { Locate, LocateFixed } from '@lucide/vue'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useFollowCar } from '../../composables/useFollowCar'
import { MapView } from '../../maps'
import { useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { useTelegramFullscreen } from '../../telegram'
import { JMapButton } from '../ui'

/** Street-level framing for a small map. */
const FOLLOW_ZOOM = 16

const { t } = useI18n()
const session = useSessionStore()
const location = useLocationStore()
const tg = useTelegramFullscreen()

const root = ref<HTMLElement | null>(null)
const map = shallowRef<MapLibreMap | null>(null)
const onScreen = ref(true)
const pageVisible = ref(typeof document === 'undefined' || document.visibilityState !== 'hidden')
/** Frames, camera work and the live GPS stream only while someone can see the map. */
const active = computed(() => onScreen.value && pageVisible.value && tg.state.isActive)

const follow = useFollowCar(map, { label: t('map.user_location'), zoom: FOLLOW_ZOOM, active })

/** Last known position (any age): shown dimmed when it is not fresh. */
const known = computed(() => location.lastFix)
const stale = computed(() => known.value !== null && location.current === null)
const locating = ref(false)

// Initial camera only (MapView owns it afterwards).
const initialCenter = known.value ?? session.config.map.default_center
const initialZoom = known.value ? FOLLOW_ZOOM : session.config.map.default_zoom

watch(known, (f) => follow.setFix(f), { immediate: true })
watch(stale, (s) => follow.setStale(s), { immediate: true })

function onReady(m: MapLibreMap): void {
  map.value = m
}

function onMoveStart(userInitiated: boolean): void {
  if (userInitiated) follow.pause()
}

async function locate(): Promise<void> {
  const hasPosition = follow.resume()
  if (hasPosition && location.current) return
  locating.value = true
  try {
    await location.locateOnce()
  } catch {
    // The overlay keeps saying the position is unknown; the online card explains a denial.
  } finally {
    locating.value = false
  }
}

// The live stream runs only while the map is visible in an active Mini App.
let release: (() => void) | null = null
watch(
  active,
  (on) => {
    if (on) release ??= location.acquireLive()
    else {
      release?.()
      release = null
    }
  },
  { immediate: true },
)

let observer: IntersectionObserver | null = null
function onVisibility(): void {
  pageVisible.value = document.visibilityState !== 'hidden'
}

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibility)
  if (root.value && typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver(([entry]) => {
      if (entry) onScreen.value = entry.isIntersecting
    })
    observer.observe(root.value)
  }
  // No position yet (or only an old one): ask once; the live stream takes over.
  if (!location.current) void location.locateOnce().catch(() => undefined)
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', onVisibility)
  observer?.disconnect()
  observer = null
  release?.()
  release = null
})
</script>

<template>
  <section ref="root" class="home-map" :aria-label="t('home.map_title')">
    <MapView :style-url="session.config.map.style_url" :center="initialCenter" :zoom="initialZoom" @ready="onReady" @movestart="onMoveStart">
      <template #fallback>
        <p class="home-map__note">{{ t('map.unavailable_title') }}</p>
      </template>
    </MapView>
    <p v-if="!known" class="home-map__overlay jt-glass" role="status">{{ t('home.no_position') }}</p>
    <p v-else-if="stale" class="home-map__overlay jt-glass" role="status">{{ t('home.last_known_position') }}</p>
    <div class="home-map__btn">
      <JMapButton :label="t('map.locate_me')" :active="follow.following.value && known !== null" :loading="locating" @click="locate">
        <LocateFixed v-if="follow.following.value" :size="22" :stroke-width="2" aria-hidden="true" />
        <Locate v-else :size="22" :stroke-width="2" aria-hidden="true" />
      </JMapButton>
    </div>
  </section>
</template>

<style scoped>
.home-map {
  position: relative;
  height: 180px;
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  border-radius: var(--jt-radius-card);
  overflow: hidden;
  background: var(--jt-color-fill-secondary);
  /* MapView fills its parent; keeps the map's layers below the page chrome. */
  isolation: isolate;
}

.home-map :deep(.jt-map) {
  position: absolute;
  inset: 0;
}

.home-map__overlay {
  position: absolute;
  left: var(--jt-space-3);
  top: var(--jt-space-3);
  z-index: 2;
  max-width: calc(100% - 2 * var(--jt-space-3));
  padding: 6px 12px;
  border-radius: var(--jt-radius-capsule);
  font: var(--jt-font-footnote);
  pointer-events: none;
}

.home-map__note {
  padding: var(--jt-space-4);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.home-map__btn {
  position: absolute;
  right: var(--jt-space-3);
  bottom: var(--jt-space-3);
  z-index: 2;
}
</style>
