<!-- Audit log: who changed what and when, with a before/after diff of every entry. -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RouteLocationRaw } from 'vue-router'
import { adminsApi, auditApi, type AuditRecord } from '../../api/system'
import { DataTable, DateRange, DebouncedInput, Drawer, FilterSelect, FiltersBar, JsonDiff, KeyValueList, Pagination, StateView, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'

const ENTITY_TYPES = ['order', 'dispute', 'driver', 'topup', 'city', 'service_zone', 'place', 'tariff', 'app_setting', 'notification', 'notification_template', 'admin', 'report', 'file']

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()

const list = usePagedList({ admin_id: '', entity_type: '', entity_id: '', action: '', from: '', to: '' }, (f, page, size, signal) =>
  auditApi.list(f, page, size, signal),
)
const f = list.filters
const selected = ref<AuditRecord | null>(null)
const detailOpen = computed({
  get: () => selected.value !== null,
  set: (v) => {
    if (!v) selected.value = null
  },
})

// Administrators for the filter (only listable with admins.manage; others filter by clicking a name).
const admins = useResource((signal) => adminsApi.list(1, 100, signal))
if (auth.can('admins.manage')) void admins.reload()
const adminOptions = computed(() => (admins.data.value?.items ?? []).map((a) => ({ value: a.id, label: a.full_name || a.email })))
const entityOptions = computed(() => ENTITY_TYPES.map((e) => ({ value: e, label: entityLabel(e) })))

function entityLabel(e: string): string {
  return te(`system.entity_${e}`, 'kk') ? t(`system.entity_${e}`) : e
}

const columns = computed<Column<AuditRecord>[]>(() => [
  { key: 'created_at', label: t('system.time'), width: '150px', format: (v) => fmt.dateTime(v as string) },
  { key: 'admin', label: t('system.admin') },
  { key: 'action', label: t('system.action'), mono: true },
  { key: 'entity_type', label: t('system.entity'), format: (v) => entityLabel(String(v)) },
  { key: 'entity_id', label: t('system.entity_id'), mono: true },
  { key: 'ip', label: 'IP', mono: true },
])

function filterAdmin(id: string | null): void {
  if (id) f.admin_id = id
}

/** Link to the record behind an entry, when the panel has a page for it. */
function entityLink(r: AuditRecord): RouteLocationRaw | null {
  if (!r.entity_id) return null
  if (r.entity_type === 'order' && auth.can('orders.view')) return { name: 'order-detail', params: { id: r.entity_id } }
  if (r.entity_type === 'driver' && auth.can('drivers.view')) return { name: 'driver-detail', params: { id: r.entity_id } }
  if (r.entity_type === 'topup' && auth.can('finance.view')) return { name: 'topup-detail', params: { id: r.entity_id } }
  return null
}

const detailItems = computed(() => {
  const r = selected.value
  if (!r) return []
  return [
    { key: 'time', label: t('system.time'), value: fmt.dateTime(r.created_at) },
    { key: 'admin', label: t('system.admin'), value: r.admin_name ? `${r.admin_name} · ${r.admin_email ?? ''}` : r.admin_email },
    { key: 'action', label: t('system.action'), value: r.action, mono: true },
    { key: 'entity', label: t('system.entity'), value: `${entityLabel(r.entity_type)} · ${r.entity_id ?? '—'}` },
    { key: 'ip', label: 'IP', value: r.ip, mono: true },
    { key: 'ua', label: t('system.user_agent'), value: r.user_agent },
  ]
})
</script>

<template>
  <section class="jt-page">
    <FiltersBar :show-search="false" @reset="list.reset">
      <FilterSelect v-if="auth.can('admins.manage')" v-model="f.admin_id" :options="adminOptions" :placeholder="t('system.all_admins')" />
      <span v-else-if="f.admin_id" class="chip">
        {{ t('system.admin') }}: <span class="jt-mono">{{ f.admin_id.slice(0, 8) }}</span>
        <button type="button" class="jt-link-button" @click="f.admin_id = ''">×</button>
      </span>
      <FilterSelect v-model="f.entity_type" :options="entityOptions" :placeholder="t('system.all_entities')" />
      <DebouncedInput v-model="f.entity_id" :placeholder="t('system.entity_id')" />
      <DebouncedInput v-model="f.action" :placeholder="t('system.action_placeholder')" />
      <DateRange v-model:from="f.from" v-model:to="f.to" />
    </FiltersBar>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable :columns="columns" :rows="list.rows.value" row-key="id" :loading="list.loading.value" :empty-text="t('system.no_audit')" clickable @row-click="(r) => (selected = r)">
        <template #cell-admin="{ row }">
          <button v-if="row.admin_id" type="button" class="jt-link-button" :title="t('system.filter_by_admin')" @click.stop="filterAdmin(row.admin_id)">
            {{ row.admin_name || row.admin_email }}
          </button>
          <span v-else class="jt-muted">{{ t('actor.system') }}</span>
        </template>
        <template #cell-entity_id="{ row }">
          <RouterLink v-if="entityLink(row)" :to="entityLink(row)!" class="jt-mono" @click.stop>{{ row.entity_id?.slice(0, 8) }}</RouterLink>
          <span v-else class="jt-mono">{{ row.entity_id ? row.entity_id.slice(0, 18) : '—' }}</span>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>

    <Drawer v-model:open="detailOpen" :title="selected?.action ?? ''" width="720px">
      <template v-if="selected">
        <KeyValueList :items="detailItems" />
        <h3 class="jt-section-title diff-title">{{ t('system.changes') }}</h3>
        <JsonDiff :before="selected.before" :after="selected.after" />
      </template>
    </Drawer>
  </section>
</template>

<style scoped>
.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: var(--jt-control-height);
  padding: 0 10px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.diff-title {
  margin: var(--jt-space-5) 0 var(--jt-space-2);
}
</style>
