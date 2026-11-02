<!--
  Coupon rows: campaign and version, the passenger, amount and minimum fare,
  validity (decided by expires_at on the server), the order that earned it and
  the order that used it. An unused coupon can be voided by an approver with a
  reason; the system's void reason is translated, an operator's shown as typed.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SYSTEM_VOID_REASONS, couponsApi, type Coupon } from '../../api/marketing'
import { AppButton, DataTable, ReasonDialog, StatusBadge, toast, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useAuthStore } from '../../stores/auth'
import OrderLink from './OrderLink.vue'
import PersonCell from './PersonCell.vue'

const props = withDefaults(defineProps<{ rows: Coupon[]; loading?: boolean; showUser?: boolean }>(), { loading: false, showUser: true })
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const voiding = ref<Coupon | null>(null)
const voidOpen = computed({
  get: () => voiding.value !== null,
  set: (v) => {
    if (!v) voiding.value = null
  },
})

const columns = computed<Column<Coupon>[]>(() => {
  const cols: Column<Coupon>[] = [{ key: 'issued_at', label: t('marketing.col_issued'), width: '150px' }]
  if (props.showUser) cols.push({ key: 'user', label: t('marketing.col_passenger') })
  cols.push(
    { key: 'campaign_key', label: t('marketing.campaign') },
    { key: 'amount', label: t('marketing.col_amount'), align: 'right' },
    { key: 'status', label: t('common.status') },
    { key: 'orders', label: t('marketing.col_orders') },
  )
  if (auth.can('marketing.approve')) cols.push({ key: 'actions', label: '', align: 'right', width: '110px' })
  return cols
})

function voidText(reason: string): string {
  return (SYSTEM_VOID_REASONS as readonly string[]).includes(reason) ? t(`marketing.void_reason_${reason}`) : reason
}

async function submitVoid(reason: string): Promise<void> {
  const c = voiding.value
  if (!c) return
  await couponsApi.void(c.id, reason)
  toast.success(t('marketing.voided_ok'))
  emit('changed')
}
</script>

<template>
  <DataTable :columns="columns" :rows="rows" row-key="id" :loading="loading" :empty-text="t('marketing.no_coupons')">
    <template #cell-issued_at="{ row }">
      <span class="stack">
        <span>{{ fmt.dateTime(row.issued_at) }}</span>
        <span class="jt-muted note">{{ t('marketing.coupon_valid_until', { time: fmt.dateTime(row.expires_at) }) }}</span>
      </span>
    </template>
    <template #cell-user="{ row }">
      <PersonCell :id="row.user_id" />
    </template>
    <template #cell-campaign_key="{ row }">
      <span class="stack">
        <span class="jt-mono">{{ row.campaign_key }}</span>
        <span class="jt-muted note">{{ t('marketing.version_n', { n: row.version }) }} · {{ row.categories.map((c) => t(`category.${c}`)).join(', ') }}</span>
      </span>
    </template>
    <template #cell-amount="{ row }">
      <span class="stack stack--end">
        <span class="jt-mono">{{ fmt.money(row.amount) }}</span>
        <span class="jt-muted note">{{ t('marketing.coupon_min_fare', { amount: fmt.money(row.min_order_fare) }) }}</span>
      </span>
    </template>
    <template #cell-status="{ row }">
      <span class="stack">
        <StatusBadge kind="coupon" :status="row.status" />
        <span v-if="row.void_reason" class="jt-muted note" :title="voidText(row.void_reason)">{{ voidText(row.void_reason) }}</span>
      </span>
    </template>
    <template #cell-orders="{ row }">
      <span class="stack">
        <span v-if="row.source_order_number !== null" class="jt-muted">{{ t('marketing.coupon_source') }} <OrderLink :number="row.source_order_number" /></span>
        <span v-if="row.redeemed_order_number !== null" class="jt-muted">{{ t('marketing.coupon_redeemed_on') }} <OrderLink :number="row.redeemed_order_number" /></span>
        <span v-if="row.source_order_number === null && row.redeemed_order_number === null" class="jt-muted">—</span>
      </span>
    </template>
    <template #cell-actions="{ row }">
      <AppButton v-if="row.status === 'issued'" size="sm" variant="destructive" @click="voiding = row">{{ t('marketing.void') }}</AppButton>
    </template>
  </DataTable>
  <ReasonDialog
    v-model:open="voidOpen"
    :title="t('marketing.void_title')"
    :message="voiding ? t('marketing.void_message', { amount: fmt.money(voiding.amount) }) : ''"
    :max-length="300"
    :min-length="3"
    :confirm-label="t('marketing.void')"
    destructive
    :submit="submitVoid"
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

.note {
  max-width: 170px;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 12px;
}
</style>
