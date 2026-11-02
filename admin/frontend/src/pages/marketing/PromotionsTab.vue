<!-- Promotions applied to orders, by status, with the "under review" toggle (filters in the URL). -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { promotionsApi } from '../../api/marketing'
import { FilterSelect, FiltersBar, Pagination, StateView, ToggleSwitch } from '../../components'
import { usePagedList } from '../../composables/usePagedList'
import { PROMOTION_STATUSES } from '../../utils/status'
import PromotionsTable from './PromotionsTable.vue'

const { t } = useI18n()

const list = usePagedList({ status: '', review: '' }, (f, page, size, signal) => promotionsApi.list(f, page, size, signal))
const f = list.filters

const statusOptions = computed(() => PROMOTION_STATUSES.map((s) => ({ value: s as string, label: t(`promotion_status.${s}`) })))
const review = computed({
  get: () => f.review === 'true',
  set: (v: boolean) => (f.review = v ? 'true' : ''),
})
</script>

<template>
  <div class="jt-page">
    <FiltersBar :show-search="false" @reset="list.reset">
      <FilterSelect v-model="f.status" :options="statusOptions" :placeholder="t('marketing.all_statuses')" />
      <ToggleSwitch v-model="review" :label="t('marketing.under_review')" />
    </FiltersBar>
    <p class="jt-muted note">{{ t('marketing.promotions_note') }}</p>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <PromotionsTable :rows="list.rows.value" :loading="list.loading.value" @changed="list.reload({ silent: true })" />
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </div>
</template>

<style scoped>
.note {
  font: var(--jt-font-small);
}
</style>
