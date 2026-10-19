<!--
  Dashboard: live counters (now / today), period totals for the chosen dates
  and city, daily charts and the online-drivers map. Refreshes on realtime
  events and every minute.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RouteLocationRaw } from 'vue-router'
import { localDate } from '../../api/common'
import { dashboardApi } from '../../api/system'
import { Card, DateRange, FilterSelect, FiltersBar, StateView } from '../../components'
import ColumnChart from '../../components/charts/ColumnChart.vue'
import LineChart from '../../components/charts/LineChart.vue'
import { useCities } from '../../composables/useCities'
import { useFormat } from '../../composables/useFormat'
import { useListQuery } from '../../composables/useListQuery'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import type { Permission } from '../../permissions'
import { useAuthStore } from '../../stores/auth'
import CancelReasonsCard from './CancelReasonsCard.vue'
import KpiTile from './KpiTile.vue'
import LiveDriversMap from './LiveDriversMap.vue'

const DEFAULT_DAYS = 7
const MAX_DAYS = 92

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const { options: cityOptions } = useCities()
const q = useListQuery({ from: '', to: '', city_id: '' })
const f = q.filters

function daysAgo(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return localDate(d)
}

const range = computed(() => ({ from: f.from || daysAgo(DEFAULT_DAYS - 1), to: f.to || localDate() }))
const tooLong = computed(() => {
  const a = Date.parse(range.value.from)
  const b = Date.parse(range.value.to)
  return Number.isFinite(a) && Number.isFinite(b) && (b - a) / 86_400_000 + 1 > MAX_DAYS
})

const res = useResource((signal) => dashboardApi.get({ ...range.value, city_id: f.city_id }, signal))
watch(
  () => q.key.value,
  () => {
    if (!tooLong.value) void res.reload()
  },
  { immediate: true },
)

const d = computed(() => res.data.value)

function link(perm: Permission, to: RouteLocationRaw): RouteLocationRaw | null {
  return auth.can(perm) ? to : null
}

const now = computed(() => {
  const m = d.value
  if (!m) return []
  return [
    { key: 'active', label: t('dashboard.active_orders'), value: fmt.count(m.orders.active_now), detail: t('dashboard.searching_n', { n: m.orders.searching_now }), to: link('orders.view', { name: 'orders', query: { status: 'searching' } }) },
    { key: 'online', label: t('dashboard.drivers_online'), value: fmt.count(m.drivers.online_now), detail: t('dashboard.offline_n', { n: m.drivers.offline_now }), to: link('drivers.view', { name: 'drivers', query: { online: 'true' } }) },
    {
      key: 'applications',
      label: t('dashboard.pending_applications'),
      value: fmt.count(m.drivers.pending_applications),
      detail: t('dashboard.changes_requested_n', { n: m.drivers.changes_requested }),
      to: link('drivers.moderate', { name: 'moderation' }),
      tone: m.drivers.pending_applications > 0 ? ('attention' as const) : ('neutral' as const),
    },
    {
      key: 'topups',
      label: t('dashboard.pending_topups'),
      value: fmt.count(m.topups.pending_verification_count),
      detail: fmt.money(m.topups.pending_verification_amount),
      to: link('finance.view', { name: 'topups' }),
      tone: m.topups.pending_verification_count > 0 ? ('attention' as const) : ('neutral' as const),
    },
  ]
})

const today = computed(() => {
  const s = d.value?.today
  if (!s) return []
  return [
    { key: 'created', label: t('dashboard.orders_created'), value: fmt.count(s.orders_created) },
    { key: 'completed', label: t('dashboard.orders_completed'), value: fmt.count(s.orders_completed) },
    { key: 'cancelled', label: t('dashboard.orders_cancelled'), value: fmt.count(s.orders_cancelled) },
    { key: 'disputed', label: t('dashboard.orders_disputed'), value: fmt.count(s.orders_disputed), to: link('orders.view', { name: 'disputes' }) },
    { key: 'commission', label: t('dashboard.commission_net'), value: fmt.money(s.commission_net), detail: t('dashboard.gmv_value', { amount: fmt.money(s.gmv) }) },
    { key: 'topups', label: t('dashboard.topups_confirmed'), value: fmt.money(s.topups_confirmed_amount), detail: t('dashboard.count_n', { n: s.topups_confirmed_count }) },
  ]
})

