<!--
  Map-first layout: one full-screen MapLibre map shared by home, the route
  editor, the place picker, the confirmation and the live order (§14). Child
  routes render only their bottom sheet; they drive the map through the stage
  (stage/mapStage.ts). Floating Liquid Glass controls (§15.1) sit above the
  map: the profile button at the top, locate / recenter above the sheet.
-->
<script setup lang="ts">
import { CircleUserRound, Crosshair, LocateFixed, TriangleAlert } from '@lucide/vue'
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
import { canOpenLocationSettings, LocationError, openLocationSettings } from '../../telegram'

/** Gap between the sheet top and the floating buttons; clears the attribution row. */
const CONTROLS_GAP_PX = 40

const { t } = useI18n()
const errorText = useErrorText()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const location = useLocationStore()
const booking = useBookingStore()

const { stage, attach } = createMapStage({
  stop: (_index, letter) => t('booking.marker', { letter }),
  car: t('map.driver_car'),
  user: t('map.user_location'),
})
provide(STAGE_KEY, stage)

const pageStyle = computed(() => ({ '--jt-map-attribution-offset': `${stage.sheetHeight.value}px` }))
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

watch(
  () => location.current,
  (loc) => stage.user.set(loc),
)

function onReady(m: MapLibreMap): void {
  stage.map.value = m
  attach(m)
  if (location.current) stage.user.set(location.current)
  // First, silent attempt; problems surface only when the user taps locate.
  void location.locate()
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
    <MapView
      :style-url="session.config.map.style_url"
      :center="session.config.map.default_center"
      :zoom="session.config.map.default_zoom"
      :padding="stage.padding.value"
      @ready="onReady"
    >
      <CenterPin v-if="stage.pin.visible" :padding="stage.padding.value" :label="stage.pin.label" />
      <template #fallback>
        <JEmptyState :title="t('map.unavailable_title')" :description="t('map.unavailable_text')" />
      </template>
    </MapView>

    <Transition name="jt-glass-pop">
      <div v-if="stage.controls.menu" class="shell__top" :style="topStyle">
        <JMapButton :label="t('home.menu')" @click="openProfile">
          <CircleUserRound :size="24" :stroke-width="1.75" aria-hidden="true" />
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

    <div class="shell__side" :style="bottomStyle">
      <Transition name="jt-glass-pop">
        <JMapButton v-if="stage.controls.recenter" :label="t('map.recenter')" @click="stage.controls.recenter?.()">
          <Crosshair :size="22" :stroke-width="2" aria-hidden="true" />
        </JMapButton>
      </Transition>
      <Transition name="jt-glass-pop">
        <JMapButton v-if="stage.controls.locate" :label="t('map.locate_me')" :loading="location.locating" @click="locate">
          <LocateFixed :size="22" :stroke-width="2" aria-hidden="true" />
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
