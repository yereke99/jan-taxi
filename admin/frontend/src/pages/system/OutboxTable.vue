<!-- Telegram notification outbox: status filters, attempts, last error and retry for dead/cancelled rows. -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NOTIFICATION_TEMPLATE_KEYS, notificationsApi, type OutboxItem } from '../../api/system'
import { AppButton, DataTable, Drawer, FilterSelect, FiltersBar, KeyValueList, Pagination, StateView, StatusBadge, toast, type Column } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { NOTIFICATION_STATUSES } from '../../utils/status'

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()

const list = usePagedList({ status: '', bot: '', template: '' }, (f, page, size, signal) => notificationsApi.list(f, page, size, signal))
const f = list.filters
const retrying = ref<string | null>(null)
const selected = ref<OutboxItem | null>(null)
const detailOpen = computed({
  get: () => selected.value !== null,
  set: (v) => {
    if (!v) selected.value = null
  },
})

const statusOptions = computed(() => NOTIFICATION_STATUSES.map((s) => ({ value: s, label: t(`notification_status.${s}`) })))
const botOptions = computed(() => (['passenger', 'driver'] as const).map((b) => ({ value: b, label: t(`bots.${b}`) })))
const templateOptions = computed(() => NOTIFICATION_TEMPLATE_KEYS.map((k) => ({ value: k as string, label: k as string })))

const columns = computed<Column<OutboxItem>[]>(() => [
  { key: 'created_at', label: t('common.created'), format: (v) => fmt.dateTime(v as string) },
  { key: 'template', label: t('system.template') },
  { key: 'recipient', label: t('system.recipient') },
  { key: 'status', label: t('common.status') },
  { key: 'last_error', label: t('system.delivery') },
  { key: 'actions', label: '', align: 'right' },
])

async function retry(item: OutboxItem): Promise<void> {
  if (retrying.value) return
  retrying.value = item.id
  try {
    const updated = await notificationsApi.retry(item.id)
    if (selected.value?.id === item.id) selected.value = updated
    toast.success(t('system.retry_queued'))
    void list.reload({ silent: true })
  } catch (err) {
    errors.report(err)
  } finally {
    retrying.value = null
  }
}

const detailItems = computed(() => {
  const x = selected.value
  if (!x) return []
  const last = x.attempts_summary.last
  return [
    { key: 'id', label: 'ID', value: x.id, mono: true },
    { key: 'dedupe', label: t('system.dedupe_key'), value: x.dedupe_key, mono: true },
    { key: 'chat', label: t('system.chat_id'), value: String(x.chat_id), mono: true },
    { key: 'user', label: t('system.recipient'), value: `${x.user_name}${x.username ? ` (@${x.username})` : ''}` },
    { key: 'sent', label: t('system.sent_at'), value: fmt.dateTime(x.sent_at) },
    { key: 'msg', label: t('system.telegram_message_id'), value: x.telegram_message_id === null ? null : String(x.telegram_message_id), mono: true },
    { key: 'summary', label: t('system.attempts'), value: t('system.attempts_summary', { total: x.attempts_summary.total, retry: x.attempts_summary.retry, fatal: x.attempts_summary.fatal }) },
    { key: 'last', label: t('system.last_attempt'), value: last ? `${fmt.dateTime(last.finished_at)} · ${last.outcome}${last.http_status ? ` · HTTP ${last.http_status}` : ''}` : null },
    { key: 'error', label: t('system.last_error'), value: x.last_error },
  ]
})
</script>

<template>
  <div class="jt-page">
    <FiltersBar :show-search="false" @reset="list.reset">
      <FilterSelect v-model="f.status" :options="statusOptions" :placeholder="t('orders.all_statuses')" />
      <FilterSelect v-model="f.bot" :options="botOptions" :placeholder="t('system.all_bots')" />
      <FilterSelect v-model="f.template" :options="templateOptions" :placeholder="t('system.all_templates')" />
    </FiltersBar>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable :columns="columns" :rows="list.rows.value" row-key="id" :loading="list.loading.value" :empty-text="t('system.no_notifications')" clickable @row-click="(r) => (selected = r)">
        <template #cell-template="{ row }">
          <span class="stack">
            <span class="jt-mono">{{ row.template }}</span>
            <span class="jt-muted">{{ t(`bots.${row.bot}`) }}</span>
          </span>
        </template>
        <template #cell-recipient="{ row }">
          <span class="stack">
            <span>{{ row.user_name || '—' }}</span>
            <span class="jt-muted">{{ row.username ? `@${row.username}` : row.chat_id }}</span>
          </span>
        </template>
        <template #cell-status="{ row }">
          <span class="stack">
            <StatusBadge kind="notification" :status="row.status" />
            <span class="jt-muted jt-num">{{ t('system.attempts_n', { n: row.attempts, max: row.max_attempts }) }}</span>
          </span>
        </template>
        <template #cell-last_error="{ row }">
          <span class="stack">
            <span v-if="row.status === 'pending'" class="jt-muted">{{ t('system.next_attempt_at', { time: fmt.relative(row.next_attempt_at) }) }}</span>
            <span v-else-if="row.sent_at" class="jt-muted">{{ t('system.sent_at_value', { time: fmt.dateTime(row.sent_at) }) }}</span>
            <span v-if="row.last_error" class="err" :title="row.last_error">{{ row.last_error }}</span>
          </span>
        </template>
        <template #cell-actions="{ row }">
          <AppButton v-if="row.status === 'dead' || row.status === 'cancelled'" size="sm" :loading="retrying === row.id" @click.stop="retry(row)">{{ t('common.retry') }}</AppButton>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>

    <Drawer v-model:open="detailOpen" :title="selected?.template ?? ''" width="560px">
      <template v-if="selected">
        <KeyValueList :items="detailItems" />
        <h3 class="jt-section-title params-title">{{ t('system.params') }}</h3>
        <pre class="params">{{ JSON.stringify(selected.params ?? {}, null, 2) }}</pre>
      </template>
      <template #footer>
        <AppButton v-if="selected && (selected.status === 'dead' || selected.status === 'cancelled')" variant="primary" :loading="retrying === selected.id" @click="retry(selected)">
          {{ t('common.retry') }}
        </AppButton>
      </template>
    </Drawer>
  </div>
</template>

<style scoped>
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

.err {
  display: inline-block;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
  color: var(--jt-color-red-text);
  font-size: 12px;
}

.params-title {
  margin: var(--jt-space-4) 0 var(--jt-space-2);
}

.params {
  margin: 0;
  padding: 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
  font: 12px/18px var(--jt-font-mono);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
