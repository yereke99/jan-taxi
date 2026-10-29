<!--
  Place search for one stop: type-ahead /geo/search (300 ms debounce, at
  least 2 characters, superseded requests aborted), recent places from the
  order history, "use my location" for the pickup and "choose on map".
-->
<script setup lang="ts">
import { Clock, LocateFixed, MapPin, MapPinned } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { GEO_SEARCH_MIN_CHARS, searchPlaces } from '../../api/geo'
import type { GeoPlace } from '../../api/types'
import { JSheet, JSkeleton, JSpinner, JTextField, toast } from '../../components/ui'
import { createLatest } from '../../domain/latest'
import type { PlaceValue } from '../../domain/route'
import { useAddressText, useErrorText } from '../../i18n/core'
import { BOOKING_FROM_HOME, ROUTE } from '../../router/names'
import { formatDistance } from '../../utils/format'
import { hasCoordinates, haversineMeters, type LatLon } from '../../utils/geo'
import { useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { navigateBack } from '../../telegram'
import { cleanText, codePointLength } from '../../utils/text'

const SEARCH_DEBOUNCE_MS = 300

const { t } = useI18n()
const addr = useAddressText()
const errorText = useErrorText()
const route = useRoute()
const router = useRouter()
const stage = useStageScreen()
const booking = useBookingStore()
const location = useLocationStore()
const orders = useOrdersStore()
const session = useSessionStore()

const index = computed(() => Number(route.params.index))
const isPickup = computed(() => index.value === 0)
const isDestination = computed(() => index.value === booking.stops.length - 1)
const title = computed(() =>
  isPickup.value ? t('search.title_pickup') : isDestination.value ? t('search.title_destination') : t('search.title_stop'),
)

const field = ref<InstanceType<typeof JTextField> | null>(null)
const query = ref('')
const results = ref<GeoPlace[]>([])
const searching = ref(false)
const searchError = ref<unknown>(null)
const searchedFor = ref('')
const usingLocation = ref(false)

const trimmed = computed(() => query.value.trim())
const tooShort = computed(() => codePointLength(trimmed.value) < GEO_SEARCH_MIN_CHARS)

/**
 * Where results should be near: the live GPS position, else the route's
 * pickup, else the map centre, else the city centre from the config — the
 * search is always local (the API orders and filters by distance).
 */
function searchNear(): LatLon {
  const pickup=booking.stops[0]?.place
  return location.current ?? (pickup && hasCoordinates(pickup) ? pickup : null) ?? stage.center() ?? session.config.map.default_center
}

const runner = createLatest<string, GeoPlace[]>({
  delayMs: SEARCH_DEBOUNCE_MS,
  run: (q, signal) => {
    const near = searchNear()
    return searchPlaces(q, { near, lang: session.language, signal }).then((list) => {
      resultsNear.value = near
      return list
    })
  },
  onStart: () => {
    searching.value = true
  },
  onResult: (list, q) => {
    results.value = list
    searchedFor.value = q
    searching.value = false
    searchError.value = null
  },
  onError: (err) => {
    searching.value = false
    searchError.value = err
  },
})

watch(trimmed, (q) => {
  if (codePointLength(q) < GEO_SEARCH_MIN_CHARS) {
    runner.cancel()
    results.value = []
    searchedFor.value = ''
    searching.value = false
    searchError.value = null
    return
  }
  searching.value = true
  runner.schedule(q)
})

const recent = computed(() => orders.places.filter(p=>isDestination.value || hasCoordinates(p)))
const manualMode = ref(false)
const manualText = ref('')
const canManual = computed(()=>isDestination.value && session.config.orders?.manual_destination_enabled === true && session.config.orders.manual_destination_pricing === 'passenger_tariff_bounds')
const manualValid = computed(()=>codePointLength(cleanText(manualText.value))>0 && codePointLength(cleanText(manualText.value))<=200)
function saveManual(): void { if(!canManual.value || !manualValid.value)return; choose({lat:null,lon:null,address:cleanText(manualText.value),subtitle:null,source:'manual',inServiceArea:null}) }

/** Distance from where the search was made, so far-away matches are obvious. */
const resultsNear = ref<LatLon | null>(null)
function distanceText(p: GeoPlace): string {
  const near = resultsNear.value
  return near ? formatDistance(haversineMeters(near, p), session.language) : ''
}

function done(): void {
  if (route.query.from === BOOKING_FROM_HOME) void router.replace({ name: ROUTE.book, query: { category: booking.category } })
  else navigateBack(router)
}

function choose(place: PlaceValue): void {
  if (!isDestination.value && !hasCoordinates(place)) return
  booking.setStopPlace(index.value, place)
  done()
}

function chooseResult(p: GeoPlace): void {
  choose({ lat: p.lat, lon: p.lon, address: p.title, subtitle: p.subtitle, source: 'search', inServiceArea: true })
}

async function useMyLocation(): Promise<void> {
  if (usingLocation.value) return
  usingLocation.value = true
  try {
    const loc = await location.locate()
    if (!loc) {
      toast.error(location.error === 'outside_kz' ? t('gps.outside_kz') : errorText(location.error))
      return
    }
    await booking.setPickupFromLocation(loc)
    done()
  } catch (err) {
    toast.error(errorText(err))
  } finally {
    usingLocation.value = false
  }
}

function chooseOnMap(): void {
  void router.replace({ name: ROUTE.bookPick, params: { index: String(index.value) }, query: route.query })
}

onMounted(() => {
  if (!Number.isInteger(index.value) || index.value < 0 || index.value >= booking.stops.length) {
    void router.replace({ name: ROUTE.book })
    return
  }
  if (!orders.historyLoaded && !orders.historyLoading) void orders.loadHistory()
  void nextTick(() => field.value?.focus())
})
onBeforeUnmount(() => runner.cancel())
</script>

<template>
  <JSheet :snap="'full'" :snap-points="['full']" :floating="false" :glass="false" :aria-label="title" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <div class="search-head">
        <h1 class="search-head__title">{{ title }}</h1>
        <JTextField
          ref="field"
          v-model="query"
          type="search"
          inputmode="search"
          enterkeyhint="search"
          clearable
          :placeholder="t('search.placeholder')"
        />
      </div>
    </template>

    <div v-if="manualMode && canManual" class="search-actions">
      <JTextField v-model="manualText" :label="t('manual.label')" :hint="t('manual.helper')" :max="200" />
      <button type="button" class="search-row" :disabled="!manualValid" @click="saveManual">{{ t('manual.save') }}</button>
      <button type="button" class="search-row" @click="manualMode=false">{{ t('manual.use_search') }}</button>
    </div>
    <div v-else class="search-actions">
      <button v-if="canManual" type="button" class="search-row" @click="manualMode=true; manualText=query">{{ t('manual.action') }}</button>
      <button v-if="isPickup" type="button" class="search-row" :disabled="usingLocation" @click="useMyLocation">
        <span class="search-row__icon is-accent">
          <JSpinner v-if="usingLocation" />
          <LocateFixed v-else :size="20" :stroke-width="2" aria-hidden="true" />
        </span>
        <span class="search-row__text">
          <span class="search-row__title">{{ usingLocation ? t('search.locating') : t('search.use_my_location') }}</span>
        </span>
      </button>
      <button type="button" class="search-row" @click="chooseOnMap">
        <span class="search-row__icon"><MapPinned :size="20" :stroke-width="2" aria-hidden="true" /></span>
        <span class="search-row__text">
          <span class="search-row__title">{{ t('search.choose_on_map') }}</span>
        </span>
      </button>
    </div>

    <template v-if="!manualMode && tooShort">
      <p v-if="trimmed.length > 0" class="search-note">{{ t('search.min_chars') }}</p>
      <section v-if="recent.length > 0" class="search-section">
        <h2 class="search-section__title">{{ t('search.recent') }}</h2>
        <button v-for="p in recent" :key="`${p.lat},${p.lon}`" type="button" class="search-row" @click="choose(p)">
          <span class="search-row__icon"><Clock :size="18" :stroke-width="2" aria-hidden="true" /></span>
          <span class="search-row__text">
            <span class="search-row__title">{{ addr(p.address) }}</span>
          </span>
        </button>
      </section>
    </template>

    <section v-else-if="!manualMode" class="search-section" aria-live="polite">
      <h2 class="search-section__title">{{ t('search.results') }}</h2>
      <div v-if="searching && results.length === 0" class="search-skeletons">
        <div v-for="n in 4" :key="n" class="search-skeleton"><JSkeleton width="70%" /><JSkeleton width="40%" height="12px" /></div>
      </div>
      <p v-else-if="searchError" class="search-note is-error" role="alert">{{ errorText(searchError) }}</p>
      <p v-else-if="results.length === 0 && searchedFor" class="search-note">{{ t('search.nothing_found') }}</p>
      <template v-else>
        <button v-for="p in results" :key="`${p.lat},${p.lon},${p.title}`" type="button" class="search-row" @click="chooseResult(p)">
          <span class="search-row__icon"><MapPin :size="18" :stroke-width="2" aria-hidden="true" /></span>
          <span class="search-row__text">
            <span class="search-row__title">{{ p.title }}</span>
            <span v-if="p.subtitle" class="search-row__subtitle">{{ p.subtitle }}</span>
          </span>
          <span v-if="resultsNear" class="search-row__meta jt-tabular">{{ distanceText(p) }}</span>
        </button>
      </template>
    </section>
  </JSheet>
</template>

<style scoped>
.search-head {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-3);
}

.search-head__title {
  font: var(--jt-font-title2);
}

.search-actions {
  display: flex;
  flex-direction: column;
  margin-bottom: var(--jt-space-4);
}

.search-section {
  display: flex;
  flex-direction: column;
}

.search-section__title {
  padding: var(--jt-space-2) 0;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
}

.search-row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 56px;
  padding: 8px 0;
  text-align: left;
}

.search-row + .search-row {
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.search-row:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
}

.search-row__icon {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.search-row__icon.is-accent {
  color: var(--jt-color-blue);
}

.search-row__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.search-row__title {
  font: var(--jt-font-body);
  overflow-wrap: anywhere;
}

.search-row__subtitle {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.search-row__meta {
  flex: none;
  max-width: 30%;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  white-space: nowrap;
}

.search-note {
  padding: var(--jt-space-3) 0;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.search-note.is-error {
  color: var(--jt-color-red-text);
}

.search-skeletons {
  display: flex;
  flex-direction: column;
}

.search-skeleton {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 14px 0 14px 48px;
}
</style>
