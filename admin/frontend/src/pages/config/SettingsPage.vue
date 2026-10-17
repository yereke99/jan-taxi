<!--
  Runtime settings built from GET /admin/settings, grouped by area
  (Kaspi payee and company requisites, top-ups and automatic crediting from
  receipts, radius dispatch, …). Changed values are saved together in one
  PATCH; "default" restores the environment or built-in value (PATCH key: null).
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import { settingsApi, type SettingItem } from '../../api/config'
import { AppButton, Card, StateView, confirmAction, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import SettingField from './SettingField.vue'
import { DISPATCH_KEYS, SETTING_GROUPS, fromDraft, groupOf, radiusSchedule, sameJSON, toDraft, type Draft } from './settingsModel'

const { t, te } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const res = useResource((signal) => settingsApi.list(signal))
void res.reload()

const drafts = ref<Record<string, Draft>>({})
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)

const items = computed<SettingItem[]>(() => res.data.value?.items ?? [])
watch(items, (list) => {
  drafts.value = Object.fromEntries(list.map((i) => [i.key, toDraft(i)]))
  fieldErrors.value = {}
})

const groups = computed(() => {
  const ids = [...SETTING_GROUPS.map((g) => g.id), 'other']
  return ids
    .map((id) => ({ id, items: items.value.filter((i) => groupOf(i.key) === id) }))
    .filter((g) => g.items.length > 0)
})

function changed(item: SettingItem): boolean {
  const d = drafts.value[item.key]
  if (d === undefined) return false
  const v = fromDraft(item, d)
  return 'invalid' in v || !sameJSON(v.value, item.value)
}

const changedItems = computed(() => items.value.filter(changed))

/** Integer value a key would have after saving (draft, else stored); null when not a valid integer. */
function draftInt(key: string): number | null {
  const item = items.value.find((i) => i.key === key)
  const d = drafts.value[key]
  if (!item || d === undefined) return null
  const v = fromDraft(item, d)
  return 'invalid' in v || typeof v.value !== 'number' ? null : v.value
}

const radius = computed(() => {
  const [base, step, stepS, max] = [DISPATCH_KEYS.base, DISPATCH_KEYS.step, DISPATCH_KEYS.stepS, DISPATCH_KEYS.max].map(draftInt)
  if (base == null || step == null || stepS == null || max == null) return { schedule: [], maxBelowBase: false }
  return { schedule: radiusSchedule(base, step, stepS, max), maxBelowBase: max < base }
})

function afterLabel(seconds: number): string {
  if (seconds === 0) return t('settings.preview_start')
  const time = seconds % 60 === 0 ? fmt.duration(seconds) : t('settings.preview_seconds', { n: seconds })
  return t('settings.preview_after', { time })
}

async function save(): Promise<void> {
  const body: Record<string, unknown> = {}
  const local: Record<string, string> = {}
  for (const item of changedItems.value) {
    const v = fromDraft(item, drafts.value[item.key]!)
    if ('invalid' in v) local[item.key] = v.invalid
    else body[item.key] = v.value
  }
  // Same rule as the API: the maximum radius may not be below the starting one.
  if (radius.value.maxBelowBase && (DISPATCH_KEYS.base in body || DISPATCH_KEYS.max in body)) local[DISPATCH_KEYS.max] = 'less_than_base'
  fieldErrors.value = errors.translate(local)
  if (Object.keys(local).length > 0 || Object.keys(body).length === 0 || saving.value) return
  saving.value = true
  try {
    res.set(await settingsApi.update(body))
    toast.success(t('settings.saved', { n: Object.keys(body).length }))
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    errors.report(err)
  } finally {
    saving.value = false
  }
}

function discard(): void {
  drafts.value = Object.fromEntries(items.value.map((i) => [i.key, toDraft(i)]))
  fieldErrors.value = {}
}

async function resetDefault(item: SettingItem): Promise<void> {
  const ok = await confirmAction({ title: t('settings.reset_title'), message: t('settings.reset_message', { key: item.key }), confirmLabel: t('settings.reset_default') })
  if (!ok) return
  try {
    res.set(await settingsApi.update({ [item.key]: null }))
    toast.success(t('settings.reset_done'))
  } catch (err) {
    errors.report(err)
  }
}

onBeforeRouteLeave(async () => {
  if (changedItems.value.length === 0) return true
  return confirmAction({ title: t('common.unsaved_changes'), confirmLabel: t('common.discard'), destructive: true })
})

function groupTitle(id: string): string {
  return te(`settings.group_${id}`, 'kk') ? t(`settings.group_${id}`) : id
}
</script>

<template>
  <section class="jt-page settings">
    <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <Card v-for="g in groups" :key="g.id" :title="groupTitle(g.id)" :subtitle="te(`settings.group_${g.id}_hint`, 'kk') ? t(`settings.group_${g.id}_hint`) : ''">
        <SettingField
          v-for="item in g.items"
          :key="item.key"
          :model-value="drafts[item.key] ?? ''"
          @update:model-value="(v) => (drafts[item.key] = v)"
          :item="item"
          :error="fieldErrors[item.key]"
          :changed="changed(item)"
          @reset="resetDefault(item)"
        />
        <div v-if="g.id === 'dispatch'" class="radius" role="note">
          <strong>{{ t('settings.preview_title') }}</strong>
          <ol v-if="radius.schedule.length" class="radius__steps">
            <li v-for="(s, i) in radius.schedule" :key="s.afterS">
              <span class="jt-muted">{{ afterLabel(s.afterS) }}</span>
              <span class="jt-num">{{ fmt.distance(s.radiusM) }}</span>
              <span v-if="i === radius.schedule.length - 1 && radius.schedule.length > 1" class="jt-muted">{{ t('settings.preview_max') }}</span>
            </li>
          </ol>
          <p v-else class="jt-muted">{{ t('settings.preview_invalid') }}</p>
        </div>
      </Card>
      <div v-if="changedItems.length" class="savebar jt-glass" role="region" :aria-label="t('settings.unsaved', { n: changedItems.length })">
        <span>{{ t('settings.unsaved', { n: changedItems.length }) }}</span>
        <div class="jt-row">
          <AppButton variant="plain" :disabled="saving" @click="discard">{{ t('common.discard') }}</AppButton>
          <AppButton variant="primary" :loading="saving" @click="save">{{ t('common.save') }}</AppButton>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.settings {
  max-width: 920px;
  padding-bottom: 80px;
}

.radius {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: var(--jt-space-3);
  padding: 12px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-fill-secondary);
}

.radius__steps {
  display: flex;
  flex-wrap: wrap;
  gap: 4px var(--jt-space-4);
}

.radius__steps li {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
}

.savebar {
  position: sticky;
  bottom: var(--jt-space-4);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
  padding: 10px 12px 10px 20px;
  border-radius: var(--jt-radius-round);
}
</style>
