<!--
  Service zones of a city: list, create (draw a polygon), edit (drag
  vertices, add parts, rename, activate), delete, GeoJSON import/export.
  Geometry errors from the API (self-intersection, outside Kazakhstan …) are
  shown next to the map.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { citiesApi, type City, type Zone } from '../../api/config'
import { AppButton, FormField, Modal, StateView, TextArea, TextInput, ToggleSwitch, confirmAction, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { canClose, fromGeometry, parseGeoJSON, toFeatureJSON, toGeometry, vertexCount, type PolygonRings, type Position } from '../../maps/polygon'
import ZoneMap from './ZoneMap.vue'

const props = defineProps<{ city: City }>()

const { t } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const res = useResource((signal) => citiesApi.zones(props.city.id, signal))
watch(() => props.city.id, () => void res.reload(), { immediate: true })

/** Zone being edited: an existing id, 'new', or null for none. */
const editing = ref<string | null>(null)
const name = ref('')
const active = ref(true)
const polygons = ref<PolygonRings[]>([])
const draft = ref<Position[] | null>(null)
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const dirty = ref(false)
const importOpen = ref(false)
const importText = ref('')
const importError = ref('')
const zoneMap = ref<InstanceType<typeof ZoneMap> | null>(null)

const zones = computed<Zone[]>(() => res.data.value?.items ?? [])
const others = computed(() => zones.value.filter((z) => z.id !== editing.value))
const vertices = computed(() => vertexCount(polygons.value))
const draftClosable = computed(() => draft.value !== null && canClose(draft.value))

function startNew(): void {
  editing.value = 'new'
  name.value = ''
  active.value = true
  polygons.value = []
  draft.value = []
  fieldErrors.value = {}
  dirty.value = false
}

function edit(z: Zone): void {
  editing.value = z.id
  name.value = z.name
  active.value = z.is_active
  polygons.value = fromGeometry(z.geometry)
  draft.value = null
  fieldErrors.value = {}
  dirty.value = false
  setTimeout(() => zoneMap.value?.fit(), 0)
}

async function stopEditing(): Promise<boolean> {
  if (dirty.value) {
    const ok = await confirmAction({ title: t('common.unsaved_changes'), confirmLabel: t('common.discard'), destructive: true })
    if (!ok) return false
  }
  editing.value = null
  draft.value = null
  polygons.value = []
  dirty.value = false
  return true
}

function onPolygons(next: PolygonRings[]): void {
  polygons.value = next
  dirty.value = true
}

function onClosed(ring: Position[]): void {
  polygons.value = [...polygons.value, [ring]]
  dirty.value = true
}

function closeDraft(): void {
  const d = draft.value
  if (!d || !canClose(d)) return
  onClosed(d)
  draft.value = null
}

function undoVertex(): void {
  if (draft.value && draft.value.length > 0) draft.value = draft.value.slice(0, -1)
}

function removePart(i: number): void {
  polygons.value = polygons.value.filter((_, k) => k !== i)
  dirty.value = true
}

function onKey(e: KeyboardEvent): void {
  if (draft.value === null) return
  const el = e.target as HTMLElement | null
  if (el && ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName)) return
  if (e.key === 'Escape') draft.value = null
  else if (e.key === 'Backspace') undoVertex()
  else if (e.key === 'Enter') closeDraft()
  else return
  e.preventDefault()
}

async function save(): Promise<void> {
  const geometry = toGeometry(polygons.value)
  const f: Record<string, string> = {}
  if (!name.value.trim()) f.name = errors.reason('required')
  if (!geometry) f.geometry = errors.reason('empty')
  fieldErrors.value = f
  if (Object.keys(f).length > 0 || !geometry || saving.value) return
  saving.value = true
  try {
    const body = { name: name.value.trim(), geometry, is_active: active.value }
    const saved = editing.value === 'new' ? await citiesApi.createZone(props.city.id, body) : await citiesApi.updateZone(editing.value!, body)
    toast.success(t('config.zone_saved'))
    dirty.value = false
    await res.reload({ silent: true })
    edit(saved)
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    if (!fieldErrors.value.geometry && !fieldErrors.value.name) errors.report(err)
  } finally {
    saving.value = false
  }
}

