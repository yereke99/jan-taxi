<!-- Places gazetteer (searched before the geocoder): list, create, edit, delete. -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { placesApi, type Place } from '../../api/config'
import { AppButton, DataTable, FilterSelect, FiltersBar, Pagination, StateView, confirmAction, toast, type Column } from '../../components'
import { useCities } from '../../composables/useCities'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { usePagedList } from '../../composables/usePagedList'
import PlaceForm from './PlaceForm.vue'

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const { options: cityOptions, byId } = useCities()

const list = usePagedList({ city_id: '', q: '' }, (f, page, size, signal) => placesApi.list(f, page, size, signal))
const f = list.filters
const formOpen = ref(false)
const editing = ref<Place | null>(null)

const columns = computed<Column<Place>[]>(() => [
  { key: 'name', label: t('common.name') },
  { key: 'aliases', label: t('config.aliases'), value: (r) => r.aliases.join(', ') },
  { key: 'city', label: t('common.city'), value: (r) => (r.city_id ? fmt.name(byId(r.city_id)?.name) : t('config.place_any_city')) },
  { key: 'point', label: t('config.coordinates'), mono: true, value: (r) => `${r.lat.toFixed(5)}, ${r.lon.toFixed(5)}` },
  { key: 'actions', label: '', align: 'right' },
])

function openForm(p: Place | null): void {
  editing.value = p
  formOpen.value = true
}

async function remove(p: Place): Promise<void> {
  const ok = await confirmAction({ title: t('config.place_delete_title', { name: fmt.name(p.name) }), confirmLabel: t('common.delete'), destructive: true })
  if (!ok) return
  try {
    await placesApi.remove(p.id)
    toast.success(t('config.place_deleted'))
    void list.reload({ silent: true })
  } catch (err) {
    errors.report(err)
  }
}
</script>

<template>
  <section class="jt-page">
    <FiltersBar v-model:search="f.q" :search-placeholder="t('config.place_search')" @reset="list.reset">
      <FilterSelect v-model="f.city_id" :options="cityOptions" :placeholder="t('common.all_cities')" />
      <template #actions>
        <AppButton variant="primary" size="sm" @click="openForm(null)">{{ t('config.place_new') }}</AppButton>
      </template>
    </FiltersBar>
    <p class="jt-muted">{{ t('config.places_hint') }}</p>
    <StateView v-if="list.error.value" :error="list.error.value" @retry="list.reload()" />
    <template v-else>
      <DataTable :columns="columns" :rows="list.rows.value" row-key="id" :loading="list.loading.value" :empty-text="t('config.no_places')" clickable @row-click="openForm">
        <template #cell-name="{ row }">
          <span class="two-line">
            <span>{{ fmt.name(row.name) }}<span v-if="!row.is_active" class="jt-muted"> · {{ t('common.inactive') }}</span></span>
            <span class="jt-muted">{{ [row.name.kk, row.name.ru, row.name.en].join(' / ') }}</span>
          </span>
        </template>
        <template #cell-aliases="{ value }">
          <span class="ellipsis" :title="String(value)">{{ value || '—' }}</span>
        </template>
        <template #cell-actions="{ row }">
          <button type="button" class="jt-link-button danger" @click.stop="remove(row)">{{ t('common.delete') }}</button>
        </template>
      </DataTable>
      <Pagination v-model:page="list.page.value" :page-size="list.pageSize" :total="list.total.value" />
    </template>
    <PlaceForm v-model:open="formOpen" :place="editing" @saved="list.reload({ silent: true })" />
  </section>
</template>

<style scoped>
.ellipsis {
  display: inline-block;
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.two-line {
  display: flex;
  flex-direction: column;
  line-height: 18px;
}

.two-line .jt-muted {
  font-size: 12px;
}

.danger {
  color: var(--jt-color-red-text);
}
</style>
