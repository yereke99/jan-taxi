<!--
  Referrals («Досыңды шақыр», §23.5) by status: who invited whom (masked),
  the code and where it was opened, the qualifying first ride and why a
  referral was held, capped or rejected. Approvers decide the ones under
  review with a reason.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { referralsApi, type ReferralDecision, type ReferralRow } from '../../api/marketing'
import { AppButton, DataTable, Pagination, ReasonDialog, StateView, StatusBadge, Tabs, toast, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useAuthStore } from '../../stores/auth'
import { REFERRAL_STATUSES } from '../../utils/status'
import OrderLink from './OrderLink.vue'
import PersonCell from './PersonCell.vue'

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()

const list = usePagedList({ status: 'review' }, (f, page, size, signal) => referralsApi.list(f, page, size, signal))
const f = list.filters

const tabs = computed(() => [...REFERRAL_STATUSES.map((s) => ({ value: s as string, label: t(`referral_status.${s}`) })), { value: '', label: t('common.all') }])

const columns = computed<Column<ReferralRow>[]>(() => {
  const cols: Column<ReferralRow>[] = [
    { key: 'attributed_at', label: t('marketing.col_attributed') },
    { key: 'invitee', label: t('marketing.col_invitee') },
    { key: 'referrer', label: t('marketing.col_referrer') },
    { key: 'code', label: t('marketing.col_code') },
    // The status cell also says why a referral was held, capped or rejected, and when it was decided.
    { key: 'status', label: t('common.status') },
    { key: 'qualifying_order_number', label: t('marketing.col_qualifying_order') },
  ]
  if (auth.can('marketing.approve')) cols.push({ key: 'actions', label: '', align: 'right' })
  return cols
})

function sourceText(source: string): string {
  return te(`marketing.source_${source}`, 'kk') ? t(`marketing.source_${source}`) : source
}

function reasonText(reason: string): string {
  return te(`marketing.referral_reason_${reason}`, 'kk') ? t(`marketing.referral_reason_${reason}`) : reason
}

const deciding = ref<{ row: ReferralRow; decision: ReferralDecision } | null>(null)
const decideOpen = computed({
  get: () => deciding.value !== null,
  set: (v) => {
    if (!v) deciding.value = null
  },
})

async function decide(reason: string): Promise<void> {
  const d = deciding.value
  if (!d) return
  const res = await referralsApi.review(d.row.id, d.decision, reason)
  const key = res.outcome === 'referral_rewarded' ? 'marketing.review_done_rewarded' : res.outcome === 'referral_capped' ? 'marketing.review_done_capped' : 'marketing.review_done_rejected'
  toast.success(t(key))
  void list.reload({ silent: true })
}
</script>

<template>
  <section class="jt-page">
    <Tabs v-model="f.status" :tabs="tabs" :aria-label="t('common.status')" />
    <p v-if="f.status === 'review'" class="hint" role="note">{{ t('marketing.review_hint') }}</p>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable :columns="columns" :rows="list.rows.value" row-key="id" :loading="list.loading.value" :empty-text="t('marketing.no_referrals')" :caption="t('nav.referrals')">
        <template #cell-invitee="{ row }">
          <PersonCell :user="row.invitee" />
        </template>
        <template #cell-referrer="{ row }">
          <PersonCell :user="row.referrer" />
        </template>
        <template #cell-code="{ row }">
          <span class="stack">
            <span class="jt-mono">{{ row.code }}</span>
            <span class="jt-muted">{{ t('marketing.col_source') }}: {{ sourceText(row.source) }}</span>
          </span>
        </template>
        <template #cell-attributed_at="{ row }">
          <span :title="fmt.dateTime(row.attributed_at)">{{ fmt.date(row.attributed_at) }}</span>
        </template>
        <template #cell-status="{ row }">
          <span class="stack">
            <StatusBadge kind="referral" :status="row.status" />
            <span v-if="row.reason" class="reason">{{ reasonText(row.reason) }}</span>
            <span v-if="row.decided_at" class="jt-muted">{{ t('marketing.col_decided') }}: {{ fmt.dateTime(row.decided_at) }}</span>
          </span>
        </template>
        <template #cell-qualifying_order_number="{ row }">
          <OrderLink :number="row.qualifying_order_number" />
        </template>
        <template #cell-actions="{ row }">
          <span v-if="row.status === 'review'" class="actions">
            <AppButton size="sm" variant="primary" @click="deciding = { row, decision: 'approve' }">{{ t('marketing.approve') }}</AppButton>
            <AppButton size="sm" variant="destructive" @click="deciding = { row, decision: 'reject' }">{{ t('marketing.reject') }}</AppButton>
          </span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
    <ReasonDialog
      v-model:open="decideOpen"
      :title="deciding?.decision === 'approve' ? t('marketing.approve_title') : t('marketing.reject_title')"
      :message="deciding?.decision === 'approve' ? t('marketing.approve_message') : t('marketing.reject_message')"
      :max-length="300"
      :min-length="3"
      :confirm-label="deciding?.decision === 'approve' ? t('marketing.approve') : t('marketing.reject')"
      :destructive="deciding?.decision === 'reject'"
      :submit="decide"
    />
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

.stack {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 18px;
}

.stack .jt-muted {
  font-size: 12px;
}

.reason {
  font: var(--jt-font-caption);
}

.actions {
  display: inline-flex;
  gap: var(--jt-space-2);
}
</style>
