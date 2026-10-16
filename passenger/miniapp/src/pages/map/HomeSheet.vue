<!--
  Home: greeting, the big "Where to?" row, the service segmented control,
  active orders, recent destinations and quick links. The map centres on the
  passenger's position once it is known; Embi is only the initial map centre.
-->
<script setup lang="ts">
import { Car, ChevronRight, Clock, Headset, Languages, ListOrdered, Search } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { OrderSummary } from '../../api/types'
import CategoryIcon from '../../components/CategoryIcon.vue'
import { JSegmented, JSheet, JSkeleton, type SegmentedOption, type SheetSnap } from '../../components/ui'
import type { PlaceValue } from '../../domain/route'
import { ROUTE } from '../../router/names'
import { useRealtime } from '../../services/realtime'
import { useStageScreen } from '../../stage/mapStage'
import { useBookingStore } from '../../stores/booking'
import { useLocationStore } from '../../stores/location'
import { useOrdersStore } from '../../stores/orders'
import { useSessionStore } from '../../stores/session'
import { formatMoney } from '../../utils/format'
import { CATEGORIES, type Category } from '../../utils/status'

const { t } = useI18n()
const router = useRouter()
const stage = useStageScreen()
const session = useSessionStore()
const booking = useBookingStore()
const orders = useOrdersStore()
const location = useLocationStore()
const realtime = useRealtime()

stage.controls.menu = true
stage.controls.locate = true
stage.controls.accuracyChip = true

const snap = ref<SheetSnap>('half')

const categoryOptions = computed<SegmentedOption<Category>[]>(() => CATEGORIES.map((c) => ({ value: c, label: t(`category.${c}`) })))
const category = computed({
  get: () => booking.category,
  set: (c: Category) => booking.setCategory(c),
})

const greeting = computed(() => {
  const name = session.me?.first_name
  return name ? t('home.greeting', { name }) : t('home.greeting_anonymous')
})
const whereTo = computed(() =>
  booking.category === 'parcel' ? t('home.where_to_parcel') : booking.category === 'other' ? t('home.where_to_other') : t('home.where_to'),
)

// Centre on the passenger once, unless they already moved the map themselves.
let userMovedMap = false
const offMove = stage.onMoveStart((user) => {
  if (user) userMovedMap = true
})
watch(
  () => [location.current, stage.map.value] as const,
  ([loc, map]) => {
    if (!loc || !map || userMovedMap) return
    stage.centerOn(loc, 15)
    userMovedMap = true
  },
  { immediate: true },
)

async function openBooking(): Promise<void> {
  await router.push({ name: ROUTE.book, query: { category: booking.category } })
  const last = booking.stops.length - 1
  if (!booking.stops[last]?.place) await router.push({ name: ROUTE.bookSearch, params: { index: String(last) } })
}

async function openRecent(place: PlaceValue): Promise<void> {
  booking.setStopPlace(booking.stops.length - 1, place)
  await router.push({ name: ROUTE.book, query: { category: booking.category } })
}

function openOrder(o: OrderSummary): void {
  void router.push({ name: ROUTE.order, params: { id: o.id } })
}

function refreshLists(): void {
  void orders.loadActive()
}

const offs: Array<() => void> = []
onMounted(() => {
  void orders.loadActive()
  if (!orders.historyLoaded) void orders.loadHistory()
  offs.push(
    realtime.on('*', (e) => {
      if (e.type.startsWith('order.')) refreshLists()
    }),
    realtime.onResync(refreshLists),
  )
})
onBeforeUnmount(() => {
  offMove()
  for (const off of offs.splice(0)) off()
})
</script>

