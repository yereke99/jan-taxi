<!--
  Tariffs per city × category × vehicle class: an overview of every class's
  current fares, the current version per category for the chosen class,
  version history, and new versions (config.manage).
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { tariffsApi, type Tariff } from '../../api/config'
import { DEFAULT_VEHICLE_CLASS, classServesCategory } from '../../api/vehicleClasses'
import { AppButton, Card, DataTable, KeyValueList, SelectInput, StateView, Tabs, VehicleClassIcon, type Column, type KeyValueItem } from '../../components'
import { useCities } from '../../composables/useCities'
import { useFormat } from '../../composables/useFormat'
import { useListQuery } from '../../composables/useListQuery'
import { useResource } from '../../composables/useResource'
import { useVehicleClasses } from '../../composables/useVehicleClasses'
import { useAuthStore } from '../../stores/auth'
import { CATEGORIES, type Category } from '../../utils/status'
import TariffForm from './TariffForm.vue'

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const vc = useVehicleClasses()
const { cities, options: cityOptions } = useCities()
const q = useListQuery({ city_id: '', category: 'taxi', vehicle_class: DEFAULT_VEHICLE_CLASS })
const f = q.filters

watch(
  cities,
  (list) => {
    if (!f.city_id && list[0]) f.city_id = list[0].id
  },
  { immediate: true },
)

const category = computed<Category>({
  get: () => ((CATEGORIES as readonly string[]).includes(f.category) ? (f.category as Category) : 'taxi'),
  set: (v) => (f.category = v),
})
const vehicleClass = computed<string>({
  get: () => f.vehicle_class || DEFAULT_VEHICLE_CLASS,
  set: (v) => (f.vehicle_class = v),
})

const res = useResource((signal) => tariffsApi.list({ city_id: f.city_id, include_inactive: true }, signal))
watch(() => f.city_id, (id) => id && void res.reload(), { immediate: true })

const all = computed<Tariff[]>(() => res.data.value?.items ?? [])
const active = computed(() => all.value.filter((x) => x.is_active))

/** Classes known from the class list plus any class that has tariffs here, in display order. */
const classCodes = computed(() => {
  const codes = vc.classes.value.map((c) => c.code)
  for (const x of all.value) if (!codes.includes(x.vehicle_class)) codes.push(x.vehicle_class)
  return codes
})
const classOptions = computed(() => classCodes.value.map((code) => ({ value: code, label: vc.name(code) })))

/** Whether the class can carry the category; unknown classes are assumed to. */
function carries(code: string, c: Category): boolean {
  const cls = vc.byCode(code)
  return cls ? classServesCategory(cls, c) : true
}

function currentOf(code: string, c: Category): Tariff | null {
  return active.value.find((x) => x.vehicle_class === code && x.category === c) ?? null
}

const current = computed(() => Object.fromEntries(CATEGORIES.map((c) => [c, currentOf(vehicleClass.value, c)])) as Record<Category, Tariff | null>)
const history = computed(() =>
  all.value.filter((x) => x.category === category.value && x.vehicle_class === vehicleClass.value).sort((a, b) => b.version - a.version),
)
const formOpen = ref(false)

const tabs = computed(() => CATEGORIES.map((c) => ({ value: c, label: t(`category.${c}`) })))

interface OverviewRow {
  code: string
  cells: Record<Category, Tariff | null>
}

const overview = computed<OverviewRow[]>(() =>
  classCodes.value.map((code) => ({ code, cells: Object.fromEntries(CATEGORIES.map((c) => [c, currentOf(code, c)])) as Record<Category, Tariff | null> })),
)

const overviewColumns = computed<Column<OverviewRow>[]>(() => [
  { key: 'class', label: t('classes.class') },
  ...CATEGORIES.map((c) => ({ key: c, label: t(`category.${c}`) })),
])

function range(x: Tariff): string {
  return `${fmt.money(x.min_fare)} – ${fmt.money(x.max_fare)}`
}

function items(x: Tariff): KeyValueItem[] {
  return [
    { key: 'range', label: t('config.fare_range'), value: range(x) },
    { key: 'step', label: t('config.fare_step'), value: fmt.money(x.fare_step) },
    { key: 'base', label: t('config.base_fare'), value: fmt.money(x.base_fare) },
    { key: 'km', label: t('config.per_km'), value: fmt.money(x.per_km) },
    { key: 'min', label: t('config.per_minute'), value: fmt.money(x.per_minute) },
    { key: 'stop', label: t('config.extra_stop_fee'), value: fmt.money(x.extra_stop_fee) },
    { key: 'fees', label: t('config.extra_fees'), value: x.extra_fees.length ? x.extra_fees.map((e) => `${fmt.name(e.name)} ${fmt.money(e.amount)}`).join(', ') : null },
    { key: 'commission', label: t('config.tariff_percent'), value: fmt.percentFromBps(x.commission_bps) },
    { key: 'created', label: t('config.published'), value: `${fmt.dateTime(x.created_at)}${x.created_by ? ` · ${x.created_by.full_name || x.created_by.email}` : ''}` },
  ]
}

