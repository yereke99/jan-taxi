<!-- Drivers: status, city, online filters and search by code, phone, plate or name. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { driversApi, type AdminDriverListItem } from '../../api/drivers'
import { DataTable, FilterSelect, FiltersBar, Pagination, StateView, StatusBadge, type Column } from '../../components'
import { useCities } from '../../composables/useCities'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { MODERATION_STATUSES } from '../../utils/status'

const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()
const { options: cityOptions } = useCities()

const list = usePagedList({ status: '', city_id: '', online: '', q: '' }, (f, page, size, signal) => driversApi.list(f, page, size, signal))
const f = list.filters

const statusOptions = computed(() => MODERATION_STATUSES.map((s) => ({ value: s, label: t(`moderation_status.${s}`) })))
const onlineOptions = computed(() => [
  { value: 'true', label: t('common.online') },
  { value: 'false', label: t('common.offline') },
])

const columns = computed<Column<AdminDriverListItem>[]>(() => [
  { key: 'code', label: t('drivers.code'), width: '90px', mono: true },
  { key: 'driver', label: t('drivers.full_name') },
  { key: 'status', label: t('common.status') },
  { key: 'city', label: t('common.city'), value: (r) => fmt.name(r.city.name) },
  { key: 'vehicle', label: t('drivers.vehicle') },
  { key: 'completed_trips', label: t('drivers.trips'), align: 'right' },
  { key: 'wallet_available', label: t('drivers.available'), align: 'right', mono: true, format: (v) => fmt.money(v as number | null) },
])

function open(row: AdminDriverListItem): void {
  void router.push({ name: 'driver-detail', params: { id: row.id } })
}

useRealtimeEvents(['driver.status_changed', 'moderation.submitted', 'moderation.approved', 'moderation.rejected', 'moderation.changes_requested'], () => void list.reload({ silent: true }), 1500)
useResync(() => void list.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <FiltersBar v-model:search="f.q" :search-placeholder="t('drivers.search_placeholder')" @reset="list.reset">
      <FilterSelect v-model="f.status" :options="statusOptions" :placeholder="t('drivers.all_statuses')" />
      <FilterSelect v-model="f.city_id" :options="cityOptions" :placeholder="t('common.all_cities')" />
      <FilterSelect v-model="f.online" :options="onlineOptions" :placeholder="t('drivers.any_availability')" />
    </FiltersBar>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable
        :columns="columns"
        :rows="list.rows.value"
        row-key="id"
        :loading="list.loading.value"
        :empty-text="t('drivers.empty')"
        clickable
        :caption="t('nav.drivers')"
        @row-click="open"
      >
        <template #cell-driver="{ row }">
          <span class="two-line">
            <span>{{ row.first_name }} {{ row.last_name }}</span>
            <span class="jt-muted">
              <span class="jt-mono">{{ row.phone ?? '—' }}</span>
              <template v-if="row.phone && !row.phone_verified"> · {{ t('drivers.unverified') }}</template>
            </span>
          </span>
        </template>
        <template #cell-status="{ row }">
          <span class="two-line">
            <StatusBadge kind="moderation" :status="row.status" />
            <span class="online" :class="{ 'is-on': row.is_online }">
              <span class="online__dot" aria-hidden="true" />{{ row.is_online ? t('common.online') : t('common.offline') }}
            </span>
          </span>
        </template>
        <template #cell-completed_trips="{ row }">
          <span class="two-line two-line--end">
            <span class="jt-num">{{ fmt.count(row.completed_trips) }}</span>
            <span v-if="row.rating !== null" class="jt-muted jt-num">★ {{ row.rating.toFixed(2) }} ({{ row.rating_count }})</span>
          </span>
        </template>
        <template #cell-vehicle="{ row }">
          <span v-if="row.vehicle" class="two-line">
            <span>{{ row.vehicle.make }} {{ row.vehicle.model }} · {{ row.vehicle.color }}</span>
            <span class="jt-mono jt-muted">{{ row.vehicle.plate }}</span>
          </span>
          <span v-else class="jt-muted">—</span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </section>
</template>

<style scoped>
.online {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--jt-color-label-secondary);
}

.online__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--jt-color-fill-tertiary);
}

.online.is-on {
  color: var(--jt-color-label);
}

.online.is-on .online__dot {
  background: var(--jt-color-green);
}

.two-line {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 18px;
}

.two-line--end {
  align-items: flex-end;
}

.online {
  font: var(--jt-font-caption);
}
</style>
