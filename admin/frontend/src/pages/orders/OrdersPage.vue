<!-- Orders: filters in the URL, live refresh on order events, CSV export of the same filters. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { dayEnd, dayStart, localityText, opt } from '../../api/common'
import { orderTariffRegion, ordersApi, type AdminOrderListItem } from '../../api/orders'
import { DataTable, DateRange, ExportButton, FiltersBar, Pagination, FilterSelect, StateView, StatusBadge, type Column } from '../../components'
import { useCities } from '../../composables/useCities'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useVehicleClasses } from '../../composables/useVehicleClasses'
import { CATEGORIES, ORDER_STATUSES } from '../../utils/status'

const { t, te } = useI18n()
const fmt = useFormat()
const vc = useVehicleClasses()
const router = useRouter()
const { options: cityOptions } = useCities()

const list = usePagedList(
  { status: '', city_id: '', category: '', from: '', to: '', q: '' },
  (f, page, size, signal) => ordersApi.list(f, page, size, signal),
)
const f = list.filters

const statusOptions = computed(() => ORDER_STATUSES.filter((s) => s !== 'draft').map((s) => ({ value: s, label: t(`order_status.${s}`) })))
const categoryOptions = computed(() => CATEGORIES.map((c) => ({ value: c, label: t(`category.${c}`) })))

function reasonLabel(code: string): string {
  return te(`cancel_reason.${code}`, 'kk') ? t(`cancel_reason.${code}`) : code
}

/** Name of the region whose tariff priced the order (the nationwide one is «Қазақстан»). */
function tariffName(row: AdminOrderListItem): string {
  const r = orderTariffRegion(row)
  return r ? fmt.name(r.name) : ''
}

function personName(p: { first_name: string; last_name: string }): string {
  return `${p.first_name} ${p.last_name}`.trim() || '—'
}

const columns = computed<Column<AdminOrderListItem>[]>(() => [
  { key: 'number', label: '№' },
  { key: 'status', label: t('common.status') },
  { key: 'category', label: t('orders.category') },
  { key: 'passenger', label: t('orders.passenger') },
  { key: 'driver', label: t('orders.driver') },
  { key: 'locality', label: t('orders.locality') },
  { key: 'route', label: t('orders.route') },
  { key: 'fare', label: t('orders.fare'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
])

function open(row: AdminOrderListItem): void {
  void router.push({ name: 'order-detail', params: { id: row.id } })
}

function exportQuery(): Record<string, string | undefined> {
  return { status: opt(f.status), city_id: opt(f.city_id), from: dayStart(f.from), to: dayEnd(f.to) }
}

useRealtimeEvents(
  ['order.created', 'order.accepted', 'order.cancelled', 'order.driver_arriving', 'order.started', 'order.completed', 'order.disputed', 'order.taken'],
  () => void list.reload({ silent: true }),
  1500,
)
useResync(() => void list.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <FiltersBar v-model:search="f.q" :search-placeholder="t('orders.search_placeholder')" search-width="400px" @reset="list.reset">
      <FilterSelect v-model="f.status" :options="statusOptions" :placeholder="t('orders.all_statuses')" :aria-label="t('common.status')" />
      <FilterSelect v-model="f.city_id" :options="cityOptions" :placeholder="t('orders.all_tariff_regions')" :aria-label="t('orders.tariff_region')" />
      <FilterSelect v-model="f.category" :options="categoryOptions" :placeholder="t('orders.all_categories')" :aria-label="t('orders.category')" />
      <DateRange v-model:from="f.from" v-model:to="f.to" />
      <template #actions>
        <ExportButton kind="orders" :query="exportQuery" />
      </template>
    </FiltersBar>

    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable
        :columns="columns"
        :rows="list.rows.value"
        row-key="id"
        :loading="list.loading.value"
        :empty-text="t('orders.empty')"
        clickable
        :caption="t('nav.orders')"
        @row-click="open"
      >
        <template #cell-number="{ row }">
          <span class="two-line">
            <span class="jt-mono">#{{ row.number }}</span>
            <span class="jt-muted">{{ fmt.dateTime(row.created_at) }}</span>
          </span>
        </template>
        <template #cell-category="{ row }">
          <span class="two-line">
            <span>{{ t(`category.${row.category}`) }} · {{ vc.name(row.vehicle_class, row.vehicle_class_name) }}</span>
            <span v-if="row.passenger_count || row.cargo_size" class="jt-muted">
              <template v-if="row.passenger_count">{{ t('classes.seats_n', { n: row.passenger_count }) }}</template>
              <template v-else-if="row.cargo_size">{{ row.cargo_size.toUpperCase() }}</template>
            </span>
          </span>
        </template>
        <template #cell-status="{ row }">
          <span class="two-line">
            <span class="status-cell">
              <StatusBadge kind="order" :status="row.status" />
              <span v-if="row.has_open_dispute && row.status !== 'disputed'" class="flag">{{ t('orders.dispute_flag') }}</span>
            </span>
            <span v-if="row.status === 'cancelled' && row.cancel_reason_code" class="jt-muted">{{ reasonLabel(row.cancel_reason_code) }}</span>
          </span>
        </template>
        <template #cell-passenger="{ row }">
          <span class="two-line clip">
            <span class="ellipsis">{{ personName(row.passenger) }}</span>
            <span class="jt-muted jt-mono">{{ row.passenger.phone ?? '' }}</span>
          </span>
        </template>
        <template #cell-driver="{ row }">
          <span v-if="row.driver" class="two-line clip">
            <span class="jt-mono">{{ row.driver.code }}</span>
            <span class="jt-muted ellipsis">{{ personName(row.driver) }}</span>
          </span>
          <span v-else class="jt-muted">—</span>
        </template>
        <template #cell-locality="{ row }">
          <span class="two-line clip">
            <span v-if="localityText(row.locality)" class="ellipsis">{{ localityText(row.locality) }}</span>
            <span v-else class="jt-muted">{{ t('orders.locality_unknown') }}</span>
            <span v-if="tariffName(row)" class="jt-muted ellipsis">{{ t('orders.tariff_short', { region: tariffName(row) }) }}</span>
          </span>
        </template>
        <template #cell-route="{ row }">
          <span class="two-line route-cell">
            <span class="ellipsis">A · {{ row.pickup.address }}</span>
            <span class="ellipsis jt-muted">
              {{ String.fromCharCode(64 + row.stop_count) }} · {{ row.destination.address }}
              <template v-if="row.stop_count > 2"> ({{ t('orders.stops_n', { n: row.stop_count }) }})</template>
            </span>
          </span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </section>
</template>

<style scoped>
.status-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.flag {
  font: 600 11px/16px var(--jt-font-family);
  color: var(--jt-color-red-text);
}

.two-line {
  display: flex;
  flex-direction: column;
  line-height: 18px;
}

.route-cell {
  max-width: 220px;
}

.two-line .jt-muted {
  font-size: 12px;
}

.clip {
  max-width: 150px;
}

.ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
