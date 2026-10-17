<!--
  Driver home (approved): who you are, the online switch, the active trip,
  the wallet summary, a small map with your own position, available orders
  and the menu. Location is reported in the background while online or on a
  trip (stores/location); this screen only shows it.
-->
<script setup lang="ts">
import { ChevronRight, LocateFixed, Navigation } from '@lucide/vue'
import type { Map as MapLibreMap } from 'maplibre-gl'
import { computed, onMounted, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import DriverPage from '../components/driver/DriverPage.vue'
import OnlineCard from '../components/driver/OnlineCard.vue'
import PrivateImage from '../components/driver/PrivateImage.vue'
import { JButton, JListGroup, JListRow, JMapButton } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { currentTarget, stopLetter } from '../features/trip/sequence'
import { MapView, useCarMarker } from '../maps'
import { ROUTE } from '../router/names'
import { useDutyStore } from '../stores/duty'
import { useLocationStore } from '../stores/location'
import { useOrdersStore } from '../stores/orders'
import { useProfileStore } from '../stores/profile'
import { useSessionStore } from '../stores/session'
import { useTripStore } from '../stores/trip'
import { useWalletStore } from '../stores/wallet'
import { motionMs } from '../utils/motion'
import { useAddressText } from '../i18n/core'

const { t } = useI18n()
const addr = useAddressText()
const fmt = useFormat()
const router = useRouter()
const session = useSessionStore()
const profile = useProfileStore()
const duty = useDutyStore()
const orders = useOrdersStore()
const trip = useTripStore()
const wallet = useWalletStore()
const location = useLocationStore()

const map = shallowRef<MapLibreMap | null>(null)
const car = useCarMarker(map, t('map.user_location'))

const driver = computed(() => session.driver)
const name = computed(() => {
  const p = profile.profile
  if (p) return `${p.first_name} ${p.last_name}`.trim()
  const me = session.me
  return me ? `${me.first_name} ${me.last_name}`.trim() : ''
})
const fix = computed(() => location.current)
const activeTarget = computed(() => (trip.active ? currentTarget(trip.active) : null))
const lowBalance = computed(() => wallet.wallet !== null && wallet.wallet.available < wallet.wallet.min_online_balance)

function onMapReady(m: MapLibreMap): void {
  map.value = m
  showFix()
}

function showFix(): void {
  const f = fix.value
  car.set(f ? { lat: f.lat, lon: f.lon, heading: f.heading } : null)
  if (f && map.value) map.value.easeTo({ center: [f.lon, f.lat], zoom: 15, duration: motionMs(400) })
}

watch(fix, showFix)

function locate(): void {
  void location.locateOnce().catch(() => undefined)
}

/** Main sections switch like the tab bar (no back history between them). */
function openTab(name: string): void {
  void router.replace({ name })
}

onMounted(() => {
  void wallet.refreshWallet()
  void trip.refresh()
  void profile.refresh()
  if (duty.isOnline) void orders.refresh()
  if (!location.current) locate()
})
</script>

<template>
  <DriverPage :title="t('home.title')" tab-bar>
    <section class="who">
      <div class="who__avatar">
        <PrivateImage :url="profile.profile?.avatar?.url ?? null" :alt="name" round />
      </div>
      <div class="who__text">
        <p class="who__name">{{ name }}</p>
        <p v-if="driver" class="who__code jt-tabular">{{ t('home.driver_code') }}: {{ driver.code }}</p>
      </div>
    </section>

    <OnlineCard />

    <button v-if="trip.active" type="button" class="trip-banner" @click="router.push({ name: ROUTE.trip })">
      <Navigation :size="24" :stroke-width="2" class="trip-banner__icon" aria-hidden="true" />
      <span class="trip-banner__text">
        <span class="trip-banner__title">{{ t('home.active_trip') }} · {{ t(`trip.status.${trip.active.status}`) }}</span>
        <span v-if="activeTarget" class="trip-banner__sub">{{ stopLetter(activeTarget.seq) }} · {{ addr(activeTarget.address) }}</span>
      </span>
      <ChevronRight :size="22" :stroke-width="2.25" aria-hidden="true" />
    </button>

    <section class="money">
      <div class="money__main">
        <p class="money__label">{{ t('home.available') }}</p>
        <p class="money__value jt-tabular">{{ wallet.wallet ? fmt.money(wallet.wallet.available) : '—' }}</p>
        <p v-if="wallet.wallet && wallet.wallet.reserved > 0" class="money__sub jt-tabular">
          {{ t('home.reserved') }}: {{ fmt.money(wallet.wallet.reserved) }}
        </p>
        <p v-if="lowBalance && wallet.wallet" class="money__warn">{{ t('home.min_online', { amount: fmt.money(wallet.wallet.min_online_balance) }) }}</p>
      </div>
      <JButton size="medium" :variant="lowBalance ? 'primary' : 'secondary'" @click="router.push({ name: ROUTE.topup })">
        {{ t('home.top_up') }}
      </JButton>
    </section>

    <section class="mini-map" :aria-label="t('home.map_title')">
      <MapView
        :style-url="session.config.map.style_url"
        :center="fix ?? session.config.map.default_center"
        :zoom="fix ? 15 : session.config.map.default_zoom"
        :interactive="false"
        @ready="onMapReady"
      >
        <template #fallback>
          <p class="mini-map__note">{{ t('map.unavailable_title') }}</p>
        </template>
      </MapView>
      <p v-if="!fix" class="mini-map__overlay jt-glass">{{ t('home.no_position') }}</p>
      <div class="mini-map__btn">
        <JMapButton :label="t('map.locate_me')" @click="locate">
          <LocateFixed :size="22" :stroke-width="2" aria-hidden="true" />
        </JMapButton>
      </div>
    </section>

    <JListGroup>
      <JListRow
        clickable
        chevron
        :title="t('home.orders_available')"
        :value="duty.isOnline ? (orders.loaded ? (orders.count > 0 ? t('home.orders_count', { n: orders.count }) : t('home.no_orders')) : '…') : t('home.offline_short')"
        @click="openTab(ROUTE.orders)"
      />
    </JListGroup>

    <JListGroup :header="t('home.menu_title')">
      <JListRow clickable chevron :title="t('home.menu_orders')" @click="openTab(ROUTE.orders)" />
      <JListRow
        :title="t('home.menu_trip')"
        :value="trip.active ? '' : t('home.no_active_trip')"
        :to="trip.active ? { name: ROUTE.trip } : undefined"
        :disabled="!trip.active"
      />
      <JListRow :title="t('home.menu_history')" :to="{ name: ROUTE.history }" />
      <JListRow clickable chevron :title="t('home.menu_wallet')" @click="openTab(ROUTE.wallet)" />
      <JListRow clickable chevron :title="t('home.menu_profile')" @click="openTab(ROUTE.profile)" />
      <JListRow :title="t('home.menu_support')" :to="{ name: ROUTE.support }" />
      <JListRow :title="t('home.menu_language')" :value="t(`languages.${session.language}`)" :to="{ name: ROUTE.language }" />
    </JListGroup>
  </DriverPage>
</template>

<style scoped>
.who {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
}

.who__avatar {
  flex: none;
  width: 64px;
  height: 64px;
}

.who__text {
  min-width: 0;
}

.who__name {
  font: var(--jt-font-title3);
  overflow-wrap: anywhere;
}

.who__code {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.trip-banner {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: calc(100% - 2 * var(--jt-side-margin));
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  text-align: left;
}

.trip-banner:active {
  opacity: var(--jt-opacity-pressed);
}

.trip-banner__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.trip-banner__title {
  font: var(--jt-font-headline);
}

.trip-banner__sub {
  font: var(--jt-font-subheadline);
  opacity: 0.8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.money {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
  margin: 0 var(--jt-side-margin) var(--jt-space-5);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
}

.money__main {
  flex: 1;
  min-width: 0;
}

.money__label {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.money__value {
  font: var(--jt-font-title1);
}

.money__sub {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.money__warn {
  margin-top: var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label);
}

.mini-map {
  position: relative;
  height: 180px;
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
  border-radius: var(--jt-radius-card);
  overflow: hidden;
  background: var(--jt-color-fill-secondary);
  /* MapView fills its parent. */
  isolation: isolate;
}

.mini-map :deep(.jt-map) {
  position: absolute;
  inset: 0;
}

.mini-map__overlay {
  position: absolute;
  left: var(--jt-space-3);
  top: var(--jt-space-3);
  z-index: 2;
  padding: 6px 12px;
  border-radius: var(--jt-radius-capsule);
  font: var(--jt-font-footnote);
}

.mini-map__note {
  padding: var(--jt-space-4);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.mini-map__btn {
  position: absolute;
  right: var(--jt-space-3);
  bottom: var(--jt-space-3);
  z-index: 2;
}
</style>
