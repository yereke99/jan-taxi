<!--
  Broadcasts (§23.7) by status with their delivery counts. "Sent" means
  accepted by Telegram, not read. marketing.broadcast prepares drafts; sending
  starts on the broadcast's page.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { broadcastsApi, type Broadcast, type BroadcastDetail } from '../../api/marketing'
import { AppButton, DataTable, Pagination, StateView, StatusBadge, Tabs, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useResync } from '../../composables/useRealtime'
import { useAuthStore } from '../../stores/auth'
import { BROADCAST_STATUSES } from '../../utils/status'
import BroadcastForm from './BroadcastForm.vue'
import { deliveryStats } from './broadcastModel'

const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()
const auth = useAuthStore()

const list = usePagedList({ status: '' }, (f, page, size, signal) => broadcastsApi.list(f, page, size, signal))
const f = list.filters
const formOpen = ref(false)

const tabs = computed(() => [{ value: '', label: t('common.all') }, ...BROADCAST_STATUSES.map((s) => ({ value: s as string, label: t(`broadcast_status.${s}`) }))])

const columns = computed<Column<Broadcast>[]>(() => [
  { key: 'title', label: t('marketing.b_title') },
  { key: 'audience', label: t('marketing.audience'), format: (v) => t(`marketing.audience_${String(v)}`) },
  { key: 'status', label: t('common.status') },
  { key: 'recipient_count', label: t('marketing.col_recipients'), align: 'right', format: (v) => fmt.count(v as number | null) },
  { key: 'delivery', label: t('marketing.col_delivery') },
  { key: 'when', label: t('marketing.col_when'), value: (r) => r.started_at ?? r.scheduled_at ?? r.created_at, format: (v) => fmt.dateTime(v as string) },
])

function deliveryText(b: Broadcast): string {
  const s = deliveryStats(b.stats).live
  // Nothing is delivered before sending starts (test sends are counted apart).
  if ((b.status === 'draft' || b.status === 'scheduled') && Object.keys(s).length === 0) return '—'
  return t('marketing.delivery_summary', { sent: fmt.count(s.sent ?? 0), failed: fmt.count((s.failed ?? 0) + (s.blocked ?? 0)), queued: fmt.count((s.queued ?? 0) + (s.sending ?? 0)) })
}

function onCreated(b: BroadcastDetail): void {
  void router.push({ name: 'broadcast-detail', params: { id: b.id } })
}

useResync(() => void list.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <div class="toolbar">
      <Tabs v-model="f.status" :tabs="tabs" :aria-label="t('common.status')" />
      <AppButton v-if="auth.can('marketing.broadcast')" variant="primary" size="sm" @click="formOpen = true">{{ t('marketing.new_broadcast') }}</AppButton>
    </div>
    <p class="jt-muted note">{{ t('marketing.sent_means') }}</p>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable
        :columns="columns"
        :rows="list.rows.value"
        row-key="id"
        :loading="list.loading.value"
        :empty-text="t('marketing.no_broadcasts')"
        clickable
        :caption="t('nav.broadcasts')"
        @row-click="(r) => router.push({ name: 'broadcast-detail', params: { id: r.id } })"
      >
        <template #cell-title="{ row }">
          <strong>{{ row.title }}</strong>
        </template>
        <template #cell-status="{ row }">
          <StatusBadge kind="broadcast" :status="row.status" />
        </template>
        <template #cell-delivery="{ row }">
          <span class="jt-muted">{{ deliveryText(row) }}</span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
    <BroadcastForm v-model:open="formOpen" @saved="onCreated" />
  </section>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
}

.note {
  font: var(--jt-font-small);
}
</style>
