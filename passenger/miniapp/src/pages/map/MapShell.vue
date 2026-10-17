<!--
  Map-first layout: one full-screen MapLibre map shared by home, the route
  editor, the place picker, the confirmation and the live order (§14). Child
  routes render only their bottom sheet; they drive the map through the stage
  (stage/mapStage.ts). Floating Liquid Glass controls (§15.1) sit above the
  map: the menu (home) or back button at the top, locate / recenter above
  the sheet. The passenger's position is watched after the first fix: the
  blue dot follows the device, the camera follows it only after the locate
  button until the map is panned, and the first fix fills pickup A.
-->
<script setup lang="ts">
import { ChevronLeft, Crosshair, Menu, Navigation, TriangleAlert } from '@lucide/vue'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { computed, provide, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { JEmptyState, JMapButton, toast } from '../../components/ui'
import { useErrorText } from '../../i18n/core'
import { CenterPin, MapView } from '../../maps'
import { ROUTE } from '../../router/names'
import { createMapStage, STAGE_KEY } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { canOpenLocationSettings, isTelegram, LocationError, navigateBack, openLocationSettings, useTelegramFullscreen, type DeviceLocation } from '../../telegram'
import { haversineMeters } from '../../utils/geo'
import { motionMs } from '../../utils/motion'

/** Gap between the sheet top and the floating buttons; clears the attribution row. */
const CONTROLS_GAP_PX = 40
/** Follow mode moves the camera only for position changes above this. */
const FOLLOW_MIN_MOVE_M = 3
/** Floating button size and the gap between stacked buttons. */
const BUTTON_PX = 48
const BUTTON_GAP_PX = 12

const { t } = useI18n()
const errorText = useErrorText()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const location = useLocationStore()
const booking = useBookingStore()

const { stage, attach } = createMapStage(
  {
    stop: (_index, letter) => t('booking.marker', { letter }),
    car: t('map.driver_car'),
    user: t('map.user_location'),
  },
  // A screen framing its content (route, trip) ends "follow my location".
  () => (location.following = false),
)
// Inside Telegram the client's own back control is shown (BackButton);
// the round in-map back button is for a plain browser only.
const inTelegram = isTelegram()
provide(STAGE_KEY, stage)
const tg = useTelegramFullscreen()

// The attribution sits above the sheet, but never under Telegram's header
// (a nearly full sheet then simply covers it).
// The map opens where the passenger is (or at the chosen pickup); the city
// centre from the config is only the fallback before the first GPS fix.
const known = location.current ?? booking.stops[0]?.place ?? null
const initialView = {
  center: known ? { lat: known.lat, lon: known.lon } : session.config.map.default_center,
  zoom: known ? 16 : session.config.map.default_zoom,
}

const pageStyle = computed(() => {
  const viewport = tg.state.viewportHeight || window.innerHeight
  const offset = Math.min(stage.sheetHeight.value, Math.max(0, viewport - stage.safe.value.top - 40))
  return { '--jt-map-attribution-offset': `${offset}px` }
})
const topStyle = computed(() => ({
  top: `${stage.safe.value.top + 8}px`,
  left: `${stage.safe.value.left + 16}px`,
}))
const bottomStyle = computed(() => ({
  bottom: `${stage.sheetHeight.value + CONTROLS_GAP_PX}px`,
  right: `${stage.safe.value.right + 16}px`,
}))
const chipStyle = computed(() => ({
  bottom: `${stage.sheetHeight.value + CONTROLS_GAP_PX + 6}px`,
  left: `${stage.safe.value.left + 16}px`,
}))
const showAccuracyChip = computed(() => stage.controls.accuracyChip && location.inaccurate)

// Floating buttons above a tall sheet would slide under Telegram's header:
// they are hidden while the sheet leaves no room for them.
const sideCount = computed(() => (stage.controls.recenter ? 1 : 0) + (stage.controls.locate ? 1 : 0))
const sideFits = computed(() => {
  const n = sideCount.value
  if (n === 0) return false
  const viewport = tg.state.viewportHeight || window.innerHeight
  const needed = n * BUTTON_PX + (n - 1) * BUTTON_GAP_PX + 8
  return viewport - stage.safe.value.top - stage.sheetHeight.value - CONTROLS_GAP_PX >= needed
})

watch(
  () => location.current,
  (loc) => {
    stage.user.set(loc)
    // Follow mode: keep the camera on the dot, at the passenger's zoom.
    if (!loc || !location.following) return
    // Standing still keeps the camera (and the pin above it) at rest.
    const c = stage.center()
    if (c && haversineMeters(c, loc) < FOLLOW_MIN_MOVE_M) return
    stage.centerOn(loc, stage.map.value?.getZoom(), { duration: motionMs(800), linear: true })
  },
)

// Any pan or zoom by the passenger ends follow mode.
stage.onMoveStart((user) => {
  if (user) location.following = false
})

/** Fills pickup A from the device position unless the passenger already chose it. */
async function fillPickup(): Promise<void> {
  const loc = await firstFix
  if (!loc || booking.stops[0]?.place) return
  try {
    await booking.setPickupFromLocation(loc)
  } catch {
    // The passenger chooses the pickup manually.
  }
}

// Ask for the position right away (not after the map loads), so the first
// frame of the map is usually already where the passenger is. The pickup is
// filled from this same attempt: a denied permission is never asked twice.
const firstFix: Promise<DeviceLocation | null> = location.current ? Promise.resolve(location.current) : location.locate()

function onReady(m: MapLibreMap): void {
  stage.map.value = m
  attach(m)
  if (location.current) stage.user.set(location.current)
  // First, silent attempt; problems surface only when the user taps locate.
  void fillPickup()
}

function locationFailureToast(err: unknown): void {
  if (err === 'outside_kz') {
    toast.error(t('gps.outside_kz'))
    return
  }
  if (err instanceof LocationError && err.code === 'denied' && canOpenLocationSettings()) {
    toast.show({
      message: errorText(err),
      tone: 'error',
      duration: 6000,
      action: { label: t('common.open_settings'), run: () => void openLocationSettings() },
    })
    return
  }
  toast.error(errorText(err))
}

async function locate(): Promise<void> {
  const loc = await location.locate()
  if (!loc) {
    locationFailureToast(location.error)
    return
  }
  location.following = true
  stage.centerOn(loc, 16)
}

function openProfile(): void {
  void router.push({ name: ROUTE.profile })
}

async function adjustPickup(): Promise<void> {
  if (route.name === ROUTE.bookPick) return
  if (route.name !== ROUTE.book) await router.push({ name: ROUTE.book, query: { category: booking.category } })
  await router.push({ name: ROUTE.bookPick, params: { index: '0' } })
}
</script>

<template>
  <div class="shell" :style="pageStyle">
    <!-- Camera padding is owned by the stage (it re-runs framing on changes). -->
    <MapView :style-url="session.config.map.style_url" :center="initialView.center" :zoom="initialView.zoom" @ready="onReady">
      <!-- The pin lifts under the passenger's finger, not during camera animations. -->
      <CenterPin
        v-if="stage.pin.visible"
        :lifted="stage.gesture.value"
        :padding="stage.padding.value"
        :label="stage.pin.label"
        :callout="stage.pin.callout"
        @callout="stage.pin.onCallout?.()"
      />
      <template #fallback>
        <JEmptyState :title="t('map.unavailable_title')" :description="t('map.unavailable_text')" />
      </template>
    </MapView>

    <Transition name="jt-glass-pop">
      <div v-if="stage.controls.menu" class="shell__top" :style="topStyle">
        <JMapButton :label="t('home.menu')" @click="openProfile">
          <Menu :size="24" :stroke-width="2" aria-hidden="true" />
        </JMapButton>
      </div>
      <div v-else-if="stage.controls.back && !inTelegram" class="shell__top" :style="topStyle">
        <JMapButton :label="t('common.back')" @click="navigateBack(router)">
          <ChevronLeft :size="26" :stroke-width="2" aria-hidden="true" />
        </JMapButton>
      </div>
    </Transition>

    <Transition name="jt-glass-pop">
      <button v-if="showAccuracyChip" type="button" class="shell__chip jt-glass" :style="chipStyle" @click="adjustPickup">
        <TriangleAlert :size="16" :stroke-width="2" aria-hidden="true" />
        <span>{{ t('booking.gps_inaccurate') }}</span>
        <span class="shell__chip-meta jt-tabular">±{{ Math.round(location.accuracy ?? 0) }} {{ t('units.m') }}</span>
      </button>
    </Transition>

    <div v-show="sideFits" class="shell__side" :style="bottomStyle">
      <Transition name="jt-glass-pop">
        <JMapButton v-if="stage.controls.recenter" :label="t('map.recenter')" @click="stage.controls.recenter?.()">
          <Crosshair :size="22" :stroke-width="2" aria-hidden="true" />
        </JMapButton>
      </Transition>
      <Transition name="jt-glass-pop">
        <JMapButton v-if="stage.controls.locate" :label="t('map.locate_me')" :loading="location.locating" :active="location.following" @click="locate">
          <Navigation :size="22" :stroke-width="2" :fill="location.following ? 'currentColor' : 'none'" aria-hidden="true" />
        </JMapButton>
      </Transition>
    </div>

    <RouterView v-slot="{ Component, route: child }">
      <Transition name="jt-sheet-swap" mode="out-in">
        <div :key="String(child.name)" class="shell__screen">
          <component :is="Component" />
        </div>
      </Transition>
    </RouterView>
  </div>
</template>

<style scoped>
.shell {
  position: absolute;
  inset: 0;
  overflow: hidden;
}

.shell__top,
.shell__side {
  position: fixed;
  z-index: var(--jt-z-floating);
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.shell__side {
  transition: bottom var(--jt-duration-spring) var(--jt-ease-spring);
}

.shell__chip {
  position: fixed;
  z-index: var(--jt-z-floating);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 36px;
  padding: 0 14px;
  border-radius: var(--jt-radius-capsule);
  font: var(--jt-font-footnote);
  font-weight: 600;
  color: var(--jt-color-label);
  transition: bottom var(--jt-duration-spring) var(--jt-ease-spring);
}

.shell__chip:active {
  opacity: var(--jt-opacity-pressed);
}

.shell__chip-meta {
  color: var(--jt-color-label-secondary);
  font-weight: 400;
}

.shell__screen {
  position: absolute;
  inset: 0;
  z-index: var(--jt-z-sheet);
  pointer-events: none;
}

.shell__screen > :deep(*) {
  pointer-events: auto;
}
</style>
