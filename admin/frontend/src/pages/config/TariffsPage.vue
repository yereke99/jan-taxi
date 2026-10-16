<!-- Tariffs: the current version per category for a city, version history, and new versions (config.manage). -->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { tariffsApi, type Tariff } from '../../api/config'
import { AppButton, Card, DataTable, KeyValueList, SelectInput, StateView, Tabs, type Column, type KeyValueItem } from '../../components'
import { useCities } from '../../composables/useCities'
import { useFormat } from '../../composables/useFormat'
import { useListQuery } from '../../composables/useListQuery'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'
import { CATEGORIES, type Category } from '../../utils/status'
import TariffForm from './TariffForm.vue'

const { t } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const { cities, options: cityOptions } = useCities()
const q = useListQuery({ city_id: '', category: 'taxi' })
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

const res = useResource((signal) => tariffsApi.list({ city_id: f.city_id, include_inactive: true }, signal))
watch(() => f.city_id, (id) => id && void res.reload(), { immediate: true })

const all = computed<Tariff[]>(() => res.data.value?.items ?? [])
const current = computed(() => Object.fromEntries(CATEGORIES.map((c) => [c, all.value.find((x) => x.category === c && x.is_active) ?? null])) as Record<Category, Tariff | null>)
const history = computed(() => all.value.filter((x) => x.category === category.value).sort((a, b) => b.version - a.version))
const formOpen = ref(false)

const tabs = computed(() => CATEGORIES.map((c) => ({ value: c, label: t(`category.${c}`) })))

function items(x: Tariff): KeyValueItem[] {
  return [
    { key: 'range', label: t('config.fare_range'), value: `${fmt.money(x.min_fare)} – ${fmt.money(x.max_fare)}` },
    { key: 'step', label: t('config.fare_step'), value: fmt.money(x.fare_step) },
    { key: 'base', label: t('config.base_fare'), value: fmt.money(x.base_fare) },
    { key: 'km', label: t('config.per_km'), value: fmt.money(x.per_km) },
    { key: 'min', label: t('config.per_minute'), value: fmt.money(x.per_minute) },
    { key: 'stop', label: t('config.extra_stop_fee'), value: fmt.money(x.extra_stop_fee) },
    { key: 'fees', label: t('config.extra_fees'), value: x.extra_fees.length ? x.extra_fees.map((e) => `${fmt.name(e.name)} ${fmt.money(e.amount)}`).join(', ') : null },
    { key: 'commission', label: t('orders.commission'), value: fmt.percentFromBps(x.commission_bps) },
    { key: 'created', label: t('config.published'), value: `${fmt.dateTime(x.created_at)}${x.created_by ? ` · ${x.created_by.full_name || x.created_by.email}` : ''}` },
  ]
}

const columns = computed<Column<Tariff>[]>(() => [
  { key: 'version', label: t('config.version'), format: (v, r) => `v${String(v)} · ${r.is_active ? t('config.current') : t('config.archived')}` },
  { key: 'range', label: t('config.fare_range'), value: (r) => `${fmt.money(r.min_fare)} – ${fmt.money(r.max_fare)} · ${t('config.step_value', { step: fmt.money(r.fare_step) })}` },
  { key: 'base_fare', label: t('config.base_fare'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'per_km', label: t('config.per_km'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'per_minute', label: t('config.per_minute'), align: 'right', format: (v) => fmt.money(v as number) },
  { key: 'commission_bps', label: t('orders.commission'), align: 'right', format: (v) => fmt.percentFromBps(v as number) },
  { key: 'created_at', label: t('config.published'), format: (v) => fmt.dateTime(v as string) },
  { key: 'created_by', label: t('config.published_by'), value: (r) => (r.created_by ? r.created_by.full_name || r.created_by.email : null) },
])

function onSaved(): void {
  void res.reload({ silent: true })
}
</script>

<template>
  <section class="jt-page">
    <div class="toolbar">
      <SelectInput :model-value="f.city_id || null" :options="cityOptions" :aria-label="t('common.city')" @update:model-value="(v) => v && (f.city_id = v)" />
      <p class="jt-muted">{{ t('config.tariffs_hint') }}</p>
    </div>

    <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <div class="jt-grid-3">
        <Card v-for="c in CATEGORIES" :key="c" :title="t(`category.${c}`)" :subtitle="current[c] ? `v${current[c]!.version}` : t('config.no_tariff')">
          <template v-if="auth.can('config.manage')" #actions>
            <AppButton size="sm" @click="(category = c), (formOpen = true)">{{ t('config.new_version') }}</AppButton>
          </template>
          <KeyValueList v-if="current[c]" :items="items(current[c]!)" />
          <p v-else class="jt-muted">{{ t('config.no_tariff_hint') }}</p>
        </Card>
      </div>

      <Card :title="t('config.history')" flush>
        <template #actions>
          <Tabs v-model="category" :tabs="tabs" :aria-label="t('orders.category')" />
        </template>
        <DataTable :columns="columns" :rows="history" row-key="id" :empty-text="t('config.no_tariff')" />
      </Card>
    </template>

    <TariffForm v-model:open="formOpen" :city-id="f.city_id" :category="category" :current="current[category]" @saved="onSaved" />
  </section>
</template>

<style scoped>
.toolbar {
  display: flex;
  align-items: center;
  gap: var(--jt-space-4);
}
</style>
