<!--
  Tariff regions: the nationwide default region (Kazakhstan, KAZ) and the
  cities whose service zones override its tariffs. Chips on top, parameters
  or the zone editor of the selected city below.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { isDefaultRegion } from '../../api/common'
import { citiesApi, type City } from '../../api/config'
import { AppButton, StateView, Tabs } from '../../components'
import { setCities } from '../../composables/useCities'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import CityForm from './CityForm.vue'
import ZoneEditor from './ZoneEditor.vue'

const { t } = useI18n()
const fmt = useFormat()
const route = useRoute()
const router = useRouter()

const res = useResource(async (signal) => {
  const page = await citiesApi.list(signal)
  setCities(page.items)
  return page
})
void res.reload()

// The nationwide default region first, then the cities in API order.
const cities = computed<City[]>(() => {
  const list = res.data.value?.items ?? []
  return [...list.filter((c) => isDefaultRegion(c)), ...list.filter((c) => !isDefaultRegion(c))]
})
const selectedId = computed(() => (typeof route.query.city === 'string' ? route.query.city : (cities.value[0]?.id ?? '')))
const isNew = computed(() => selectedId.value === 'new')
const selected = computed(() => cities.value.find((c) => c.id === selectedId.value) ?? null)
const selectedIsDefault = computed(() => isDefaultRegion(selected.value))

type TabKey = 'params' | 'zones'
const tab = computed<TabKey>({
  // The default region covers the whole country and has no zones.
  get: () => (route.query.tab === 'zones' && !selectedIsDefault.value ? 'zones' : 'params'),
  set: (v) => void router.replace({ query: { ...route.query, tab: v === 'params' ? undefined : v } }),
})
const tabs = computed(() => [
  { value: 'params' as const, label: t('config.city_params') },
  { value: 'zones' as const, label: t('config.zones') },
])

function select(id: string): void {
  void router.replace({ query: id === 'new' ? { city: id } : { ...route.query, city: id } })
}

async function onSaved(city: City): Promise<void> {
  await res.reload({ silent: true })
  select(city.id)
}
</script>

<template>
  <section class="jt-page">
    <div class="cities__bar">
      <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" compact @retry="res.reload()" />
      <div v-else class="city-chips" role="tablist" :aria-label="t('nav.cities')">
        <button
          v-for="c in cities"
          :key="c.id"
          type="button"
          role="tab"
          class="city-chip"
          :class="{ 'is-active': c.id === selectedId }"
          :aria-selected="c.id === selectedId"
          @click="select(c.id)"
        >
          {{ fmt.name(c.name) }} <span class="jt-mono city-chip__code">{{ c.code }}</span>
          <span v-if="isDefaultRegion(c)" class="city-chip__badge">{{ t('config.default_region_badge') }}</span>
          <span v-else-if="!c.is_active" class="city-chip__off">· {{ t('common.inactive') }}</span>
        </button>
      </div>
      <AppButton size="sm" :variant="isNew ? 'primary' : 'secondary'" @click="select('new')">{{ t('config.new_city') }}</AppButton>
    </div>

    <p class="cities__note" role="note">{{ t('config.regions_note') }}</p>

    <div class="cities__main">
      <template v-if="isNew">
        <h2 class="jt-section-title">{{ t('config.new_city') }}</h2>
        <CityForm :city="null" @saved="onSaved" />
      </template>
      <template v-else-if="selected">
        <div class="cities__title">
          <h2 class="jt-section-title">
            {{ fmt.name(selected.name) }} <span class="jt-mono jt-muted">{{ selected.code }}</span>
            <span v-if="selectedIsDefault" class="default-badge">{{ t('config.default_region_badge') }}</span>
          </h2>
          <Tabs v-if="!selectedIsDefault" v-model="tab" :tabs="tabs" :aria-label="fmt.name(selected.name)" />
        </div>
        <p v-if="selectedIsDefault" class="cities__default" role="note">{{ t('config.default_region_note') }}</p>
        <CityForm v-if="tab === 'params'" :city="selected" @saved="onSaved" />
        <ZoneEditor v-else :city="selected" />
      </template>
      <p v-else-if="res.data.value" class="jt-muted">{{ t('config.no_cities') }}</p>
    </div>
  </section>
</template>

<style scoped>
.cities__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
}

.city-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.city-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: var(--jt-control-height);
  padding: 0 14px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  font: 500 14px/20px var(--jt-font-family);
}

.city-chip:hover {
  background: var(--jt-color-fill-secondary);
}

.city-chip.is-active {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.city-chip__code,
.city-chip__off {
  font-size: 12px;
  opacity: 0.7;
}

.city-chip__badge,
.default-badge {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label);
  font: 600 11px/16px var(--jt-font-family);
  white-space: nowrap;
}

.city-chip.is-active .city-chip__badge {
  background: color-mix(in srgb, var(--jt-color-on-primary) 20%, transparent);
  color: inherit;
}

.default-badge {
  margin-left: var(--jt-space-2);
  vertical-align: middle;
}

.cities__note,
.cities__default {
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-small);
}

.cities__note {
  margin: var(--jt-space-1) 0 var(--jt-space-3);
}

.cities__main {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  padding: var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.cities__title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
}
</style>
