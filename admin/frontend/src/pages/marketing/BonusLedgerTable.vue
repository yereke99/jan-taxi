<!-- Passenger bonus ledger rows: signed balance and reserve deltas, balances after, the order or operator behind each entry. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { BonusEntry } from '../../api/marketing'
import { DataTable, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import OrderLink from './OrderLink.vue'
import PersonCell from './PersonCell.vue'

const props = withDefaults(defineProps<{ rows: BonusEntry[]; loading?: boolean; showUser?: boolean }>(), { loading: false, showUser: true })

const { t, te } = useI18n()
const fmt = useFormat()

const columns = computed<Column<BonusEntry>[]>(() => {
  const cols: Column<BonusEntry>[] = [
    { key: 'id', label: 'ID', width: '70px', mono: true },
    { key: 'created_at', label: t('common.created'), width: '140px', format: (v) => fmt.dateTime(v as string) },
  ]
  if (props.showUser) cols.push({ key: 'user', label: t('marketing.col_passenger') })
  cols.push(
    { key: 'type', label: t('finance.entry_type'), format: (v) => typeLabel(String(v)) },
    { key: 'balance_delta', label: t('finance.col_change'), align: 'right' },
    { key: 'balance_after', label: t('finance.col_after'), align: 'right' },
    { key: 'order', label: t('finance.source') },
    { key: 'description', label: t('finance.description') },
  )
  return cols
})

function typeLabel(type: string): string {
  return te(`marketing.bonus_type_${type}`, 'kk') ? t(`marketing.bonus_type_${type}`) : type
}

/** System descriptions arrive in English; known ones are shown in the operator's language (operators' reasons stay as written). */
const SYSTEM_DESCRIPTIONS: Record<string, string> = {
  'referral reward': 'marketing.desc_referral_reward',
  'passenger credit': 'marketing.desc_passenger_credit',
  'order reservation': 'marketing.desc_order_reservation',
  'order cancelled': 'marketing.desc_order_cancelled',
  'paid part of the fare': 'marketing.desc_paid_part',
}

function describe(text: string): string {
  if (!text) return '—'
  const key = SYSTEM_DESCRIPTIONS[text]
  return key ? t(key) : text
}
</script>

<template>
  <DataTable :columns="columns" :rows="rows" row-key="id" :loading="loading" :empty-text="t('finance.no_entries')">
    <template #cell-user="{ row }">
      <PersonCell :id="row.user_id" />
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
    <template #cell-order="{ row }">
      <OrderLink v-if="row.order_number !== null" :number="row.order_number" :id="row.order_id" />
      <span v-else-if="row.admin_id" class="jt-muted">{{ t('actor.admin') }}</span>
      <span v-else class="jt-muted">—</span>
    </template>
    <template #cell-description="{ row }">
      <span class="desc" :title="row.description">{{ describe(row.description) }}</span>
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

.stack .jt-muted {
  font-size: 12px;
}

.desc {
  display: inline-block;
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
</style>
