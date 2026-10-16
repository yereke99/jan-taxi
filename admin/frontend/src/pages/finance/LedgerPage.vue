<!-- Wallet journal across all drivers with filters, CSV export and the reconciliation check. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { LEDGER_TYPES, ledgerApi, ledgerQuery } from '../../api/finance'
import { DateRange, DriverPicker, ExportButton, FilterSelect, FiltersBar, Pagination, StateView } from '../../components'
import { usePagedList } from '../../composables/usePagedList'
import { useRealtimeEvents } from '../../composables/useRealtime'
import LedgerTable from './LedgerTable.vue'
import ReconciliationPanel from './ReconciliationPanel.vue'

const { t } = useI18n()

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
        <ExportButton kind="ledger" :query="exportQuery" />
      </template>
    </FiltersBar>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <LedgerTable :rows="list.rows.value" :loading="list.loading.value" />
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </section>
</template>
