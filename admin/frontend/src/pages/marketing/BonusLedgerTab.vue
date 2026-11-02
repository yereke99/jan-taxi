<!-- The passenger bonus ledger across passengers, filtered by entry type (filters in the URL). -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { BONUS_ENTRY_TYPES, bonusApi } from '../../api/marketing'
import { FilterSelect, FiltersBar, Pagination, StateView } from '../../components'
import { usePagedList } from '../../composables/usePagedList'
import BonusLedgerTable from './BonusLedgerTable.vue'

const { t } = useI18n()

const list = usePagedList({ type: '' }, (f, page, size, signal) => bonusApi.ledger(f, page, size, signal))
const f = list.filters

const typeOptions = computed(() => BONUS_ENTRY_TYPES.map((v) => ({ value: v as string, label: t(`marketing.bonus_type_${v}`) })))
</script>

<template>
  <div class="jt-page">
    <FiltersBar :show-search="false" @reset="list.reset">
      <FilterSelect v-model="f.type" :options="typeOptions" :placeholder="t('finance.all_types')" />
    </FiltersBar>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <BonusLedgerTable :rows="list.rows.value" :loading="list.loading.value" />
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </div>
</template>