const period = computed(() => {
  const m = d.value
  if (!m) return []
  const o = m.orders
  const perf = m.performance
  return [
    { key: 'created', label: t('dashboard.orders_created'), value: fmt.count(o.created), detail: t('dashboard.by_category', { taxi: o.by_category.taxi, parcel: o.by_category.parcel, other: o.by_category.other }) },
    { key: 'completed', label: t('dashboard.orders_completed'), value: fmt.count(o.completed), detail: perf.completion_rate_bps === null ? '' : t('dashboard.completion_rate', { rate: fmt.percentFromBps(perf.completion_rate_bps) }) },
    {
      key: 'cancelled',
      label: t('dashboard.orders_cancelled'),
      value: fmt.count(o.cancelled),
      detail: t('dashboard.cancel_breakdown', { p: o.cancelled_by_passenger, d: o.cancelled_by_driver, a: o.cancelled_by_admin, n: o.no_driver_found }),
    },
    { key: 'disputed', label: t('dashboard.orders_disputed'), value: fmt.count(o.disputed) },
    { key: 'gmv', label: t('dashboard.gmv'), value: fmt.money(m.revenue.gmv) },
    { key: 'commission', label: t('dashboard.commission_net'), value: fmt.money(m.revenue.commission_net), detail: t('dashboard.commission_breakdown', { charged: fmt.money(m.revenue.commission_charged), refunded: fmt.money(m.revenue.commission_refunded) }) },
    { key: 'topups', label: t('dashboard.topups_confirmed'), value: fmt.money(m.topups.confirmed_amount), detail: t('dashboard.topups_breakdown', { n: m.topups.confirmed_count, refunded: fmt.money(m.topups.refunded_amount) }) },
    { key: 'accept', label: t('dashboard.avg_accept_time'), value: perf.avg_accept_time_s === null ? '—' : fmt.duration(perf.avg_accept_time_s), detail: perf.avg_trip_duration_s === null ? '' : t('dashboard.avg_trip', { time: fmt.duration(perf.avg_trip_duration_s) }) },
    { key: 'registered', label: t('dashboard.drivers_registered'), value: fmt.count(m.drivers.registered_in_period), detail: t('dashboard.drivers_total', { approved: m.drivers.approved, total: m.drivers.total, suspended: m.drivers.suspended }) },
    { key: 'wallets', label: t('dashboard.wallets_total'), value: fmt.money(m.wallets.total_balance), detail: t('dashboard.wallets_reserved', { amount: fmt.money(m.wallets.total_reserved) }) },
    {
      key: 'bonuses',
      label: t('dashboard.bonuses'),
      value: fmt.money((m.revenue.welcome_bonuses ?? 0) + (m.revenue.manual_bonuses ?? 0)),
      detail: t('dashboard.bonuses_breakdown', { welcome: fmt.money(m.revenue.welcome_bonuses ?? 0), manual: fmt.money(m.revenue.manual_bonuses ?? 0) }),
      to: link('finance.view', { name: 'ledger', query: { type: 'bonus_credit' } }),
    },
    {
      key: 'notifications',
      label: t('dashboard.notifications_dead'),
      value: fmt.count(m.notifications.dead),
      detail: t('dashboard.notifications_pending', { n: m.notifications.pending }),
      to: link('orders.manage', { name: 'notifications', query: { status: 'dead' } }),
      tone: m.notifications.dead > 0 ? ('attention' as const) : ('neutral' as const),
    },
  ]
})

