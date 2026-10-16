<!--
  Runtime settings generated from GET /admin/settings, grouped by area.
  Changed values are saved together in one PATCH; "default" restores the
  environment or built-in value (PATCH key: null).
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import { settingsApi, type SettingItem } from '../../api/config'
import { AppButton, Card, StateView, confirmAction, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useResource } from '../../composables/useResource'
import SettingField from './SettingField.vue'
import { SETTING_GROUPS, fromDraft, groupOf, sameJSON, toDraft, type Draft } from './settingsModel'

const { t, te } = useI18n()
const errors = useErrors()
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

async function save(): Promise<void> {
  const body: Record<string, unknown> = {}
  const local: Record<string, string> = {}
  for (const item of changedItems.value) {
    const v = fromDraft(item, drafts.value[item.key]!)
    if ('invalid' in v) local[item.key] = v.invalid
    else body[item.key] = v.value
  }
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
