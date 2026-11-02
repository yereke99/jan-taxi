<!--
  Campaigns (§23.2): filters by status, type and text, budget usage, the
  create drawer (marketing.edit) and, right in the list, the on/off switch of
  the two launch campaigns (approvers only, with a reason).
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { CAMPAIGN_TEMPLATES, campaignsApi, type Campaign, type CampaignDetail } from '../../api/marketing'
import { AppButton, DataTable, FilterSelect, FiltersBar, Pagination, StateView, StatusBadge, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useAuthStore } from '../../stores/auth'
import { CAMPAIGN_STATUSES } from '../../utils/status'
import CampaignForm from './CampaignForm.vue'
import { isLaunchCampaign } from './campaignModel'
import CampaignSwitch from './CampaignSwitch.vue'
import UsageBar from './UsageBar.vue'

const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()
const auth = useAuthStore()

const list = usePagedList({ status: '', template: '', q: '' }, (f, page, size, signal) => campaignsApi.list(f, page, size, signal))
const f = list.filters
const formOpen = ref(false)

const statusOptions = computed(() => CAMPAIGN_STATUSES.map((s) => ({ value: s as string, label: t(`campaign_status.${s}`) })))
const templateOptions = computed(() => CAMPAIGN_TEMPLATES.map((v) => ({ value: v as string, label: t(`marketing.template_${v}`) })))
const hasLaunch = computed(() => list.rows.value.some(isLaunchCampaign))

// The on/off switch sits right after the title so the launch campaigns' state
// is obvious; the title cell also names the type and the version.
const columns = computed<Column<Campaign>[]>(() => [
  { key: 'title', label: t('marketing.campaign') },
  { key: 'switch', label: t('marketing.col_switch'), width: '130px' },
  { key: 'status', label: t('common.status') },
  { key: 'window', label: t('marketing.window') },
  { key: 'usage', label: t('marketing.col_usage') },
])

function windowText(c: Campaign): string {
  if (c.starts_at && c.ends_at) return t('marketing.window_range', { from: fmt.date(c.starts_at), to: fmt.date(c.ends_at) })
  if (c.starts_at) return t('marketing.window_from', { from: fmt.date(c.starts_at) })
  if (c.ends_at) return t('marketing.window_until', { to: fmt.date(c.ends_at) })
  return t('marketing.window_open')
}

function open(c: Campaign): void {
  void router.push({ name: 'campaign-detail', params: { id: c.id } })
}

function onCreated(c: CampaignDetail): void {
  open(c)
}
</script>

<template>
  <section class="jt-page">
    <FiltersBar v-model:search="f.q" :search-placeholder="t('marketing.search_campaigns')" @reset="list.reset">
      <FilterSelect v-model="f.status" :options="statusOptions" :placeholder="t('marketing.all_statuses')" />
      <FilterSelect v-model="f.template" :options="templateOptions" :placeholder="t('marketing.all_templates')" />
      <template #actions>
        <AppButton v-if="auth.can('marketing.edit')" variant="primary" size="sm" @click="formOpen = true">{{ t('marketing.new_campaign') }}</AppButton>
      </template>
    </FiltersBar>
    <p v-if="hasLaunch && !auth.can('marketing.approve')" class="hint" role="note">{{ t('marketing.switch_who') }}</p>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable
        :columns="columns"
        :rows="list.rows.value"
        row-key="id"
        :loading="list.loading.value"
        :empty-text="t('marketing.no_campaigns')"
        clickable
        :caption="t('nav.campaigns')"
        @row-click="open"
      >
        <template #cell-title="{ row }">
          <span class="title" :title="row.key">
            <span class="title__main">
              {{ fmt.name(row.title) }}
              <span v-if="isLaunchCampaign(row)" class="tag tag--dark">{{ t('marketing.launch') }}</span>
              <span v-else-if="row.is_system" class="tag">{{ t('marketing.system') }}</span>
            </span>
            <span class="jt-muted title__key">{{ t(`marketing.template_${row.template}`) }} · {{ t('marketing.version_n', { n: row.version }) }}</span>
          </span>
        </template>
        <template #cell-status="{ row }">
          <span class="badges">
            <StatusBadge kind="campaign" :status="row.status" />
            <span v-if="row.redemptions_frozen" class="tag tag--danger">{{ t('marketing.frozen') }}</span>
          </span>
        </template>
        <template #cell-window="{ row }">
          <span class="jt-muted">{{ windowText(row) }}</span>
        </template>
        <template #cell-usage="{ row }">
          <UsageBar :campaign="row" />
        </template>
        <template #cell-switch="{ row }">
          <CampaignSwitch v-if="isLaunchCampaign(row)" :campaign="row" compact @changed="list.reload({ silent: true })" @stale="list.reload({ silent: true })" />
          <span v-else class="jt-muted">—</span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
    <CampaignForm v-model:open="formOpen" @saved="onCreated" />
  </section>
</template>

<style scoped>
.hint {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  color: var(--jt-color-label-secondary);
}

.title {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  line-height: 18px;
}

.title__main {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}

.title__key {
  font-size: 12px;
}

.badges {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.tag {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 7px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
  font: 600 11px/16px var(--jt-font-family);
}

.tag--dark {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.tag--danger {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}
</style>
