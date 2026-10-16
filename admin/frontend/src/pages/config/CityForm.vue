<!-- Create or edit a city: names, code, map centre (click the map), zoom, time zone and dispatch policy. -->
<script setup lang="ts">
import { Marker, type Map as MapLibreMap } from 'maplibre-gl'
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LatLon } from '../../api/common'
import { CITY_TIMEZONES, citiesApi, type City, type CityInput } from '../../api/config'
import { AppButton, FormField, SelectInput, TextInput, ToggleSwitch, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { parseDecimal, parseInteger } from '../../forms/numbers'
import { DEFAULT_MAP_CENTER, inKazakhstanBBox } from '../../maps/constants'
import MapView from '../../maps/MapView.vue'
import { pinMarkerElement } from '../../maps/markers'

const props = defineProps<{ city: City | null }>()
const emit = defineEmits<{ saved: [city: City] }>()

const { t } = useI18n()
const errors = useErrors()

interface Form {
  code: string
  kk: string
  ru: string
  en: string
  lat: string
  lon: string
  zoom: string
  timezone: string
  radius: string
  requireLocation: boolean
  maxAge: string
  searchTimeout: string
  active: boolean
}

function fromCity(c: City | null): Form {
  return {
    code: c?.code ?? '',
    kk: c?.name.kk ?? '',
    ru: c?.name.ru ?? '',
    en: c?.name.en ?? '',
    lat: c ? String(c.center.lat) : '',
    lon: c ? String(c.center.lon) : '',
    zoom: String(c?.default_zoom ?? 13),
    timezone: c?.timezone ?? 'Asia/Aqtobe',
    radius: String(c?.dispatch_radius_m ?? 50000),
    requireLocation: c?.require_driver_location ?? false,
    maxAge: String(c?.driver_location_max_age_s ?? 900),
    searchTimeout: String(c?.search_timeout_s ?? 900),
    active: c?.is_active ?? true,
  }
}

const form = reactive<Form>(fromCity(props.city))
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const map = shallowRef<MapLibreMap | null>(null)
let pin: Marker | null = null

watch(
  () => props.city?.id,
  () => {
    Object.assign(form, fromCity(props.city))
    fieldErrors.value = {}
    placePin()
  },
)

const tzOptions = computed(() => CITY_TIMEZONES.map((z) => ({ value: z, label: z })))
const center = computed<LatLon | null>(() => {
  const lat = parseDecimal(form.lat)
  const lon = parseDecimal(form.lon)
  return lat === null || lon === null ? null : { lat, lon }
})

function placePin(): void {
  const m = map.value
  const c = center.value
  if (!m) return
  if (!c) {
    pin?.remove()
    pin = null
    return
  }
  if (!pin) pin = new Marker({ element: pinMarkerElement(), anchor: 'bottom' }).setLngLat([c.lon, c.lat]).addTo(m)
  else pin.setLngLat([c.lon, c.lat])
}

function onReady(m: MapLibreMap): void {
  map.value = m
  placePin()
}

function onMapClick(p: LatLon): void {
  form.lat = p.lat.toFixed(6)
  form.lon = p.lon.toFixed(6)
}

watch(center, placePin)

function intIn(name: string, raw: string, min: number, max: number, out: Record<string, string>): number {
  const n = parseInteger(raw)
  if (n === null) out[name] = 'integer'
  else if (n < min || n > max) out[name] = 'out_of_range'
  return n ?? 0
}

async function save(): Promise<void> {
  const f: Record<string, string> = {}
  if (!props.city && !/^[A-Za-z]{3}$/.test(form.code.trim())) f.code = 'invalid'
  for (const lang of ['kk', 'ru', 'en'] as const) if (!form[lang].trim()) f[`name.${lang}`] = 'required'
  const c = center.value
  if (!c) f.center = 'required'
  else if (!inKazakhstanBBox(c)) f.center = 'outside_service_area'
  const zoom = parseDecimal(form.zoom)
  if (zoom === null || zoom < 3 || zoom > 20) f.default_zoom = 'out_of_range'
  const radius = intIn('dispatch_radius_m', form.radius, 500, 1_000_000, f)
  const maxAge = intIn('driver_location_max_age_s', form.maxAge, 30, 86_400, f)
  const timeout = intIn('search_timeout_s', form.searchTimeout, 60, 86_400, f)
  fieldErrors.value = errors.translate(f)
  if (Object.keys(f).length > 0 || !c || zoom === null || saving.value) return

  const body: CityInput = {
    name: { kk: form.kk.trim(), ru: form.ru.trim(), en: form.en.trim() },
    center: c,
    default_zoom: zoom,
    timezone: form.timezone,
    is_active: form.active,
    dispatch_radius_m: radius,
    require_driver_location: form.requireLocation,
    driver_location_max_age_s: maxAge,
    search_timeout_s: timeout,
  }
  saving.value = true
  try {
    const saved = props.city ? await citiesApi.update(props.city.id, body) : await citiesApi.create({ ...body, code: form.code.trim().toUpperCase() })
    toast.success(t('config.city_saved'))
    emit('saved', saved)
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
  <form class="city-form" novalidate @submit.prevent="save">
    <div class="city-form__fields">
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('config.city_code')" :error="fieldErrors.code" :hint="city ? t('config.city_code_fixed') : t('config.city_code_hint')" required>
          <TextInput :id="f.id" v-model="form.code" :readonly="Boolean(city)" :invalid="f.invalid" class="jt-mono" />
        </FormField>
        <FormField v-slot="f" :label="t('config.timezone')" :error="fieldErrors.timezone">
          <SelectInput :id="f.id" :model-value="form.timezone" :options="tzOptions" @update:model-value="(v) => v && (form.timezone = v)" />
        </FormField>
      </div>
      <FormField v-slot="f" :label="t('config.name_kk')" :error="fieldErrors['name.kk'] || fieldErrors.name" required>
        <TextInput :id="f.id" v-model="form.kk" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('config.name_ru')" :error="fieldErrors['name.ru']" required>
        <TextInput :id="f.id" v-model="form.ru" :invalid="f.invalid" />
      </FormField>
      <FormField v-slot="f" :label="t('config.name_en')" :error="fieldErrors['name.en']" required>
        <TextInput :id="f.id" v-model="form.en" :invalid="f.invalid" />
      </FormField>
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('config.latitude')" :error="fieldErrors.center" required>
          <TextInput :id="f.id" v-model="form.lat" inputmode="decimal" :invalid="f.invalid" />
        </FormField>
        <FormField v-slot="f" :label="t('config.longitude')" required>
          <TextInput :id="f.id" v-model="form.lon" inputmode="decimal" :invalid="Boolean(fieldErrors.center)" />
        </FormField>
        <FormField v-slot="f" :label="t('config.zoom')" :error="fieldErrors.default_zoom">
          <TextInput :id="f.id" v-model="form.zoom" inputmode="decimal" :invalid="f.invalid" />
        </FormField>
      </div>
      <div class="jt-form-row">
        <FormField v-slot="f" :label="t('config.dispatch_radius')" :error="fieldErrors.dispatch_radius_m" :hint="t('config.dispatch_radius_hint')">
          <TextInput :id="f.id" v-model="form.radius" inputmode="numeric" :invalid="f.invalid" />
        </FormField>
        <FormField v-slot="f" :label="t('config.search_timeout')" :error="fieldErrors.search_timeout_s" :hint="t('config.search_timeout_hint')">
          <TextInput :id="f.id" v-model="form.searchTimeout" inputmode="numeric" :invalid="f.invalid" />
        </FormField>
      </div>
      <ToggleSwitch v-model="form.requireLocation" :label="t('config.require_location')" />
      <FormField v-if="form.requireLocation" v-slot="f" :label="t('config.location_max_age')" :error="fieldErrors.driver_location_max_age_s" :hint="t('config.location_max_age_hint')">
        <TextInput :id="f.id" v-model="form.maxAge" inputmode="numeric" :invalid="f.invalid" />
      </FormField>
      <ToggleSwitch v-model="form.active" :label="t('config.city_active')" />
      <div class="jt-row">
        <AppButton type="submit" variant="primary" :loading="saving">{{ city ? t('common.save') : t('config.create_city') }}</AppButton>
      </div>
    </div>
    <div class="city-form__map">
      <p class="jt-muted">{{ t('config.center_pick_hint') }}</p>
      <MapView :center="center ?? DEFAULT_MAP_CENTER" :zoom="city?.default_zoom ?? 11" height="420px" cursor="crosshair" @ready="onReady" @click="onMapClick" />
    </div>
  </form>
</template>

<style scoped>
.city-form {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--jt-space-5);
  align-items: start;
}

.city-form__fields {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.city-form__map {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.city-form__map :deep(.map) {
  border-radius: var(--jt-radius-inner);
}
</style>
