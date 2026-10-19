<!-- Wallet journal across all drivers with filters, CSV export, the reconciliation check and the company bonus (finance.manage). -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { LEDGER_TYPES, ledgerApi, ledgerQuery } from '../../api/finance'
import { AppButton, DateRange, DriverPicker, ExportButton, FilterSelect, FiltersBar, Pagination, StateView } from '../../components'
import { usePagedList } from '../../composables/usePagedList'
import { useRealtimeEvents } from '../../composables/useRealtime'
import { useAuthStore } from '../../stores/auth'
import BonusDialog from '../drivers/BonusDialog.vue'
import LedgerTable from './LedgerTable.vue'
import ReconciliationPanel from './ReconciliationPanel.vue'

const { t } = useI18n()
const auth = useAuthStore()
const bonusOpen = ref(false)

const list = usePagedList({ driver_id: '', type: '', from: '', to: '' }, (f, page, size, signal) => ledgerApi.list(f, page, size, signal))
const f = list.filters

const typeOptions = computed(() => LEDGER_TYPES.map((v) => ({ value: v, label: t(`ledger_type.${v}`) })))

function exportQuery(): Record<string, string | undefined> {
  return ledgerQuery(f)
}

useRealtimeEvents(['wallet.updated'], () => void list.reload({ silent: true }), 2000)
</script>

<template>
  <section class="jt-page">
    <ReconciliationPanel />
    <FiltersBar :show-search="false" @reset="list.reset">
      <DriverPicker v-model="f.driver_id" />
      <FilterSelect v-model="f.type" :options="typeOptions" :placeholder="t('finance.all_types')" />
      <DateRange v-model:from="f.from" v-model:to="f.to" />
      <template #actions>
        <AppButton v-if="auth.can('finance.manage')" @click="bonusOpen = true">{{ t('bonus.grant') }}</AppButton>
        <ExportButton kind="ledger" :query="exportQuery" />
      </template>
    </FiltersBar>
    <BonusDialog v-if="auth.can('finance.manage')" v-model:open="bonusOpen" @done="list.reload({ silent: true })" />
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <LedgerTable :rows="list.rows.value" :loading="list.loading.value" />
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </section>
</template>
