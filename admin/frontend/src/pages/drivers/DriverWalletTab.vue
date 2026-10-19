<!-- Driver wallet: balances, bonuses (welcome and company), the wallet journal and manual adjustments (finance.manage). -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminDriverDetail, DriverBonus } from '../../api/drivers'
import { LEDGER_TYPES, ledgerApi, type WalletAdjustmentResult } from '../../api/finance'
import { AppButton, Card, DataTable, FilterSelect, Pagination, StateView, toast, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { DEFAULT_PAGE_SIZE } from '../../services/api'
import { useAuthStore } from '../../stores/auth'
import LedgerTable from '../finance/LedgerTable.vue'
import AdjustmentDialog from './AdjustmentDialog.vue'
import { welcomeState } from './bonus'

const props = defineProps<{ driver: AdminDriverDetail }>()
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const page = ref(1)
const type = ref('')
const adjustOpen = ref(false)

const ledger = useResource((signal) => ledgerApi.driverLedger(props.driver.id, type.value, page.value, DEFAULT_PAGE_SIZE, signal))
watch([page, type], () => void ledger.reload(), { immediate: true })
watch(type, () => (page.value = 1))

const typeOptions = computed(() => LEDGER_TYPES.map((v) => ({ value: v, label: t(`ledger_type.${v}`) })))
const w = computed(() => props.driver.wallet)

const welcome = computed(() => welcomeState(props.driver))
const bonusTotal = computed(() => props.driver.bonuses.reduce((sum, b) => sum + b.amount, 0))

const bonusColumns = computed<Column<DriverBonus>[]>(() => [
  { key: 'created_at', label: t('common.created'), width: '150px', format: (v) => fmt.dateTime(v as string) },
  { key: 'kind', label: t('bonus.kind'), format: (v) => t(`bonus.kind_${String(v)}`) },
  { key: 'amount', label: t('bonus.amount_short'), align: 'right' },
  { key: 'comment', label: t('common.comment'), format: (v) => (v ? String(v) : '—') },
  { key: 'admin_name', label: t('bonus.given_by'), format: (v) => (v ? String(v) : '—') },
])

function onAdjusted(res: WalletAdjustmentResult): void {
  toast.success(t('drivers.adjusted_ok', { amount: fmt.signedMoney(res.entry.balance_delta) }))
  page.value = 1
  void ledger.reload()
  emit('changed')
}

// A bonus given from the header changes the wallet: show its journal row.
watch(
  () => props.driver.wallet?.updated_at,
  (now, before) => {
    if (before && now !== before) void ledger.reload({ silent: true })
  },
)

// The detail page reloads the driver (balances, bonuses) on the same event.
useRealtimeEvents(['wallet.updated'], (ev) => {
  if (ev.data.driver_id === props.driver.id) void ledger.reload({ silent: true })
})
</script>

<template>
  <div class="wallet">
    <div class="wallet__tiles">
      <div class="tile">
        <p class="tile__label">{{ t('finance.balance') }}</p>
        <p class="tile__value jt-num">{{ fmt.money(w?.balance) }}</p>
      </div>
      <div class="tile">
        <p class="tile__label">{{ t('finance.reserved') }}</p>
        <p class="tile__value jt-num">{{ fmt.money(w?.reserved) }}</p>
      </div>
      <div class="tile">
        <p class="tile__label">{{ t('finance.available') }}</p>
        <p class="tile__value jt-num">{{ fmt.money(w?.available) }}</p>
        <p v-if="w" class="tile__hint" :class="{ 'jt-negative': w.available < w.min_online_balance }">
          {{ t('drivers.min_online_balance', { amount: fmt.money(w.min_online_balance) }) }}
        </p>
      </div>
      <div v-if="auth.can('finance.manage')" class="tile tile--action">
        <AppButton variant="primary" @click="adjustOpen = true">{{ t('drivers.adjust') }}</AppButton>
        <p class="tile__hint">{{ t('drivers.adjust_hint') }}</p>
      </div>
    </div>

    <Card :title="t('bonus.history_title')" :subtitle="driver.bonuses.length ? t('bonus.history_total', { amount: fmt.money(bonusTotal), n: driver.bonuses.length }) : ''" flush>
      <div class="welcome" :class="`welcome--${welcome.kind}`">
        <span class="welcome__label">{{ t('bonus.welcome_title') }}</span>
        <span v-if="welcome.kind === 'granted'">
          <strong class="jt-num jt-positive">+{{ fmt.money(welcome.amount) }}</strong>
          <span class="jt-muted"> · {{ fmt.dateTime(welcome.at) }}</span>
        </span>
        <span v-else-if="welcome.kind === 'awaiting_approval'" class="jt-muted">{{ t('bonus.welcome_awaiting') }}</span>
        <span v-else class="jt-muted">{{ t('bonus.welcome_not_granted') }}</span>
      </div>
      <DataTable :columns="bonusColumns" :rows="driver.bonuses" row-key="ledger_entry_id" :empty-text="t('bonus.history_empty')">
        <template #cell-amount="{ row }">
          <span class="jt-mono jt-positive">{{ fmt.signedMoney(row.amount) }}</span>
        </template>
        <template #cell-kind="{ row }">
          <span class="kind" :class="`kind--${row.kind}`">{{ t(`bonus.kind_${row.kind}`) }}</span>
        </template>
      </DataTable>
    </Card>

    <Card :title="t('drivers.ledger')" flush>
      <template #actions>
        <FilterSelect v-model="type" :options="typeOptions" :placeholder="t('finance.all_types')" />
      </template>
      <StateView v-if="ledger.error.value" :error="ledger.error.value" compact @retry="ledger.reload()" />
      <LedgerTable v-else :rows="ledger.data.value?.items ?? []" :loading="ledger.loading.value" :show-driver="false" />
    </Card>
    <Pagination v-model:page="page" :page-size="DEFAULT_PAGE_SIZE" :total="ledger.data.value?.total ?? 0" />

    <AdjustmentDialog v-model:open="adjustOpen" :driver-id="driver.id" :driver-code="driver.code" :wallet="w" @done="onAdjusted" />
  </div>
</template>

<style scoped>
.welcome {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--jt-space-3);
  margin: 0 var(--jt-space-5) var(--jt-space-3);
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.welcome__label {
  font: var(--jt-font-body-strong);
}

.kind {
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font: 500 12px/20px var(--jt-font-family);
}

.kind--welcome {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.wallet {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
}

.wallet__tiles {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--jt-space-3);
}

.tile {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: var(--jt-space-4) var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.tile--action {
  align-items: flex-start;
  justify-content: center;
}

.tile__label {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.tile__value {
  font: var(--jt-font-title);
}

.tile__hint {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}
</style>