<template>
  <JSheet
    v-model:snap="snap"
    :snap-points="['peek', 'half', 'full']"
    :peek-height="196"
    :aria-label="t('home.where_to')"
    @height="(h) => (stage.sheetHeight.value = h)"
  >
    <template #header>
      <div class="home-head">
        <h1 class="home-head__greeting">{{ greeting }}</h1>
        <button type="button" class="home-where" @click="openBooking">
          <Search :size="22" :stroke-width="2.25" aria-hidden="true" />
          <span>{{ whereTo }}</span>
        </button>
        <JSegmented v-model="category" :options="categoryOptions" :aria-label="t('home.category_label')" />
      </div>
    </template>

    <section v-if="orders.active.length > 0" class="home-section" :aria-label="t('home.active_title')">
      <button v-for="o in orders.active" :key="o.id" type="button" class="home-active" @click="openOrder(o)">
        <span class="home-active__icon"><CategoryIcon :category="o.category" /></span>
        <span class="home-active__text">
          <span class="home-active__status">{{ t(`order_status.${o.status}`) }}</span>
          <span class="home-active__route">{{ o.pickup.address }} → {{ o.destination.address }}</span>
        </span>
        <span class="home-active__fare jt-tabular">{{ formatMoney(o.fare, session.language) }}</span>
        <ChevronRight :size="20" class="home-chevron" aria-hidden="true" />
      </button>
    </section>

    <section v-if="(!orders.historyLoaded && orders.historyLoading) || orders.destinations.length > 0" class="home-section">
      <h2 class="home-section__title">{{ t('home.recent') }}</h2>
      <div v-if="!orders.historyLoaded && orders.historyLoading" class="home-list">
        <div v-for="n in 2" :key="n" class="home-skeleton"><JSkeleton width="60%" /><JSkeleton width="35%" height="12px" /></div>
      </div>
      <div v-else-if="orders.destinations.length > 0" class="home-list">
        <button v-for="p in orders.destinations" :key="`${p.lat},${p.lon}`" type="button" class="home-recent" @click="openRecent(p)">
          <span class="home-recent__icon"><Clock :size="18" :stroke-width="2" aria-hidden="true" /></span>
          <span class="home-recent__title">{{ p.address }}</span>
        </button>
      </div>
    </section>

    <nav class="home-links">
      <button type="button" class="home-link" @click="router.push({ name: ROUTE.orders })">
        <span class="home-link__icon"><ListOrdered :size="22" :stroke-width="1.75" aria-hidden="true" /></span>
        <span>{{ t('menu.my_orders') }}</span>
      </button>
      <button type="button" class="home-link" @click="router.push({ name: ROUTE.support })">
        <span class="home-link__icon"><Headset :size="22" :stroke-width="1.75" aria-hidden="true" /></span>
        <span>{{ t('menu.support') }}</span>
      </button>
      <button type="button" class="home-link" @click="router.push({ name: ROUTE.becomeDriver })">
        <span class="home-link__icon"><Car :size="22" :stroke-width="1.75" aria-hidden="true" /></span>
        <span>{{ t('menu.become_driver') }}</span>
      </button>
      <button type="button" class="home-link" @click="router.push({ name: ROUTE.language })">
        <span class="home-link__icon"><Languages :size="22" :stroke-width="1.75" aria-hidden="true" /></span>
        <span>{{ t('menu.language') }}</span>
      </button>
    </nav>
  </JSheet>
</template>

<style scoped>
.home-head {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-4);
}

.home-head__greeting {
  font: var(--jt-font-title3);
}

.home-where {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  height: 56px;
  padding: 0 var(--jt-space-5);
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-title3);
  text-align: left;
}

.home-where:active {
  opacity: var(--jt-opacity-pressed);
}

.home-section {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  margin-bottom: var(--jt-space-5);
}

.home-section__title {
  font: var(--jt-font-headline);
}

.home-active {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 64px;
  padding: var(--jt-space-3) var(--jt-space-3) var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  text-align: left;
}

.home-active:active {
  opacity: 0.8;
}

.home-active__icon {
  flex: none;
  display: flex;
}

.home-active__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.home-active__status {
  font: var(--jt-font-headline);
}

.home-active__route {
  font: var(--jt-font-footnote);
  opacity: 0.75;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-active__fare {
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.home-chevron {
  flex: none;
  opacity: 0.6;
}

.home-list {
  display: flex;
  flex-direction: column;
}

.home-skeleton {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: var(--jt-space-3) 0;
}

.home-recent {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 52px;
  text-align: left;
}

.home-recent + .home-recent {
  box-shadow: inset 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.home-recent:active {
  opacity: var(--jt-opacity-pressed);
}

.home-recent__icon {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.home-recent__title {
  flex: 1;
  min-width: 0;
  font: var(--jt-font-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home-links {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--jt-space-2);
  margin-bottom: var(--jt-space-2);
}

.home-link {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  min-height: 52px;
  padding: var(--jt-space-2) var(--jt-space-3);
  border-radius: 18px;
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
  font-weight: 600;
  text-align: left;
}

.home-link:active {
  opacity: var(--jt-opacity-pressed);
}

.home-link__icon {
  flex: none;
  display: flex;
}
</style>
