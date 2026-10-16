<!-- Gazetteer place: names in three languages, search aliases, point picked on the map. -->
<script setup lang="ts">
import { Marker, type Map as MapLibreMap } from 'maplibre-gl'
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LatLon } from '../../api/common'
import { placesApi, type Place, type PlaceInput } from '../../api/config'
import { AppButton, Drawer, FormField, SelectInput, TextInput, ToggleSwitch, toast } from '../../components'
import { useCities } from '../../composables/useCities'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { parseDecimal } from '../../forms/numbers'
import { inKazakhstanBBox } from '../../maps/constants'
import MapView from '../../maps/MapView.vue'
import { pinMarkerElement } from '../../maps/markers'

const props = defineProps<{ open: boolean; place: Place | null }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; saved: [place: Place] }>()

const MAX_ALIASES = 20
const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const { cities, byId } = useCities()

const form = reactive({ cityId: null as string | null, kk: '', ru: '', en: '', aliases: '', lat: '', lon: '', active: true })
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const map = shallowRef<MapLibreMap | null>(null)
let pin: Marker | null = null

watch(
  () => [props.open, props.place] as const,
  ([open]) => {
    if (!open) return
    const p = props.place
    Object.assign(form, {
      cityId: p?.city_id ?? null,
      kk: p?.name.kk ?? '',
      ru: p?.name.ru ?? '',
      en: p?.name.en ?? '',
      aliases: (p?.aliases ?? []).join('\n'),
      lat: p ? String(p.lat) : '',
      lon: p ? String(p.lon) : '',
      active: p?.is_active ?? true,
    })
    fieldErrors.value = {}
  },
  { immediate: true },
)

const cityOptions = computed(() => cities.value.map((c) => ({ value: c.id, label: `${fmt.name(c.name)} · ${c.code}` })))
const point = computed<LatLon | null>(() => {
  const lat = parseDecimal(form.lat)
  const lon = parseDecimal(form.lon)
  return lat === null || lon === null ? null : { lat, lon }
})
const mapCenter = computed<LatLon | null>(() => point.value ?? byId(form.cityId)?.center ?? null)
const aliasList = computed(() => [...new Set(form.aliases.split(/[\n,;]+/).map((a) => a.trim()).filter(Boolean))])

function placePin(): void {
  const m = map.value
  const p = point.value
  if (!m) return
  if (!p) {
    pin?.remove()
    pin = null
    return
  }
  if (!pin) pin = new Marker({ element: pinMarkerElement(), anchor: 'bottom' }).setLngLat([p.lon, p.lat]).addTo(m)
  else pin.setLngLat([p.lon, p.lat])
}

function onReady(m: MapLibreMap): void {
  map.value = m
  placePin()
}

watch(point, placePin)
watch(
  () => props.open,
  (open) => {
    if (!open) {
      pin?.remove()
      pin = null
      map.value = null
    }
  },
)

async function save(): Promise<void> {
  const f: Record<string, string> = {}
  for (const lang of ['kk', 'ru', 'en'] as const) if (!form[lang].trim()) f[`name.${lang}`] = 'required'
  const p = point.value
  if (!p) f.location = 'required'
  else if (!inKazakhstanBBox(p)) f.location = 'outside_service_area'
  if (aliasList.value.length > MAX_ALIASES) f.aliases = 'too_many'
  fieldErrors.value = errors.translate(f)
  if (Object.keys(f).length > 0 || !p || saving.value) return
  const body: PlaceInput = {
    city_id: form.cityId,
    name: { kk: form.kk.trim(), ru: form.ru.trim(), en: form.en.trim() },
    aliases: aliasList.value,
    lat: p.lat,
    lon: p.lon,
    is_active: form.active,
  }
  saving.value = true
  try {
    const saved = props.place ? await placesApi.update(props.place.id, body) : await placesApi.create(body)
    toast.success(t('config.place_saved'))
    emit('saved', saved)
    emit('update:open', false)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    if (Object.keys(fieldErrors.value).length === 0) errors.report(err)
  } finally {
    saving.value = false
  }
}

onBeforeUnmount(() => pin?.remove())
</script>

<template>
  <Drawer :open="open" :title="place ? t('config.place_edit') : t('config.place_new')" width="640px" @update:open="(v) => emit('update:open', v)">
    <form class="jt-form" novalidate @submit.prevent="save">
      <FormField v-slot="f" :label="t('common.city')" :error="fieldErrors.city_id" :hint="t('config.place_city_hint')">
        <SelectInput :id="f.id" v-model="form.cityId" :options="cityOptions" :placeholder="t('config.place_any_city')" />
      </FormField>
      <FormField v-slot="f" :label="t('config.name_kk')" :error="fieldErrors['name.kk'] || fieldErrors.name" required>
        <TextInput :id="f.id" v-model="form.kk" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('config.name_ru')" :error="fieldErrors['name.ru']" required>
        <TextInput :id="f.id" v-model="form.ru" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('config.name_en')" :error="fieldErrors['name.en']" required>
        <TextInput :id="f.id" v-model="form.en" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('config.aliases')" :error="fieldErrors.aliases" :hint="t('config.aliases_hint', { n: aliasList.length, max: MAX_ALIASES })">
        <textarea :id="f.id" v-model="form.aliases" class="aliases" rows="3" />
      </FormField>
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('config.latitude')" :error="fieldErrors.location" required>
          <TextInput :id="f.id" v-model="form.lat" inputmode="decimal" :invalid="f.invalid" />
        </FormField>
        <FormField v-slot="f" :label="t('config.longitude')" required>
          <TextInput :id="f.id" v-model="form.lon" inputmode="decimal" :invalid="Boolean(fieldErrors.location)" />
        </FormField>
      </div>
      <p class="jt-muted">{{ t('config.place_pick_hint') }}</p>
      <MapView
        v-if="open"
        :center="mapCenter"
        :zoom="15"
        height="300px"
        cursor="crosshair"
        @ready="onReady"
        @click="(p) => ((form.lat = p.lat.toFixed(6)), (form.lon = p.lon.toFixed(6)))"
      />
      <ToggleSwitch v-model="form.active" :label="t('common.active')" />
      <button type="submit" hidden />
    </form>
    <template #footer>
      <AppButton @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
      <AppButton variant="primary" :loading="saving" @click="save">{{ t('common.save') }}</AppButton>
    </template>
  </Drawer>
</template>

<style scoped>
.aliases {
  width: 100%;
  padding: 8px 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  resize: vertical;
  outline: none;
}

.aliases:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

:deep(.map) {
  border-radius: var(--jt-radius-inner);
}
</style>
