<!--
  Segments (§23.7): saved audiences of passengers or drivers with typed
  conditions and live counts (matching, reachable, suppressed). marketing.edit
  creates, edits and archives them; archived ones stay for history.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { segmentsApi, type Segment } from '../../api/marketing'
import { AppButton, DataTable, StateView, Tabs, toast, type Column } from '../../components'
import { useCities } from '../../composables/useCities'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'
import SegmentForm from './SegmentForm.vue'
import { filtersSummary } from './segmentModel'

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const auth = useAuthStore()
const cities = useCities()
const res = useResource((signal) => segmentsApi.list(signal))
void res.reload()

const view = ref<'active' | 'archived'>('active')
const viewTabs = computed(() => [
  { value: 'active' as const, label: t('common.active') },
  { value: 'archived' as const, label: t('marketing.archived') },
])
const rows = computed(() => (res.data.value?.items ?? []).filter((s) => (view.value === 'archived') === (s.archived_at !== null)))
const canEdit = computed(() => auth.can('marketing.edit'))

const formOpen = ref(false)
const editing = ref<Segment | null>(null)

const columns = computed<Column<Segment>[]>(() => {
  const cols: Column<Segment>[] = [
    // The name cell also shows the audience; the reach cell the matching and suppressed counts.
    { key: 'name', label: t('common.name') },
    { key: 'conditions', label: t('marketing.col_conditions'), value: (r) => filtersSummary(r.audience, r.filters, t, cityName) },
    { key: 'reachable', label: t('marketing.count_reachable'), align: 'right', value: (r) => r.count?.reachable ?? null },
    { key: 'updated_at', label: t('common.updated'), format: (v) => fmt.dateTime(v as string) },
  ]
  if (canEdit.value) cols.push({ key: 'actions', label: '', align: 'right' })
  return cols
})

function cityName(id: string): string {
  const c = cities.byId(id)
  return c ? fmt.name(c.name) : id.slice(0, 8)
}

function openForm(s: Segment | null): void {
  editing.value = s
  formOpen.value = true
}

async function setArchived(s: Segment, archived: boolean): Promise<void> {
  try {
    await segmentsApi.update(s.id, { archived })
    toast.success(archived ? t('marketing.segment_archived') : t('marketing.segment_restored'))
    await res.reload({ silent: true })
  } catch (err) {
    errors.report(err)
  }
}
</script>

<template>
  <section class="jt-page">
    <div class="toolbar">
      <Tabs v-model="view" :tabs="viewTabs" :aria-label="t('nav.segments')" />
      <p class="jt-muted toolbar__hint">{{ t('marketing.count_hint') }}</p>
      <AppButton v-if="canEdit" variant="primary" size="sm" @click="openForm(null)">{{ t('marketing.new_segment') }}</AppButton>
    </div>
    <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <DataTable
      v-else
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="res.loading.value"
      :empty-text="t('marketing.no_segments')"
      :clickable="canEdit && view === 'active'"
      @row-click="openForm"
    >
      <template #cell-name="{ row }">
        <span class="stack">
          <strong>{{ row.name }}</strong>
          <span class="jt-muted">{{ t(`marketing.audience_${row.audience}`) }}</span>
        </span>
      </template>
      <template #cell-reachable="{ row }">
        <span v-if="row.count" class="stack stack--end">
          <span class="jt-num">{{ fmt.count(row.count.reachable) }}</span>
          <span class="jt-muted">{{ t('marketing.count_matching') }} {{ fmt.count(row.count.matching) }} · {{ t('marketing.count_suppressed') }} {{ fmt.count(row.count.suppressed) }}</span>
        </span>
        <span v-else class="jt-muted">—</span>
      </template>
      <template #cell-conditions="{ row }">
        <span class="conditions" :title="filtersSummary(row.audience, row.filters, t, cityName)">{{ filtersSummary(row.audience, row.filters, t, cityName) }}</span>
      </template>
      <template #cell-actions="{ row }">
        <span class="actions" @click.stop @keydown.stop>
          <AppButton v-if="!row.archived_at" size="sm" @click="openForm(row)">{{ t('common.edit') }}</AppButton>
          <AppButton v-if="!row.archived_at" size="sm" variant="destructive" @click="setArchived(row, true)">{{ t('marketing.action_archive') }}</AppButton>
          <AppButton v-else size="sm" @click="setArchived(row, false)">{{ t('marketing.restore') }}</AppButton>
        </span>
      </template>
    </DataTable>
    <SegmentForm v-model:open="formOpen" :segment="editing" @saved="res.reload({ silent: true })" />
  </section>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
}

.toolbar__hint {
  flex: 1;
  font: var(--jt-font-small);
}

.stack {
  display: inline-flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 18px;
}

.stack--end {
  align-items: flex-end;
}

.stack .jt-muted {
  font-size: 12px;
}

.conditions {
  display: inline-block;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.actions {
  display: inline-flex;
  gap: var(--jt-space-2);
}
</style>
