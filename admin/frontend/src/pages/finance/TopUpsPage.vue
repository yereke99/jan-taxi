<!-- Kaspi top-ups: pending verification first (oldest first), then other statuses. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { dayEnd, dayStart, opt } from '../../api/common'
import { REJECT_DUPLICATE_RECEIPT, topUpsApi, type AdminTopUp } from '../../api/finance'
import { DataTable, DateRange, DriverPicker, ExportButton, FiltersBar, Pagination, StateView, StatusBadge, Tabs, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'

const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()

const list = usePagedList({ status: 'pending_verification', driver_id: '', from: '', to: '', q: '' }, (f, page, size, signal) =>
  topUpsApi.list(f, page, size, signal),
)
const f = list.filters

const tabs = computed(() => [
  ...(['pending_verification', 'created', 'confirmed', 'rejected', 'expired', 'refunded'] as const).map((s) => ({ value: s as string, label: t(`topup_status.${s}`) })),
  { value: '', label: t('common.all') },
])

const columns = computed<Column<AdminTopUp>[]>(() => {
  const cols: Column<AdminTopUp>[] = [
    { key: 'reference', label: t('finance.reference'), mono: true },
    { key: 'driver', label: t('orders.driver') },
    { key: 'amount', label: t('finance.col_requested'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
  ]
  if (f.status !== 'pending_verification' && f.status !== 'created') {
    cols.push({ key: 'verified_amount', label: t('finance.col_verified'), align: 'right', mono: true, format: (v) => fmt.money(v as number | null) })
  }
  cols.push(
    { key: 'report', label: t('finance.col_report') },
    { key: 'created_at', label: t('common.created'), format: (v) => fmt.dateTime(v as string) },
    { key: 'status', label: t('common.status') },
  )
  return cols
})

function exportQuery(): Record<string, string | undefined> {
  return { status: opt(f.status), from: dayStart(f.from), to: dayEnd(f.to) }
}

useRealtimeEvents(['topup.updated', 'topup.confirmed'], () => void list.reload({ silent: true }), 1000)
useResync(() => void list.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <Tabs v-model="f.status" :tabs="tabs" :aria-label="t('common.status')" />
    <FiltersBar v-model:search="f.q" :search-placeholder="t('finance.topup_search')" @reset="list.reset">
      <DriverPicker v-model="f.driver_id" />
      <DateRange v-model:from="f.from" v-model:to="f.to" />
      <template #actions>
        <ExportButton kind="topups" :query="exportQuery" />
      </template>
    </FiltersBar>
    <p v-if="f.status === 'pending_verification'" class="hint">{{ t('finance.pending_hint') }}</p>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable
        :columns="columns"
        :rows="list.rows.value"
        row-key="id"
        :loading="list.loading.value"
        :empty-text="t('finance.no_topups')"
        clickable
        :caption="t('nav.topups')"
        @row-click="(r) => router.push({ name: 'topup-detail', params: { id: r.id } })"
      >
        <template #cell-driver="{ row }">
          <span class="two-line">
            <span class="jt-mono">{{ row.driver.code }}</span>
            <span class="jt-muted">{{ row.driver.first_name }} {{ row.driver.last_name }}</span>
          </span>
        </template>
        <template #cell-report="{ row }">
          <span v-if="row.driver_reported_paid_at" class="two-line">
            <span class="jt-mono">{{ row.driver_payment_reference ?? '—' }}</span>
            <span class="jt-muted">{{ fmt.relative(row.driver_reported_paid_at) }}</span>
          </span>
          <span v-else class="jt-muted">—</span>
        </template>
        <template #cell-status="{ row }">
          <span class="two-line">
            <span class="badges">
              <StatusBadge kind="topup" :status="row.status" />
              <span v-if="row.confirmed_automatically" class="auto">{{ t('finance.auto_badge') }}</span>
            </span>
            <span v-if="row.rejected_reason === REJECT_DUPLICATE_RECEIPT" class="receipt receipt--bad">{{ t('finance.reject_reason_duplicate_receipt') }}</span>
            <span v-else-if="row.has_receipt" class="receipt">{{ t('finance.has_receipt') }}</span>
          </span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </section>
</template>

<style scoped>
.hint {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  color: var(--jt-color-label-secondary);
}

.two-line {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 18px;
}

.receipt {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.receipt--bad {
  color: var(--jt-color-red-text);
}

.badges {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.auto {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: 600 12px/16px var(--jt-font-family);
}
</style>