const labels = computed(() => (d.value?.daily ?? []).map((r) => r.date))
const orderSeries = computed(() => [
  { key: 'created', label: t('dashboard.orders_created'), values: (d.value?.daily ?? []).map((r) => r.orders_created), shade: 'light' as const },
  { key: 'completed', label: t('dashboard.orders_completed'), values: (d.value?.daily ?? []).map((r) => r.orders_completed), shade: 'strong' as const },
])
const commissionValues = computed(() => (d.value?.daily ?? []).map((r) => r.commission))
const topupValues = computed(() => (d.value?.daily ?? []).map((r) => r.topups_confirmed_amount))

let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && !tooLong.value) void res.reload({ silent: true })
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(timer))

useRealtimeEvents(
  ['order.created', 'order.accepted', 'order.completed', 'order.cancelled', 'order.disputed', 'topup.updated', 'topup.confirmed', 'moderation.submitted', 'moderation.approved', 'moderation.rejected', 'moderation.changes_requested', 'driver.status_changed'],
  () => {
    if (!tooLong.value) void res.reload({ silent: true })
  },
  3000,
)
useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <FiltersBar :show-search="false" @reset="q.reset">
      <DateRange v-model:from="f.from" v-model:to="f.to" />
      <FilterSelect v-model="f.city_id" :options="cityOptions" :placeholder="t('common.all_cities')" />
      <span class="jt-muted range-note">{{ t('dashboard.range_note', { from: fmt.date(range.from), to: fmt.date(range.to), tz: d?.timezone ?? '' }) }}</span>
    </FiltersBar>
    <p v-if="tooLong" class="warn" role="alert">{{ t('dashboard.range_too_long', { n: MAX_DAYS }) }}</p>

    <StateView v-if="!d" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <h2 class="jt-section-title">{{ t('dashboard.now') }}</h2>
      <div class="tiles tiles--4">
        <KpiTile v-for="k in now" :key="k.key" :label="k.label" :value="k.value" :detail="k.detail" :to="k.to" :tone="k.tone" />
      </div>

      <h2 class="jt-section-title">{{ t('dashboard.today') }}</h2>
      <div class="tiles tiles--6">
        <KpiTile v-for="k in today" :key="k.key" :label="k.label" :value="k.value" :detail="k.detail" :to="k.to" />
      </div>

      <div class="maprow">
        <Card :title="t('dashboard.live_map')" :subtitle="t('dashboard.live_map_hint')" flush>
          <LiveDriversMap v-if="auth.can('drivers.view')" :city-id="f.city_id" />
        </Card>
        <Card :title="t('dashboard.orders_per_day')">
          <ColumnChart :labels="labels" :series="orderSeries" :format="(n) => fmt.count(n)" :caption="t('dashboard.orders_per_day')" />
        </Card>
      </div>

      <h2 class="jt-section-title">{{ t('dashboard.period') }}</h2>
      <div class="tiles tiles--4">
        <KpiTile v-for="k in period" :key="k.key" :label="k.label" :value="k.value" :detail="k.detail" :to="k.to" :tone="k.tone" />
      </div>

      <CancelReasonsCard :counts="d.orders.cancelled_by_reason" />

      <div class="jt-grid-2">
        <Card :title="t('dashboard.commission_per_day')">
          <LineChart :labels="labels" :values="commissionValues" :label="t('dashboard.commission_net')" :format="(n) => fmt.money(n)" :caption="t('dashboard.commission_per_day')" />
        </Card>
        <Card :title="t('dashboard.topups_per_day')">
          <LineChart :labels="labels" :values="topupValues" :label="t('dashboard.topups_confirmed')" :format="(n) => fmt.money(n)" :caption="t('dashboard.topups_per_day')" />
        </Card>
      </div>
    </template>
  </section>
</template>

<style scoped>
.tiles {
  display: grid;
  gap: var(--jt-space-3);
}

.tiles--4 {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.tiles--6 {
  grid-template-columns: repeat(6, minmax(0, 1fr));
}

.tiles--6 :deep(.kpi__value) {
  font: var(--jt-font-title);
}

.maprow {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.range-note {
  font: var(--jt-font-small);
}

.warn {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}
</style>
