<!-- Driver wallet: balances, the wallet journal and manual adjustments (finance.manage). -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminDriverDetail } from '../../api/drivers'
import { LEDGER_TYPES, ledgerApi, type WalletAdjustmentResult } from '../../api/finance'
import { AppButton, Card, FilterSelect, Pagination, StateView, toast } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { DEFAULT_PAGE_SIZE } from '../../services/api'
import { useAuthStore } from '../../stores/auth'
import LedgerTable from '../finance/LedgerTable.vue'
import AdjustmentDialog from './AdjustmentDialog.vue'

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

function onAdjusted(res: WalletAdjustmentResult): void {
  toast.success(t('drivers.adjusted_ok', { amount: fmt.signedMoney(res.entry.balance_delta) }))
  page.value = 1
  void ledger.reload()
  emit('changed')
}

useRealtimeEvents(['wallet.updated'], (ev) => {
  if (ev.data.driver_id === props.driver.id) {
    void ledger.reload({ silent: true })
    emit('changed')
  }
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