async function remove(z: Zone): Promise<void> {
  const ok = await confirmAction({
    title: t('config.zone_delete_title', { name: z.name }),
    message: t('config.zone_delete_message'),
    confirmLabel: t('common.delete'),
    destructive: true,
  })
  if (!ok) return
  try {
    await citiesApi.deleteZone(z.id)
    if (editing.value === z.id) {
      dirty.value = false
      await stopEditing()
    }
    toast.success(t('config.zone_deleted'))
    await res.reload({ silent: true })
  } catch (err) {
    errors.report(err)
  }
}

async function toggleActive(z: Zone, value: boolean): Promise<void> {
  try {
    await citiesApi.updateZone(z.id, { is_active: value })
    await res.reload({ silent: true })
  } catch (err) {
    errors.report(err)
  }
}

function exportZone(z: { name: string; geometry: Zone['geometry'] }): void {
  const text = toFeatureJSON(z.geometry, { name: z.name, city: props.city.code })
  const href = URL.createObjectURL(new Blob([text], { type: 'application/geo+json' }))
  const a = document.createElement('a')
  a.href = href
  a.download = `${props.city.code}-${z.name.replace(/[^\p{L}\p{N}_-]+/gu, '_') || 'zone'}.geojson`
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(href), 10_000)
}

function exportCurrent(): void {
  const geometry = toGeometry(polygons.value)
  if (geometry) exportZone({ name: name.value || 'zone', geometry })
}

async function readFile(e: Event): Promise<void> {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  importText.value = await file.text()
}

function applyImport(): void {
  const r = parseGeoJSON(importText.value)
  if ('error' in r) {
    importError.value = t(`config.geojson_${r.error}`)
    return
  }
  polygons.value = fromGeometry(r.geometry)
  draft.value = null
  dirty.value = true
  importOpen.value = false
  setTimeout(() => zoneMap.value?.fit(), 0)
}

function openImport(): void {
  importText.value = ''
  importError.value = ''
  importOpen.value = true
}

onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="zones">
    <aside class="zones__side">
      <div v-if="editing === null" class="zones__list">
        <div class="zones__head">
          <h3 class="jt-section-title">{{ t('config.zones') }}</h3>
          <AppButton size="sm" variant="primary" @click="startNew">{{ t('config.zone_new') }}</AppButton>
        </div>
        <StateView v-if="res.error.value || (res.loading.value && !res.data.value)" :loading="res.loading.value" :error="res.error.value" compact @retry="res.reload()" />
        <p v-else-if="zones.length === 0" class="jt-muted">{{ t('config.no_zones') }}</p>
        <ul v-else class="zone-items">
          <li v-for="z in zones" :key="z.id" class="zone-item">
            <button type="button" class="zone-item__main" @click="edit(z)">
              <strong>{{ z.name }}</strong>
              <span class="jt-muted">{{ t('config.zone_area', { area: (z.area_km2 ?? 0).toFixed(1) }) }} · {{ fmt.dateTime(z.updated_at) }}</span>
            </button>
            <div class="zone-item__actions">
              <ToggleSwitch :model-value="z.is_active" :label="t('common.active')" @update:model-value="(v) => toggleActive(z, v)" />
              <button type="button" class="jt-link-button" @click="exportZone(z)">GeoJSON</button>
              <button type="button" class="jt-link-button danger" @click="remove(z)">{{ t('common.delete') }}</button>
            </div>
          </li>
        </ul>
        <p class="jt-muted zones__hint">{{ t('config.zones_hint') }}</p>
      </div>

      <form v-else class="jt-form" novalidate @submit.prevent="save">
        <h3 class="jt-section-title">{{ editing === 'new' ? t('config.zone_new') : t('config.zone_edit') }}</h3>
        <FormField v-slot="f" :label="t('common.name')" :error="fieldErrors.name" required>
          <TextInput :id="f.id" v-model="name" :invalid="f.invalid" @update:model-value="dirty = true" />
        </FormField>
        <ToggleSwitch v-model="active" :label="t('config.zone_active')" @update:model-value="dirty = true" />

        <div class="draw">
          <template v-if="draft !== null">
            <p class="draw__state">{{ t('config.drawing', { n: draft.length }) }}</p>
            <p class="jt-muted">{{ t('config.drawing_hint') }}</p>
            <div class="jt-row">
              <AppButton size="sm" variant="primary" :disabled="!draftClosable" @click="closeDraft">{{ t('config.close_polygon') }}</AppButton>
              <AppButton size="sm" :disabled="draft.length === 0" @click="undoVertex">{{ t('config.undo_vertex') }}</AppButton>
              <AppButton size="sm" variant="plain" @click="draft = null">{{ t('common.cancel') }}</AppButton>
            </div>
          </template>
          <template v-else>
            <p class="jt-muted">{{ t('config.edit_hint') }}</p>
            <div class="jt-row">
              <AppButton size="sm" @click="draft = []">{{ t('config.add_part') }}</AppButton>
              <AppButton size="sm" @click="openImport">{{ t('config.import') }}</AppButton>
              <AppButton size="sm" :disabled="polygons.length === 0" @click="exportCurrent">{{ t('config.export') }}</AppButton>
            </div>
          </template>
        </div>

        <ul v-if="polygons.length" class="parts">
          <li v-for="(rings, i) in polygons" :key="i" class="parts__item">
            <span>{{ t('config.part_n', { n: i + 1, v: rings[0]?.length ?? 0 }) }}<template v-if="rings.length > 1"> · {{ t('config.holes_n', { n: rings.length - 1 }) }}</template></span>
            <button type="button" class="jt-link-button danger" @click="removePart(i)">{{ t('common.delete') }}</button>
          </li>
        </ul>
        <p class="jt-muted">{{ t('config.vertices_total', { n: vertices }) }}</p>
        <p v-if="fieldErrors.geometry" class="geom-error" role="alert">{{ t('config.geometry_error', { reason: fieldErrors.geometry }) }}</p>

        <div class="jt-row">
          <AppButton type="submit" variant="primary" :loading="saving" :disabled="draft !== null && draft.length > 0">{{ t('common.save') }}</AppButton>
          <AppButton variant="plain" :disabled="saving" @click="stopEditing">{{ t('common.close') }}</AppButton>
        </div>
      </form>
    </aside>

    <div class="zones__map">
      <ZoneMap
        ref="zoneMap"
        v-model:draft="draft"
        :center="city.center"
        :zoom="city.default_zoom"
        :others="editing === null ? zones : others"
        :polygons="polygons"
        @update:polygons="onPolygons"
        @closed="onClosed"
      />
    </div>

    <Modal v-model:open="importOpen" :title="t('config.import_title')" width="560px">
      <div class="jt-form">
        <p class="jt-muted">{{ t('config.import_hint') }}</p>
        <input type="file" accept=".geojson,.json,application/geo+json,application/json" @change="readFile" />
        <FormField v-slot="f" :label="t('config.geojson')" :error="importError">
          <TextArea :id="f.id" v-model="importText" :rows="8" mono :invalid="f.invalid" />
        </FormField>
      </div>
      <template #footer>
        <AppButton @click="importOpen = false">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" :disabled="!importText.trim()" @click="applyImport">{{ t('config.import_apply') }}</AppButton>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.zones {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
}

.zones__side {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.zones__list {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.zones__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.zones__hint {
  font: var(--jt-font-caption);
}

.zones__map {
  overflow: hidden;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.zone-items {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.zone-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.zone-item__main {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  text-align: left;
}

.zone-item__actions {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.danger {
  color: var(--jt-color-red-text);
}

.draw {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
  padding: 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.draw__state {
  font: var(--jt-font-body-strong);
}

.parts {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.parts__item {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-2);
}

.geom-error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
