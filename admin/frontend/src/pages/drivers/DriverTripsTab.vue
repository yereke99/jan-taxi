<!-- Orders taken by the driver (GET /admin/drivers/{id}/orders). -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { driversApi, type AdminDriverDetail } from '../../api/drivers'
import type { AdminOrderListItem } from '../../api/orders'
import { Card, DataTable, FilterSelect, KeyValueList, Pagination, StateView, StatusBadge, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { DEFAULT_PAGE_SIZE } from '../../services/api'
import { ORDER_STATUSES } from '../../utils/status'

const props = defineProps<{ driver: AdminDriverDetail }>()

const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()
const page = ref(1)
const status = ref('')

const res = useResource((signal) => driversApi.orders(props.driver.id, status.value, page.value, DEFAULT_PAGE_SIZE, signal))
watch([page, status], () => void res.reload(), { immediate: true })
watch(status, () => (page.value = 1))

const statusOptions = computed(() => ORDER_STATUSES.filter((s) => s !== 'draft').map((s) => ({ value: s, label: t(`order_status.${s}`) })))

const stats = computed(() => {
  const s = props.driver.stats
  return [
    { key: 'completed', label: t('drivers.stat_completed'), value: fmt.count(s.completed_trips) },
    { key: 'accepted', label: t('drivers.stat_accepted'), value: fmt.count(s.accepted_orders) },
    { key: 'cancelled', label: t('drivers.stat_cancelled'), value: fmt.count(s.cancelled_by_driver) },
    { key: 'fares', label: t('drivers.stat_fares'), value: fmt.money(s.total_fares) },
    { key: 'commission', label: t('drivers.stat_commission'), value: fmt.money(s.total_commission) },
  ]
})

const columns = computed<Column<AdminOrderListItem>[]>(() => [
  { key: 'number', label: '№', width: '80px', mono: true, format: (v) => `#${String(v)}` },
  { key: 'created_at', label: t('common.created'), format: (v) => fmt.dateTime(v as string) },
  { key: 'status', label: t('common.status') },
  { key: 'category', label: t('orders.category'), format: (v) => t(`category.${String(v)}`) },
  { key: 'route', label: t('orders.route'), value: (r) => `${r.pickup.address} → ${r.destination.address}` },
  { key: 'fare', label: t('orders.fare'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'commission_amount', label: t('orders.commission'), align: 'right', format: (v) => fmt.money(v as number) },
])
</script>

<template>
  <div class="trips">
    <Card :title="t('drivers.stats')">
      <KeyValueList :items="stats" />
    </Card>
    <Card :title="t('drivers.trips')" flush>
      <template #actions>
        <FilterSelect v-model="status" :options="statusOptions" :placeholder="t('orders.all_statuses')" />
      </template>
      <StateView v-if="res.error.value" :error="res.error.value" compact @retry="res.reload()" />
      <DataTable
        v-else
        :columns="columns"
        :rows="res.data.value?.items ?? []"
        row-key="id"
        :loading="res.loading.value"
        :empty-text="t('orders.empty')"
        clickable
        @row-click="(r) => router.push({ name: 'order-detail', params: { id: r.id } })"
      >
        <template #cell-status="{ row }">
          <StatusBadge kind="order" :status="row.status" />
        </template>
        <template #cell-route="{ value }">
          <span class="route" :title="String(value)">{{ value }}</span>
        </template>
      </DataTable>
    </Card>
    <Pagination v-model:page="page" :page-size="DEFAULT_PAGE_SIZE" :total="res.data.value?.total ?? 0" />
  </div>
</template>

<style scoped>
.trips {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr);
  grid-template-rows: auto auto;
  gap: var(--jt-space-4);
  align-items: start;
}

.trips > :nth-child(3) {
  grid-column: 2;
}

.route {
  display: inline-block;
  max-width: 340px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
</style>
