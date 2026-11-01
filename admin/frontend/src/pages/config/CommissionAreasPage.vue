<!--
  Villages with their own driver commission: a percentage of the fare taken
  from the driver who accepts an order whose pickup A lies in the village.
  List (commission, circle or boundary, active switch, edit, delete) and a map
  editor: find the village by name (OpenStreetMap) and take the suggested
  radius, adjust the circle (drag the centre or the edge point, type km) or
  draw the exact boundary. «Check a point» asks the API which commission an
  order picked up there gets.
-->
<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import type { LatLon } from '../../api/common'
import { commissionAreasApi, type CommissionArea, type CommissionAreaResolve, type CommissionAreaShape, type RadiusSuggestion, type SettlementHit } from '../../api/config'
import { AppButton, DebouncedInput, FormField, StateView, Tabs, TextInput, ToggleSwitch, confirmAction, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { DEFAULT_LOCALE } from '../../i18n'
import { canClose, vertexCount, type PolygonRings, type Position } from '../../maps/polygon'
import { isAbortError, isApiError } from '../../services/api'
import CommissionAreaMap from './CommissionAreaMap.vue'
import {
  DEFAULT_RADIUS_M,
  MAX_COMMISSION_BPS,
  areaForm,
  boundaryCenter,
  drawnRadius,
  metersToRadiusInput,
  resolveText,
  searchQuery,
  shapeText,
  suggestionText,
  validateAreaForm,
  type AreaForm,
  type Words,
} from './commissionAreaModel'

const { t, te } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const words: Words = {
  t: (key, named) => (named ? t(key, named) : t(key)),
  te: (key) => te(key, DEFAULT_LOCALE),
  distance: (m) => fmt.distance(m),
  percent: (bps) => fmt.percentFromBps(bps),
}

const res = useResource((signal) => commissionAreasApi.list(signal))
void res.reload()
const areas = computed<CommissionArea[]>(() => res.data.value?.items ?? [])

/** Village being edited: an id, 'new', or null for the list. */
const editing = ref<string | null>(null)
const form = reactive<AreaForm>(areaForm(null))
const draft = ref<Position[] | null>(null)
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const dirty = ref(false)
/** Bumped after a failed switch so it shows the saved state again. */
const toggleKey = ref(0)
const areaMap = ref<InstanceType<typeof CommissionAreaMap> | null>(null)

const others = computed(() => areas.value.filter((a) => a.id !== editing.value))
const radius = computed(() => (form.shape === 'circle' ? drawnRadius(form.radiusKm) : null))
const vertices = computed(() => vertexCount(form.polygons))
const draftClosable = computed(() => draft.value !== null && canClose(draft.value))
const shapeTabs = computed(() => [
  { value: 'circle' as const, label: t('commission_areas.shape_circle_tab') },
  { value: 'polygon' as const, label: t('commission_areas.shape_polygon_tab') },
])

function fitMap(): void {
  void nextTick(() => areaMap.value?.fit())
}

// Frame the saved villages once they are loaded.
watch(
  () => res.data.value,
  (data, before) => {
    if (data && !before && editing.value === null) fitMap()
  },
)

// --- radius suggestion ---------------------------------------------------

const suggestion = ref<RadiusSuggestion | null>(null)
const suggestFailed = ref(false)
const suggesting = ref(false)
let suggestCtrl: AbortController | null = null

const suggestNote = computed(() => {
  if (suggestion.value) return suggestionText(suggestion.value, words)
  return suggestFailed.value ? t('commission_areas.suggest_failed') : ''
})

/** Asks for a radius around the current centre; the circle takes it. */
async function suggest(): Promise<void> {
  const c = form.center
  if (!c) return
  suggestCtrl?.abort()
  const ctrl = new AbortController()
  suggestCtrl = ctrl
  suggesting.value = true
  suggestFailed.value = false
  try {
    const s = await commissionAreasApi.suggest(c, editing.value === 'new' ? null : editing.value, ctrl.signal)
    if (suggestCtrl !== ctrl) return
    suggestion.value = s
    form.radiusKm = metersToRadiusInput(s.radius_m)
    dirty.value = true
    fitMap()
  } catch (err) {
    if (isAbortError(err) || suggestCtrl !== ctrl) return
    suggestion.value = null
    suggestFailed.value = true
    // The circle still needs a radius to show and to drag.
    if (drawnRadius(form.radiusKm) === null) form.radiusKm = metersToRadiusInput(DEFAULT_RADIUS_M)
  } finally {
    if (suggestCtrl === ctrl) {
      suggesting.value = false
      suggestCtrl = null
    }
  }
}

function cancelSuggest(): void {
  suggestCtrl?.abort()
  suggestCtrl = null
  suggesting.value = false
  suggestion.value = null
  suggestFailed.value = false
}

// --- village search ------------------------------------------------------

const query = ref('')
/** Results of the last search; null before a search. */
const hits = ref<SettlementHit[] | null>(null)
const searching = ref(false)
let searchCtrl: AbortController | null = null

watch(query, async (text) => {
  searchCtrl?.abort()
  searchCtrl = null
  searching.value = false
  hits.value = null
  const q = searchQuery(text)
  if (!q) return
  const ctrl = new AbortController()
  searchCtrl = ctrl
  searching.value = true
  try {
    const r = await commissionAreasApi.search(q, ctrl.signal)
    if (searchCtrl === ctrl) hits.value = r.items
  } catch (err) {
    if (!isAbortError(err) && searchCtrl === ctrl) errors.report(err)
  } finally {
    if (searchCtrl === ctrl) {
      searching.value = false
      searchCtrl = null
    }
  }
})

/** A found village gives the name and the centre and asks for a radius. */
function pick(hit: SettlementHit): void {
  form.name = hit.title
  form.center = { lat: hit.lat, lon: hit.lon }
  fieldErrors.value = Object.fromEntries(Object.entries(fieldErrors.value).filter(([k]) => k !== 'name' && k !== 'center'))
  dirty.value = true
  query.value = ''
  suggestion.value = null
  fitMap()
  void suggest()
}

// --- editing -------------------------------------------------------------

function open(a: CommissionArea | null): void {
  stopChecking()
  cancelSuggest()
  editing.value = a ? a.id : 'new'
  Object.assign(form, areaForm(a))
  draft.value = null
  fieldErrors.value = {}
  query.value = ''
  dirty.value = false
  fitMap()
}

/** Changes or a boundary being drawn that leaving would lose. */
const unsaved = computed(() => editing.value !== null && (dirty.value || (draft.value?.length ?? 0) > 0))

function confirmDiscard(): Promise<boolean> {
  return confirmAction({ title: t('common.unsaved_changes'), confirmLabel: t('common.discard'), destructive: true })
}

async function close(): Promise<void> {
  if (unsaved.value && !(await confirmDiscard())) return
  cancelSuggest()
  editing.value = null
  draft.value = null
  query.value = ''
  dirty.value = false
}

/** The map set the centre (first click) or the pin was dragged. */
function onCenter(c: LatLon): void {
  const first = form.center === null
  form.center = c
  dirty.value = true
  if (first) void suggest()
}

function onRadius(m: number): void {
  form.radiusKm = metersToRadiusInput(m)
  dirty.value = true
}

const shape = computed<CommissionAreaShape>({
  get: () => form.shape,
  set: (v) => {
    if (v === form.shape) return
    form.shape = v
    dirty.value = true
    // An empty boundary starts drawing at once; a circle needs a radius.
    draft.value = v === 'polygon' && form.polygons.length === 0 ? [] : null
    if (v === 'circle' && drawnRadius(form.radiusKm) === null) form.radiusKm = metersToRadiusInput(suggestion.value?.radius_m ?? DEFAULT_RADIUS_M)
    fitMap()
  },
})

function onPolygons(next: PolygonRings[]): void {
  form.polygons = next
  dirty.value = true
}

function onClosed(ring: Position[]): void {
  form.polygons = [...form.polygons, [ring]]
  // Without a village point the middle of the boundary is taken; the pin can be moved.
  if (!form.center) form.center = boundaryCenter(form.polygons)
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
  form.polygons = form.polygons.filter((_, k) => k !== i)
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
  if (saving.value) return
  const { errors: codes, body } = validateAreaForm(form)
  fieldErrors.value = errors.translate(codes)
  if (!body) return
  const id = editing.value
  saving.value = true
  try {
    if (id === null || id === 'new') await commissionAreasApi.create(body)
    else await commissionAreasApi.update(id, body)
    toast.success(t('commission_areas.saved'))
    cancelSuggest()
    dirty.value = false
    editing.value = null
    draft.value = null
    await res.reload({ silent: true })
  } catch (err) {
    const f = errors.fields(err)
    if (isApiError(err) && err.fields.name === 'duplicate') f.name = t('commission_areas.name_taken')
    fieldErrors.value = f
    // Problems without a place in the form go to a toast.
    const shown = ['name', 'commission_bps', 'shape', 'center', form.shape === 'circle' ? 'radius_m' : 'boundary']
    if (Object.keys(f).length === 0 || Object.keys(f).some((k) => !shown.includes(k))) errors.report(err)
    if (isApiError(err) && err.status === 404) void res.reload({ silent: true })
  } finally {
    saving.value = false
  }
}

async function remove(a: CommissionArea): Promise<void> {
  const ok = await confirmAction({
    title: t('commission_areas.delete_title', { name: a.name }),
    message: t('commission_areas.delete_message'),
    confirmLabel: t('common.delete'),
    destructive: true,
  })
  if (!ok) return
  try {
    await commissionAreasApi.remove(a.id)
    toast.success(t('commission_areas.deleted'))
  } catch (err) {
    errors.report(err)
  }
  await res.reload({ silent: true })
}

async function toggleActive(a: CommissionArea, value: boolean): Promise<void> {
  try {
    await commissionAreasApi.update(a.id, { is_active: value })
  } catch (err) {
    errors.report(err)
    toggleKey.value++
  }
  await res.reload({ silent: true })
}

// --- check a point -------------------------------------------------------

const checking = ref(false)
const checkPoint = ref<LatLon | null>(null)
const checkResult = ref<CommissionAreaResolve | null>(null)
const checkLoading = ref(false)
let checkCtrl: AbortController | null = null

const checkText = computed(() => (checkResult.value ? resolveText(checkResult.value, words) : ''))
const checkDistance = computed(() => {
  const r = checkResult.value
  return r?.area && r.distance_m !== null ? t('commission_areas.check_distance', { distance: fmt.distance(r.distance_m) }) : ''
})

function stopChecking(): void {
  checkCtrl?.abort()
  checkCtrl = null
  checking.value = false
  checkPoint.value = null
  checkResult.value = null
  checkLoading.value = false
}

function toggleChecking(): void {
  if (checking.value) stopChecking()
  else checking.value = true
}

async function check(p: LatLon): Promise<void> {
  checkCtrl?.abort()
  const ctrl = new AbortController()
  checkCtrl = ctrl
  checkPoint.value = p
  checkResult.value = null
  checkLoading.value = true
  try {
    const r = await commissionAreasApi.resolve(p, ctrl.signal)
    if (checkCtrl === ctrl) checkResult.value = r
  } catch (err) {
    if (!isAbortError(err) && checkCtrl === ctrl) errors.report(err)
  } finally {
    if (checkCtrl === ctrl) {
      checkLoading.value = false
      checkCtrl = null
    }
  }
}

onMounted(() => document.addEventListener('keydown', onKey))
// Leaving the page (another menu item, browser Back) asks before dropping edits.
onBeforeRouteLeave(async () => !unsaved.value || (await confirmDiscard()))

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
  searchCtrl?.abort()
  suggestCtrl?.abort()
  checkCtrl?.abort()
})
</script>

