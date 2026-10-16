<!--
  Wallet reconciliation (§6): every wallet's balance and reserve must equal
  the sum of its journal deltas. Any mismatch is listed with its reasons.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ledgerApi, type ReconciliationMismatch } from '../../api/finance'
import { AppButton, Card, DataTable, StateView, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'

const { t, te } = useI18n()
const fmt = useFormat()
const res = useResource((signal) => ledgerApi.reconcile(signal))
void res.reload()

const columns = computed<Column<ReconciliationMismatch>[]>(() => [
  { key: 'driver', label: t('orders.driver') },
  { key: 'balance', label: t('finance.balance'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
  { key: 'ledger_balance', label: t('finance.ledger_balance'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
  { key: 'balance_diff', label: t('finance.diff'), align: 'right', mono: true, format: (v) => fmt.signedMoney(v as number) },
  { key: 'reserved', label: t('finance.reserved'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
  { key: 'ledger_reserved', label: t('finance.ledger_reserved'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
  { key: 'active_reservations', label: t('finance.active_reservations'), align: 'right', mono: true, format: (v) => fmt.money(v as number) },
  { key: 'reasons', label: t('common.reason'), value: (r) => r.reasons.map((x) => (te(`finance.mismatch_${x}`, 'kk') ? t(`finance.mismatch_${x}`) : x)).join('; ') },
])
</script>

<template>
  <Card :title="t('finance.reconciliation')" :subtitle="t('finance.reconciliation_hint')" :flush="Boolean(res.data.value && !res.data.value.ok)">
    <template #actions>
      <AppButton size="sm" :loading="res.loading.value" @click="res.reload()">{{ t('finance.check_again') }}</AppButton>
    </template>
    <StateView v-if="res.error.value" :error="res.error.value" compact @retry="res.reload()" />
    <template v-else-if="res.data.value">
      <p v-if="res.data.value.ok" class="ok">
        <span class="ok__dot" aria-hidden="true" />
        {{ t('finance.reconciliation_ok', { n: res.data.value.wallets_checked, time: fmt.dateTime(res.data.value.checked_at) }) }}
      </p>
      <template v-else>
        <p class="bad" role="alert">
          {{ t('finance.reconciliation_bad', { n: res.data.value.mismatches.length, total: res.data.value.wallets_checked, time: fmt.dateTime(res.data.value.checked_at) }) }}
        </p>
        <DataTable :columns="columns" :rows="res.data.value.mismatches" row-key="wallet_id">
          <template #cell-driver="{ row }">
            <RouterLink :to="{ name: 'driver-detail', params: { id: row.driver.id }, query: { tab: 'wallet' } }" class="jt-mono">{{ row.driver.code }}</RouterLink>
          </template>
        </DataTable>
      </template>
    </template>
  </Card>
</template>

<style scoped>
.ok {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ok__dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--jt-color-green);
}

.bad {
  margin: 0 var(--jt-space-5) var(--jt-space-3);
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}
</style>
