<!--
  Route editor: 2..max_points stops (A pickup, up to three intermediate, the
  destination). Every change re-quotes the route (booking store); markers can
  be dragged on the map; the map frames all stops and the route above the
  sheet. Points outside Kazakhstan block the "Continue" button.
-->
<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { reverseGeocode } from '../../api/geo'
import FarePreview from '../../components/booking/FarePreview.vue'
import RouteStopList from '../../components/booking/RouteStopList.vue'
import { JButton, JSheet, toast } from '../../components/ui'
import { loadProblem } from '../../domain/vehicleClass'
import { useErrorText } from '../../i18n/core'
import { STOP_LETTERS } from '../../maps'
import { ROUTE } from '../../router/names'
import { useAutoFit, useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { POOR_ACCURACY_M, useLocationStore } from '../../stores/location'
import { useSessionStore } from '../../stores/session'
import { haptics } from '../../telegram'
import { hasCoordinates, roundCoord, type LatLon } from '../../utils/geo'
import { CATEGORIES, type Category } from '../../utils/status'

const { t } = useI18n()
const errorText = useErrorText()
const route = useRoute()
const router = useRouter()
const stage = useStageScreen()
const booking = useBookingStore()
const location = useLocationStore()
const session = useSessionStore()

stage.controls.locate = true
stage.controls.back = true

// The inline notice replaces the floating GPS chip on this screen.
const lowAccuracy = computed(
  () => booking.stops[0]?.place?.source === 'gps' && (booking.pickupAccuracy ?? 0) > POOR_ACCURACY_M,
)

/** Inline message per stop: outside Kazakhstan, duplicates, other API reasons. */
const stopErrorTexts = computed(() => {
  const out: Record<number, string> = {}
  booking.stops.forEach((s, i) => {
    if (s.place?.inServiceArea === false) out[i] = t('booking.outside')
  })
  for (const [k, reason] of Object.entries(booking.stopErrors)) {
    const i = Number(k)
    out[i] =
      reason === 'outside_service_area' ? t('booking.outside') : reason === 'duplicate' ? t('booking.duplicate') : t('booking.stop_invalid')
  }
  return out
})

/** Preview problems that are not tied to one stop (route unavailable, network, the load…). */
const generalError = computed(() => {
  if (booking.previewStatus !== 'error' || Object.keys(booking.stopErrors).length > 0) return ''
  switch (loadProblem(booking.previewError)) {
    case 'passengers':
      return t('confirm.load_passengers')
    case 'cargo':
      return t('confirm.load_cargo')
    case 'unavailable':
      return t('confirm.load_unavailable')
    default:
      return errorText(booking.previewError)
  }
})

// --- Map ---------------------------------------------------------------------

function syncMarkers(): void {
  const pts = booking.stops.flatMap((s, i) => (s.place && hasCoordinates(s.place) ? [{ lat: s.place.lat, lon: s.place.lon, letter: STOP_LETTERS[i] }] : []))
  stage.stops.setStops(pts)
  markerIndex = booking.stops.flatMap((s, i) => (s.place && hasCoordinates(s.place) ? [i] : []))
}

let markerIndex: number[] = []
const dragControllers = new Map<number, AbortController>()

async function onMarkerDrag(markerPos: number, point: LatLon): Promise<void> {
  const index = markerIndex[markerPos]
  if (index === undefined) return
  dragControllers.get(index)?.abort()
  const ctrl = new AbortController()
  dragControllers.set(index, ctrl)
  try {
    const r = await reverseGeocode(point, { lang: session.language, signal: ctrl.signal })
    if (ctrl.signal.aborted) return
    booking.setStopPlace(index, {
      lat: roundCoord(point.lat),
      lon: roundCoord(point.lon),
      address: r.title,
      subtitle: r.title ? r.subtitle : null,
      source: 'map',
      inServiceArea: r.in_service_area,
    })
    if (!r.in_service_area) haptics.notification('error')
  } catch (err) {
    if (ctrl.signal.aborted) return
    toast.error(errorText(err))
    syncMarkers()
  } finally {
    if (dragControllers.get(index) === ctrl) dragControllers.delete(index)
  }
}

stage.setStopDrag((pos, point) => void onMarkerDrag(pos, point))

function framePoints(): LatLon[] {
  const pts: LatLon[] = booking.stops.flatMap((s) => (s.place && hasCoordinates(s.place) ? [s.place] : []))
  const q = booking.quoteCurrent ? booking.quote : null
  if (q?.bbox) pts.push({ lon: q.bbox[0], lat: q.bbox[1] }, { lon: q.bbox[2], lat: q.bbox[3] })
  return pts
}

watch(
  () => booking.stops,
  () => syncMarkers(),
  { immediate: true },
)

watch(
  () => (booking.quoteCurrent ? booking.quote : null),
  (q) => stage.route.setRoute(q?.geometry ?? null),
  { immediate: true },
)

// Re-frame when the set of points or the quoted route changes and after the
// sheet settles; viewport changes keep the camera.
const autoFit = useAutoFit(stage, framePoints)
watch(
  () => [booking.routeKey, booking.quoteCurrent ? booking.quote?.quote_id : null] as const,
  () => autoFit.refit(),
  { immediate: true },
)

// --- Pickup from GPS ---------------------------------------------------------

const pickupCtrl = new AbortController()

async function fillPickup(): Promise<void> {
  if (booking.stops[0]?.place) return
  const loc = location.current ?? (await location.locate())
  if (!loc || booking.stops[0]?.place || pickupCtrl.signal.aborted) return
  try {
    await booking.setPickupFromLocation(loc, pickupCtrl.signal)
  } catch {
    // The passenger chooses the pickup manually.
  }
}

onMounted(() => {
  const c = route.query.category
  if (typeof c === 'string' && (CATEGORIES as readonly string[]).includes(c)) booking.setCategory(c as Category)
  // Back from the search for a new stop without choosing a place.
  booking.pruneEmptyStops()
  booking.schedulePreview(true)
  void fillPickup()
})

onBeforeUnmount(() => {
  pickupCtrl.abort()
  for (const c of dragControllers.values()) c.abort()
})

// --- Actions -----------------------------------------------------------------

function select(index: number): void {
  void router.push({ name: ROUTE.bookSearch, params: { index: String(index) } })
}

function add(): void {
  if (booking.addStop()) {
    haptics.selection()
    void router.push({ name: ROUTE.bookSearch, params: { index: String(booking.stops.length - 2) } })
  }
}

function adjustPickup(): void {
  void router.push({ name: ROUTE.bookPick, params: { index: '0' } })
}

const continuing = ref(false)
function next(): void {
  if (!booking.canContinue) return
  continuing.value = true
  void router.push({ name: ROUTE.confirm }).finally(() => (continuing.value = false))
}
</script>

<template>
  <JSheet :snap="'content'" :snap-points="['content']" :aria-label="t('booking.title')" @height="(h) => (stage.sheetHeight.value = h)">
    <template #header>
      <div class="editor-head">
        <h1 class="editor-head__title">{{ t('booking.title') }}</h1>
      </div>
    </template>

    <RouteStopList
      :stops="booking.stops"
      :errors="stopErrorTexts"
      :can-add="booking.canAdd"
      :max-points="booking.maxPoints"
      @select="select"
      @remove="booking.removeStop"
      @move="booking.moveStop"
      @add="add"
    />

    <button v-if="lowAccuracy" type="button" class="editor-gps" @click="adjustPickup">
      <TriangleAlert :size="18" :stroke-width="2" aria-hidden="true" />
      <span class="editor-gps__text">
        <strong>{{ t('booking.gps_inaccurate') }}</strong>
        <span>{{ t('booking.gps_inaccurate_hint', { meters: Math.round(booking.pickupAccuracy ?? 0) }) }}</span>
      </span>
      <span class="editor-gps__action">{{ t('booking.adjust_pin') }}</span>
    </button>

    <FarePreview class="editor-quote" :error="generalError" />

    <template #footer>
      <JButton block :disabled="!booking.canContinue" :loading="continuing" @click="next">{{ t('booking.continue') }}</JButton>
    </template>
  </JSheet>
</template>

<style scoped>
.editor-head {
  padding-bottom: var(--jt-space-3);
}

.editor-head__title {
  font: var(--jt-font-title1);
}

.editor-gps {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-2);
  width: 100%;
  margin-top: var(--jt-space-3);
  padding: var(--jt-space-3);
  border-radius: 16px;
  background: var(--jt-color-fill-secondary);
  text-align: left;
  font: var(--jt-font-footnote);
}

.editor-gps:active {
  opacity: var(--jt-opacity-pressed);
}

.editor-gps__text {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
  color: var(--jt-color-label-secondary);
}

.editor-gps__text strong {
  color: var(--jt-color-label);
  font-weight: 600;
}

.editor-gps__action {
  flex: none;
  align-self: center;
  font-weight: 600;
  color: var(--jt-color-label);
}

.editor-quote {
  margin-top: var(--jt-space-4);
}
</style>