<template>
  <section class="jt-page">
    <p class="areas__note" role="note">{{ t('commission_areas.note') }}</p>

    <div class="areas">
      <aside class="areas__side">
        <div v-if="editing === null" class="areas__list">
          <div class="areas__head">
            <h2 class="jt-section-title">{{ t('commission_areas.villages') }}</h2>
            <AppButton size="sm" variant="primary" @click="open(null)">{{ t('commission_areas.new') }}</AppButton>
          </div>

          <div class="check">
            <AppButton size="sm" :aria-pressed="checking" @click="toggleChecking">{{ checking ? t('commission_areas.check_stop') : t('commission_areas.check') }}</AppButton>
            <template v-if="checking">
              <p v-if="checkLoading" class="jt-muted">{{ t('common.loading') }}</p>
              <p v-else-if="checkResult" class="check__result" role="status">
                <strong>{{ checkText }}</strong><span v-if="checkDistance" class="jt-muted"> · {{ checkDistance }}</span>
              </p>
              <p v-else class="jt-muted check__hint">{{ t('commission_areas.check_hint') }}</p>
            </template>
          </div>

          <StateView v-if="res.error.value || (res.loading.value && !res.data.value)" :loading="res.loading.value" :error="res.error.value" compact @retry="res.reload()" />
          <p v-else-if="areas.length === 0" class="jt-muted">{{ t('commission_areas.empty') }}</p>
          <ul v-else class="area-items">
            <li v-for="a in areas" :key="a.id" class="area-item">
              <button type="button" class="area-item__main" @click="open(a)">
                <span class="area-item__title">
                  <strong class="area-item__name">{{ a.name }}</strong>
                  <strong class="jt-num">{{ fmt.percentFromBps(a.commission_bps) }}</strong>
                </span>
                <span class="jt-muted">{{ shapeText(a, words) }}</span>
              </button>
              <div class="area-item__actions">
                <ToggleSwitch :key="`${a.id}:${toggleKey}`" :model-value="a.is_active" :label="t('common.active')" @update:model-value="(v) => toggleActive(a, v)" />
                <button type="button" class="jt-link-button" @click="open(a)">{{ t('common.edit') }}</button>
                <button type="button" class="jt-link-button danger" @click="remove(a)">{{ t('common.delete') }}</button>
              </div>
            </li>
          </ul>
        </div>

        <form v-else class="jt-form" novalidate @submit.prevent="save">
          <h2 class="jt-section-title">{{ editing === 'new' ? t('commission_areas.new') : t('commission_areas.edit') }}</h2>

          <div class="search">
            <FormField v-slot="f" :label="t('commission_areas.search_label')" :hint="t('commission_areas.search_hint')">
              <DebouncedInput
                :id="f.id"
                v-model="query"
                class="search__input"
                :placeholder="t('commission_areas.search_placeholder')"
                :aria-label="t('commission_areas.search_label')"
                maxlength="120"
              />
            </FormField>
            <p v-if="searching" class="jt-muted">{{ t('common.loading') }}</p>
            <ul v-else-if="hits && hits.length" class="hits">
              <li v-for="(h, i) in hits" :key="`${i}:${h.lat},${h.lon}`">
                <button type="button" class="hit" @click="pick(h)">
                  <span class="hit__title">{{ h.title }}</span>
                  <span v-if="h.subtitle" class="jt-muted hit__subtitle">{{ h.subtitle }}</span>
                </button>
              </li>
            </ul>
            <p v-else-if="hits" class="jt-muted">{{ t('commission_areas.search_empty') }}</p>
          </div>

          <p v-if="suggestNote" class="suggest" role="status">{{ suggestNote }}</p>

          <FormField v-slot="f" :label="t('commission_areas.name')" :error="fieldErrors.name" required>
            <TextInput :id="f.id" v-model="form.name" :invalid="f.invalid" @update:model-value="dirty = true" />
          </FormField>
          <FormField v-slot="f" :label="t('config.commission_percent')" :error="fieldErrors.commission_bps" :hint="t('config.commission_hint', { max: MAX_COMMISSION_BPS / 100 })" required>
            <TextInput :id="f.id" v-model="form.percent" inputmode="decimal" placeholder="7" :invalid="f.invalid" @update:model-value="dirty = true" />
          </FormField>

          <div class="group">
            <span class="group__label">{{ t('commission_areas.shape') }}</span>
            <Tabs v-model="shape" :tabs="shapeTabs" :aria-label="t('commission_areas.shape')" />
            <p v-if="fieldErrors.shape" class="group__error" role="alert">{{ fieldErrors.shape }}</p>
          </div>

          <FormField v-if="form.shape === 'circle'" v-slot="f" :label="t('commission_areas.radius')" :error="fieldErrors.radius_m" :hint="t('commission_areas.radius_hint')" required>
            <div class="radius">
              <TextInput :id="f.id" v-model="form.radiusKm" inputmode="decimal" :invalid="f.invalid" @update:model-value="dirty = true" />
              <AppButton size="sm" :disabled="!form.center" :loading="suggesting" @click="suggest">{{ t('commission_areas.suggest_radius') }}</AppButton>
            </div>
          </FormField>
          <template v-else>
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
                <p class="jt-muted">{{ form.polygons.length ? t('config.edit_hint') : t('commission_areas.boundary_hint') }}</p>
                <div class="jt-row">
                  <AppButton size="sm" @click="draft = []">{{ form.polygons.length ? t('config.add_part') : t('commission_areas.draw_boundary') }}</AppButton>
                </div>
              </template>
            </div>
            <ul v-if="form.polygons.length" class="parts">
              <li v-for="(rings, i) in form.polygons" :key="i" class="parts__item">
                <span>{{ t('config.part_n', { n: i + 1, v: rings[0]?.length ?? 0 }) }}<template v-if="rings.length > 1"> · {{ t('config.holes_n', { n: rings.length - 1 }) }}</template></span>
                <button type="button" class="jt-link-button danger" @click="removePart(i)">{{ t('common.delete') }}</button>
              </li>
            </ul>
            <p v-if="form.polygons.length" class="jt-muted">{{ t('config.vertices_total', { n: vertices }) }}</p>
            <p v-if="fieldErrors.boundary" class="group__error" role="alert">{{ fieldErrors.boundary }}</p>
          </template>

          <div class="group">
            <span class="group__label">{{ t('commission_areas.center') }}</span>
            <span v-if="form.center" class="jt-mono">{{ form.center.lat.toFixed(6) }}, {{ form.center.lon.toFixed(6) }}</span>
            <p class="jt-muted group__hint">{{ form.center ? t('commission_areas.center_hint') : t('commission_areas.center_missing') }}</p>
            <p v-if="fieldErrors.center" class="group__error" role="alert">{{ fieldErrors.center }}</p>
          </div>

          <ToggleSwitch v-model="form.active" :label="t('commission_areas.active')" @update:model-value="dirty = true" />

          <div class="jt-row">
            <AppButton type="submit" variant="primary" :loading="saving" :disabled="draft !== null && draft.length > 0">{{ t('common.save') }}</AppButton>
            <AppButton variant="plain" :disabled="saving" @click="close">{{ t('common.close') }}</AppButton>
          </div>
        </form>
      </aside>

      <div class="areas__map">
        <CommissionAreaMap
          ref="areaMap"
          v-model:draft="draft"
          :others="others"
          :shape="editing === null ? null : form.shape"
          :center="editing === null ? null : form.center"
          :radius="radius"
          :polygons="form.polygons"
          :checking="checking"
          :check-point="checkPoint"
          @update:center="onCenter"
          @update:radius="onRadius"
          @update:polygons="onPolygons"
          @closed="onClosed"
          @pick="check"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.areas__note {
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-small);
}

