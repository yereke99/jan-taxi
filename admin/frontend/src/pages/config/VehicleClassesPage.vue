<!--
  Vehicle classes (body types, §21): what each class carries, its seats and
  cargo space and which orders its drivers may take. Drivers pick a class at
  registration, passengers choose one after the route, tariffs are per class.
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { vehicleClassesApi, type VehicleClass } from '../../api/vehicleClasses'
import { AppButton, DataTable, StateView, VehicleClassIcon, type Column } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { setVehicleClasses } from '../../composables/useVehicleClasses'
import VehicleClassForm from './VehicleClassForm.vue'

const { t } = useI18n()
const fmt = useFormat()

const res = useResource(async (signal) => {
  const r = await vehicleClassesApi.list(signal)
  setVehicleClasses(r.items)
  return r
})
void res.reload()

const items = computed<VehicleClass[]>(() => res.data.value?.items ?? [])
const formOpen = ref(false)
const editing = ref<VehicleClass | null>(null)

function nameOf(code: string): string {
  const c = items.value.find((x) => x.code === code)
  return c ? fmt.name(c.name) : code
}

const columns = computed<Column<VehicleClass>[]>(() => [
  { key: 'class', label: t('classes.class') },
  { key: 'examples', label: t('classes.examples'), format: (v) => (v ? String(v) : '—') },
  { key: 'seats', label: t('classes.seats'), align: 'right' },
  { key: 'cargo_volume_l_default', label: t('classes.cargo'), align: 'right', format: (v) => t('classes.liters', { n: fmt.count(v as number) }) },
  { key: 'carries', label: t('classes.carries') },
  { key: 'serves', label: t('classes.serves_short') },
  { key: 'sort', label: t('classes.sort'), align: 'right', mono: true },
  { key: 'is_active', label: t('common.status') },
])

function openNew(): void {
  editing.value = null
  formOpen.value = true
}

function openEdit(row: VehicleClass): void {
  editing.value = row
  formOpen.value = true
}

function onSaved(): void {
  void res.reload({ silent: true })
}
</script>

<template>
  <section class="jt-page">
    <div class="toolbar">
      <p class="jt-muted">{{ t('classes.hint') }}</p>
      <AppButton variant="primary" @click="openNew">{{ t('classes.new') }}</AppButton>
    </div>

    <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <DataTable v-else :columns="columns" :rows="items" row-key="code" :empty-text="t('classes.empty')" clickable :caption="t('nav.vehicle_classes')" @row-click="openEdit">
      <template #cell-class="{ row }">
        <span class="cls" :class="{ 'is-off': !row.is_active }">
          <VehicleClassIcon :icon="row.icon" :size="20" />
          <span class="two-line">
            <span>{{ fmt.name(row.name) }}</span>
            <span class="jt-mono jt-muted">{{ row.code }}</span>
          </span>
        </span>
      </template>
      <template #cell-seats="{ row }">
        <span class="jt-num">{{ t('classes.seats_value', { def: row.seats_default, max: row.seats_max }) }}</span>
      </template>
      <template #cell-carries="{ row }">
        <span class="tags">
          <span v-if="row.passenger_capable" class="tag">{{ t('classes.tag_passengers') }}</span>
          <span v-if="row.cargo_capable" class="tag">{{ t('classes.tag_cargo') }}</span>
        </span>
      </template>
      <template #cell-serves="{ row }">
        <span class="serves">{{ row.serves.map(nameOf).join(', ') }}</span>
      </template>
      <template #cell-is_active="{ row }">
        <span class="state" :class="{ 'is-on': row.is_active }">
          <span class="state__dot" aria-hidden="true" />{{ row.is_active ? t('common.active') : t('common.inactive') }}
        </span>
      </template>
    </DataTable>

    <VehicleClassForm v-model:open="formOpen" :vehicle-class="editing" :all="items" @saved="onSaved" />
  </section>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
}

.cls {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.cls.is-off {
  opacity: 0.55;
}

.two-line {
  display: flex;
  flex-direction: column;
  line-height: 18px;
}

.two-line .jt-muted {
  font-size: 12px;
}

.tags {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px;
}

.tag {
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font: 500 12px/20px var(--jt-font-family);
}

.serves {
  display: inline-block;
  max-width: 220px;
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.state__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--jt-color-fill-tertiary);
}

.state.is-on {
  color: var(--jt-color-label);
}

.state.is-on .state__dot {
  background: var(--jt-color-green);
}
</style>
