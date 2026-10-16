<!-- Cities and their service zones: list on the left, parameters or zone editor on the right. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
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

const cities = computed<City[]>(() => res.data.value?.items ?? [])
const selectedId = computed(() => (typeof route.query.city === 'string' ? route.query.city : (cities.value[0]?.id ?? '')))
const isNew = computed(() => selectedId.value === 'new')
const selected = computed(() => cities.value.find((c) => c.id === selectedId.value) ?? null)

type TabKey = 'params' | 'zones'
const tab = computed<TabKey>({
  get: () => (route.query.tab === 'zones' ? 'zones' : 'params'),
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
          <span v-if="!c.is_active" class="city-chip__off">· {{ t('common.inactive') }}</span>
        </button>
      </div>
      <AppButton size="sm" :variant="isNew ? 'primary' : 'secondary'" @click="select('new')">{{ t('config.new_city') }}</AppButton>
    </div>

    <div class="cities__main">
      <template v-if="isNew">
        <h2 class="jt-section-title">{{ t('config.new_city') }}</h2>
        <CityForm :city="null" @saved="onSaved" />
      </template>
      <template v-else-if="selected">
        <div class="cities__title">
          <h2 class="jt-section-title">{{ fmt.name(selected.name) }} <span class="jt-mono jt-muted">{{ selected.code }}</span></h2>
          <Tabs v-model="tab" :tabs="tabs" :aria-label="fmt.name(selected.name)" />
        </div>
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