.areas {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  gap: var(--jt-space-4);
  align-items: start;
  padding: var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.areas__side,
.areas__list {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  min-width: 0;
}

.areas__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.areas__map {
  overflow: hidden;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.check {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
}

.check__hint {
  font: var(--jt-font-caption);
}

.area-items {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.area-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.area-item__main {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  text-align: left;
}

.area-item__title {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-2);
}

.area-item__name {
  min-width: 0;
  overflow-wrap: anywhere;
}

.area-item__actions {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.danger {
  color: var(--jt-color-red-text);
}

.search {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.jt-form .search__input {
  width: 100%;
}

.hits {
  display: flex;
  flex-direction: column;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.hits li + li {
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.hit {
  display: flex;
  flex-direction: column;
  width: 100%;
  padding: 8px 12px;
  text-align: left;
}

.hit:hover {
  background: var(--jt-color-hover);
}

.hit__subtitle {
  font: var(--jt-font-caption);
}

.suggest {
  color: var(--jt-color-label-secondary);
  font: var(--jt-font-small);
}

.group {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
}

.group__label {
  font: 500 13px/18px var(--jt-font-family);
}

.group__hint {
  font: var(--jt-font-small);
}

.group__error {
  color: var(--jt-color-red);
  font: var(--jt-font-small);
}

.radius {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
}

.radius .input {
  max-width: 120px;
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
</style>
