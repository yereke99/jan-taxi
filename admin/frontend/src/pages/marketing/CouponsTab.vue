<!-- Coupons across passengers, filtered by campaign and status (filters in the URL). -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { campaignsApi, couponsApi } from '../../api/marketing'
import { FilterSelect, FiltersBar, Pagination, StateView } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useResource } from '../../composables/useResource'
import { COUPON_STATUSES } from '../../utils/status'
import CouponsTable from './CouponsTable.vue'

const { t } = useI18n()
const fmt = useFormat()

const list = usePagedList({ campaign_id: '', status: '' }, (f, page, size, signal) => couponsApi.list(f, page, size, signal))
const f = list.filters

// Coupon campaigns for the filter (a deployment has a handful).
const campaigns = useResource((signal) => campaignsApi.list({ template: 'first_ride_coupon' }, 1, 100, signal))
void campaigns.reload()

const campaignOptions = computed(() => (campaigns.data.value?.items ?? []).map((c) => ({ value: c.id, label: `${fmt.name(c.title)} · ${c.key}` })))
const statusOptions = computed(() => COUPON_STATUSES.map((s) => ({ value: s as string, label: t(`coupon_status.${s}`) })))
</script>

<template>
  <div class="jt-page">
    <FiltersBar :show-search="false" @reset="list.reset">
      <FilterSelect v-model="f.campaign_id" :options="campaignOptions" :placeholder="t('marketing.all_campaigns')" />
      <FilterSelect v-model="f.status" :options="statusOptions" :placeholder="t('marketing.all_statuses')" />
    </FiltersBar>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <CouponsTable :rows="list.rows.value" :loading="list.loading.value" @changed="list.reload({ silent: true })" />
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
  </div>
</template>
