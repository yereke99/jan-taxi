<!--
  Promotions applied to orders: the fare, the discount, what the passenger
  paid in cash and the driver's compensation. finance.manage may reverse a
  settled one (reason + confirmation): the compensation is taken back from the
  driver's available balance and a spent bonus returns to the passenger.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { promotionsApi, type ApplicationRow } from '../../api/marketing'
import { AppButton, DataTable, ReasonDialog, StatusBadge, toast, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useAuthStore } from '../../stores/auth'
import OrderLink from './OrderLink.vue'
import PersonCell from './PersonCell.vue'

const props = withDefaults(defineProps<{ rows: ApplicationRow[]; loading?: boolean; showUser?: boolean }>(), { loading: false, showUser: true })
const emit = defineEmits<{ changed: [] }>()

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const reversing = ref<ApplicationRow | null>(null)
const reverseOpen = computed({
  get: () => reversing.value !== null,
  set: (v) => {
    if (!v) reversing.value = null
  },
})

const columns = computed<Column<ApplicationRow>[]>(() => {
  const cols: Column<ApplicationRow>[] = [{ key: 'order', label: t('orders.order') }]
  if (props.showUser) cols.push({ key: 'user', label: t('marketing.col_passenger') })
  cols.push(
    { key: 'kind', label: t('marketing.template') },
    { key: 'discount', label: t('marketing.col_discount'), align: 'right' },
    { key: 'status', label: t('common.status') },
    { key: 'compensation', label: t('marketing.col_compensation'), align: 'right' },
  )
  if (auth.can('finance.manage')) cols.push({ key: 'actions', label: '', align: 'right', width: '120px' })
  return cols
})

function reviewText(reason: string): string {
  return te(`marketing.review_reason_${reason}`, 'kk') ? t(`marketing.review_reason_${reason}`) : reason
}

/** 409 insufficient_balance (the driver's available balance is short) is shown by the dialog as is. */
async function submitReverse(reason: string): Promise<void> {
  const row = reversing.value
  if (!row) return
  await promotionsApi.reverse(row.id, reason)
  toast.success(t('marketing.reversed_ok'))
  emit('changed')
}
</script>

<template>
  <DataTable :columns="columns" :rows="rows" row-key="id" :loading="loading" :empty-text="t('marketing.no_promotions')">
    <template #cell-order="{ row }">
      <span class="stack">
        <span class="order">
          <OrderLink :number="row.order_number" :id="row.order_id" />
          <StatusBadge kind="order" :status="row.order_status" />
        </span>
        <span class="jt-muted">{{ fmt.dateTime(row.reserved_at) }}</span>
      </span>
    </template>
    <template #cell-user="{ row }">
      <PersonCell :id="row.user_id" />
    </template>
    <template #cell-kind="{ row }">
      <span :title="row.campaign_key ?? undefined">{{ t(`marketing.promo_kind_${row.kind}`) }}</span>
    </template>
    <template #cell-discount="{ row }">
      <span class="stack stack--end">
        <span class="jt-mono">−{{ fmt.money(row.discount) }}</span>
        <span class="jt-muted">{{ t('marketing.promo_cash_line', { fare: fmt.money(row.gross_fare), cash: fmt.money(row.gross_fare - row.discount) }) }}</span>
      </span>
    </template>
    <template #cell-status="{ row }">
      <span class="stack">
        <StatusBadge kind="promotion" :status="row.status" />
        <span v-if="row.review_reason" class="review">{{ reviewText(row.review_reason) }}</span>
      </span>
    </template>
    <template #cell-compensation="{ row }">
      <span class="stack stack--end">
        <span v-if="row.compensation !== null" class="jt-mono jt-positive">{{ fmt.signedMoney(row.compensation) }}</span>
        <span v-else-if="row.status === 'reserved'" class="jt-muted">{{ t('marketing.compensation_pending') }}</span>
        <span v-else class="jt-muted">—</span>
        <span v-if="row.driver_code" class="jt-mono jt-muted">{{ row.driver_code }}</span>
      </span>
    </template>
    <template #cell-actions="{ row }">
      <AppButton v-if="row.status === 'settled'" size="sm" variant="destructive" @click="reversing = row">{{ t('marketing.reverse') }}</AppButton>
    </template>
  </DataTable>
  <ReasonDialog
    v-model:open="reverseOpen"
    :title="reversing ? t('marketing.reverse_title', { n: reversing.order_number }) : ''"
    :message="reversing ? t('marketing.reverse_message', { amount: fmt.money(reversing.discount) }) : ''"
    :max-length="300"
    :min-length="3"
    :confirm-label="t('marketing.reverse')"
    destructive
    :submit="submitReverse"
  />
</template>

<style scoped>
.stack {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 18px;
}

.stack--end {
  align-items: flex-end;
}

.order {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.stack .jt-muted {
  font-size: 12px;
}

.review {
  max-width: 200px;
  font: var(--jt-font-caption);
  color: var(--jt-color-red-text);
  white-space: normal;
}
</style>