const columns = computed<Column<Tariff>[]>(() => [
  { key: 'version', label: t('config.version'), format: (v, r) => `v${String(v)} · ${r.is_active ? t('config.current') : t('config.archived')}` },
  { key: 'vehicle_class', label: t('classes.class'), format: (v) => vc.name(String(v)) },
  { key: 'range', label: t('config.fare_range'), value: (r) => `${range(r)} · ${t('config.step_value', { step: fmt.money(r.fare_step) })}` },
  { key: 'base_fare', label: t('config.base_fare'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'per_km', label: t('config.per_km'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'per_minute', label: t('config.per_minute'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'commission_bps', label: t('config.tariff_percent'), align: 'right', format: (v) => fmt.percentFromBps(v as number) },
  { key: 'created_at', label: t('config.published'), format: (v) => fmt.dateTime(v as string) },
  { key: 'created_by', label: t('config.published_by'), value: (r) => (r.created_by ? r.created_by.full_name || r.created_by.email : null) },
])

function newVersion(c: Category): void {
  category.value = c
  formOpen.value = true
}

function onSaved(): void {
  void res.reload({ silent: true })
}
</script>

<template>
  <section class="jt-page">
    <div class="toolbar">
      <SelectInput :model-value="f.city_id || null" :options="cityOptions" :aria-label="t('common.city')" @update:model-value="(v) => v && (f.city_id = v)" />
      <SelectInput :model-value="vehicleClass" :options="classOptions" :aria-label="t('classes.class')" @update:model-value="(v) => v && (vehicleClass = v)" />
      <p class="jt-muted">{{ t('config.tariffs_hint') }}</p>
    </div>

    <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <div class="jt-grid-3">
        <Card
          v-for="c in CATEGORIES"
          :key="c"
          :title="`${t(`category.${c}`)} · ${vc.name(vehicleClass)}`"
          :subtitle="current[c] ? `v${current[c]!.version}` : carries(vehicleClass, c) ? t('config.no_tariff') : ''"
        >
          <template v-if="auth.can('config.manage') && carries(vehicleClass, c)" #actions>
            <AppButton size="sm" @click="newVersion(c)">{{ t('config.new_version') }}</AppButton>
          </template>
          <KeyValueList v-if="current[c]" :items="items(current[c]!)" />
          <p v-else-if="carries(vehicleClass, c)" class="jt-muted">{{ t('config.no_tariff_hint') }}</p>
          <p v-else class="jt-muted">{{ t('config.class_not_for_category', { category: t(`category.${c}`) }) }}</p>
        </Card>
      </div>

      <Card :title="t('config.tariffs_by_class')" :subtitle="t('config.tariffs_by_class_hint')" flush>
        <DataTable :columns="overviewColumns" :rows="overview" row-key="code" clickable :empty-text="t('config.no_tariff')" @row-click="(r) => (vehicleClass = r.code)">
          <template #cell-class="{ row }">
            <span class="cls" :class="{ 'is-selected': row.code === vehicleClass }">
              <VehicleClassIcon :icon="vc.byCode(row.code)?.icon" />
              {{ vc.name(row.code) }}
            </span>
          </template>
          <template v-for="c in CATEGORIES" :key="c" #[`cell-${c}`]="{ row }">
            <span v-if="row.cells[c]" class="cell">
              <span class="jt-num">{{ range(row.cells[c]!) }}</span>
              <span class="jt-muted">v{{ row.cells[c]!.version }}</span>
            </span>
            <span v-else class="jt-muted">{{ carries(row.code, c) ? t('config.no_tariff') : '—' }}</span>
          </template>
        </DataTable>
      </Card>

      <Card :title="t('config.history')" :subtitle="vc.name(vehicleClass)" flush>
        <template #actions>
          <Tabs v-model="category" :tabs="tabs" :aria-label="t('orders.category')" />
        </template>
        <DataTable :columns="columns" :rows="history" row-key="id" :empty-text="t('config.no_tariff')" />
      </Card>
    </template>

    <TariffForm v-model:open="formOpen" :city-id="f.city_id" :category="category" :vehicle-class="vehicleClass" :tariffs="all" @saved="onSaved" />
  </section>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.cls {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.cls.is-selected {
  font-weight: 600;
}

.cell {
  display: flex;
  flex-direction: column;
  line-height: 18px;
}

.cell .jt-muted {
  font-size: 12px;
}
</style>
