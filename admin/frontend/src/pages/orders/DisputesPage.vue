<!-- Disputes: open first, resolve in place (orders.manage). -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { disputesApi, type Dispute } from '../../api/orders'
import { AppButton, DataTable, Pagination, StateView, StatusBadge, Tabs, toast, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useAuthStore } from '../../stores/auth'
import ResolveDisputeDialog from './ResolveDisputeDialog.vue'

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()

const list = usePagedList({ status: 'open' }, (f, page, size, signal) => disputesApi.list(f.status, page, size, signal))
const f = list.filters

const tabs = computed(() => [
  { value: 'open', label: t('dispute_status.open') },
  { value: 'resolved', label: t('dispute_status.resolved') },
  { value: '', label: t('common.all') },
])

const columns = computed<Column<Dispute>[]>(() => {
  const cols: Column<Dispute>[] = [
    { key: 'order_number', label: t('orders.order'), width: '90px', mono: true },
    { key: 'status', label: t('common.status'), width: '110px' },
    { key: 'created_at', label: t('orders.opened_at'), width: '140px', format: (v) => fmt.dateTime(v as string) },
    { key: 'opened_by_type', label: t('orders.opened_by'), format: (v) => (te(`actor.${String(v)}`, 'kk') ? t(`actor.${String(v)}`) : String(v)) },
    { key: 'previous_status', label: t('orders.previous_status'), format: (v) => t(`order_status.${String(v)}`) },
    { key: 'reason', label: t('common.reason') },
  ]
  if (f.status !== 'open') {
    cols.push(
      { key: 'outcome', label: t('orders.outcome'), format: (v) => (v ? t(`orders.outcome_${String(v)}`) : '—') },
      { key: 'resolved_by', label: t('orders.resolved_by'), value: (r) => r.resolved_by?.full_name ?? null },
      { key: 'resolved_at', label: t('orders.resolved_at'), format: (v) => fmt.dateTime(v as string | null) },
    )
  }
  if (auth.can('orders.manage')) cols.push({ key: 'actions', label: '', align: 'right', width: '120px' })
  return cols
})

const resolving = ref<Dispute | null>(null)
const resolveOpen = computed({
  get: () => resolving.value !== null,
  set: (v) => {
    if (!v) resolving.value = null
  },
})

function onResolved(): void {
  toast.success(t('orders.dispute_resolved'))
  void list.reload({ silent: true })
}

useRealtimeEvents(['order.disputed', 'order.completed', 'order.cancelled'], () => void list.reload({ silent: true }), 1500)
useResync(() => void list.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <Tabs v-model="f.status" :tabs="tabs" :aria-label="t('common.status')" />
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable :columns="columns" :rows="list.rows.value" row-key="id" :loading="list.loading.value" :empty-text="t('orders.no_disputes')">
        <template #cell-order_number="{ row }">
          <RouterLink :to="{ name: 'order-detail', params: { id: row.order_id } }" class="jt-mono">#{{ row.order_number }}</RouterLink>
        </template>
        <template #cell-status="{ row }">
          <StatusBadge kind="dispute" :status="row.status" />
        </template>
        <template #cell-reason="{ row }">
          <span class="reason-cell" :title="row.reason">{{ row.reason }}</span>
        </template>
        <template #cell-actions="{ row }">
          <AppButton v-if="row.status === 'open'" size="sm" variant="primary" @click="resolving = row">{{ t('orders.resolve') }}</AppButton>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
    <ResolveDisputeDialog v-model:open="resolveOpen" :dispute="resolving" @resolved="onResolved" />
  </section>
</template>

<style scoped>
.reason-cell {
  display: inline-block;
  max-width: 360px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
</style>
