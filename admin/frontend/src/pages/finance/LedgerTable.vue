<!-- Wallet journal rows: signed deltas, balances after, links to the order or top-up behind each entry. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminLedgerEntry } from '../../api/finance'
import { DataTable, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'

const props = withDefaults(defineProps<{ rows: AdminLedgerEntry[]; loading?: boolean; showDriver?: boolean }>(), {
  loading: false,
  showDriver: true,
})

const { t, te } = useI18n()
const fmt = useFormat()

const columns = computed<Column<AdminLedgerEntry>[]>(() => {
  const cols: Column<AdminLedgerEntry>[] = [
    { key: 'id', label: 'ID', width: '70px', mono: true },
    { key: 'created_at', label: t('common.created'), width: '140px', format: (v) => fmt.dateTime(v as string) },
  ]
  if (props.showDriver) cols.push({ key: 'driver', label: t('orders.driver') })
  cols.push(
    { key: 'entry_type', label: t('finance.entry_type'), format: (v) => (te(`ledger_type.${String(v)}`, 'kk') ? t(`ledger_type.${String(v)}`) : String(v)) },
    { key: 'balance_delta', label: t('finance.col_change'), align: 'right' },
    { key: 'balance_after', label: t('finance.col_after'), align: 'right' },
    { key: 'source', label: t('finance.source') },
    { key: 'description', label: t('finance.description') },
  )
  return cols
})

/** System descriptions arrive in English; known ones are shown in the operator's language. */
function describe(text: string | null): string {
  if (!text) return '—'
  const cancelled = /^order cancelled by (passenger|driver|admin|system)$/.exec(text)
  if (cancelled) return t('finance.desc_cancelled_by', { actor: t(`actor.${cancelled[1]}`) })
  if (text === 'order re-assigned by operator') return t('finance.desc_reassigned')
  return text
}
</script>

<template>
  <DataTable :columns="columns" :rows="rows" row-key="id" :loading="loading" :empty-text="t('finance.no_entries')">
    <template #cell-driver="{ row }">
      <span class="stack stack--start">
        <RouterLink :to="{ name: 'driver-detail', params: { id: row.driver.id } }" class="jt-mono">{{ row.driver.code }}</RouterLink>
        <span class="jt-muted">{{ row.driver.first_name }} {{ row.driver.last_name }}</span>
      </span>
    </template>
    <template #cell-balance_delta="{ row }">
      <span class="stack">
        <span class="jt-mono" :class="{ 'jt-positive': row.balance_delta > 0, 'jt-negative': row.balance_delta < 0 }">
          {{ row.balance_delta === 0 ? '—' : fmt.signedMoney(row.balance_delta) }}
        </span>
        <span v-if="row.reserved_delta !== 0" class="jt-mono jt-muted">{{ t('finance.reserve_short') }} {{ fmt.signedMoney(row.reserved_delta) }}</span>
      </span>
    </template>
    <template #cell-balance_after="{ row }">
      <span class="stack">
        <span class="jt-mono">{{ fmt.money(row.balance_after) }}</span>
        <span class="jt-mono jt-muted">{{ t('finance.reserve_short') }} {{ fmt.money(row.reserved_after) }}</span>
      </span>
    </template>
    <template #cell-source="{ row }">
      <RouterLink v-if="row.order_id" :to="{ name: 'order-detail', params: { id: row.order_id } }">#{{ row.order_number ?? '…' }}</RouterLink>
      <RouterLink v-else-if="row.topup_id" :to="{ name: 'topup-detail', params: { id: row.topup_id } }" class="jt-mono">{{ row.topup_reference ?? '…' }}</RouterLink>
      <span v-else-if="row.admin" class="jt-muted">{{ row.admin.full_name || row.admin.email }}</span>
      <span v-else class="jt-muted">—</span>
    </template>
    <template #cell-description="{ row }">
      <span class="desc" :title="row.description ?? ''">{{ describe(row.description) }}</span>
    </template>
  </DataTable>
</template>

<style scoped>
.stack {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-end;
  line-height: 18px;
}

.stack--start {
  align-items: flex-start;
}

.stack .jt-muted {
  font-size: 12px;
}

.desc {
  display: inline-block;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
</style>
