<!--
  Marketing safety (§23.9): the three emergency switches (on by default), the
  broadcast speed and approval threshold, and the Telegram ids that may get
  test broadcasts. Only approvers may change them; every save needs a reason,
  which the audit log keeps with each key. Order messages and orders
  themselves are never affected.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import { MARKETING_SETTING_KEYS, marketingApi, type MarketingSettingKey } from '../../api/marketing'
import type { SettingItem } from '../../api/config'
import { AppButton, Card, ReasonDialog, StateView, TextInput, ToggleSwitch, confirmAction, toast } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useResource } from '../../composables/useResource'
import { useAuthStore } from '../../stores/auth'
import { fromDraft, intListProblem, intOutOfRange, sameJSON, toDraft, type Draft } from '../config/settingsModel'

const SWITCH_KEYS: readonly MarketingSettingKey[] = ['marketing.awards_enabled', 'marketing.redemptions_enabled', 'marketing.broadcasts_enabled']

const { t } = useI18n()
const errors = useErrors()
const fmt = useFormat()
const auth = useAuthStore()
const res = useResource((signal) => marketingApi.settings(signal))
void res.reload()

const canChange = computed(() => auth.can('marketing.approve'))
const drafts = ref<Record<string, Draft>>({})
const fieldErrors = ref<Record<string, string>>({})
const reasonOpen = ref(false)

const items = computed<SettingItem[]>(() => {
  const list = res.data.value?.items ?? []
  return MARKETING_SETTING_KEYS.map((k) => list.find((i) => i.key === k)).filter((i): i is SettingItem => i !== undefined)
})
watch(items, (list) => {
  drafts.value = Object.fromEntries(list.map((i) => [i.key, toDraft(i)]))
  fieldErrors.value = {}
})

const switches = computed(() => items.value.filter((i) => SWITCH_KEYS.includes(i.key as MarketingSettingKey)))
// Speed, threshold (int) and the test recipients (int_list, typed comma-separated).
const broadcastItems = computed(() => items.value.filter((i) => !SWITCH_KEYS.includes(i.key as MarketingSettingKey)))

function label(key: string): string {
  return t(`marketing.s_${key.replace('marketing.', '')}`)
}

function hint(key: string): string {
  return t(`marketing.s_${key.replace('marketing.', '')}_hint`)
}

function changed(item: SettingItem): boolean {
  const d = drafts.value[item.key]
  if (d === undefined) return false
  const v = fromDraft(item, d)
  return 'invalid' in v || !sameJSON(v.value, item.value)
}

const changedItems = computed(() => items.value.filter(changed))

/** Checked body of the changed keys, or null after showing the field errors. */
function body(): Record<string, unknown> | null {
  const out: Record<string, unknown> = {}
  const local: Record<string, string> = {}
  for (const item of changedItems.value) {
    const v = fromDraft(item, drafts.value[item.key]!)
    if ('invalid' in v) {
      local[item.key] = v.invalid
      continue
    }
    const problem = intOutOfRange(item, v.value) ? 'out_of_range' : intListProblem(item, v.value)
    if (problem) local[item.key] = problem
    else out[item.key] = v.value
  }
  fieldErrors.value = errors.translate(local)
  return Object.keys(local).length || Object.keys(out).length === 0 ? null : out
}

const summary = computed(() =>
  changedItems.value
    .map((i) => {
      const d = drafts.value[i.key]
      // An emptied test recipients list reads as a dash.
      const value = typeof d === 'boolean' ? (d ? t('marketing.s_on') : t('marketing.s_off')) : String(d).trim() || '—'
      return `${label(i.key)}: ${value}`
    })
    .join('; '),
)

function requestSave(): void {
  if (!canChange.value || !body()) return
  reasonOpen.value = true
}

async function save(reason: string): Promise<void> {
  const values = body()
  if (!values) return
  try {
    res.set(await marketingApi.updateSettings(values, reason))
    toast.success(t('settings.saved', { n: Object.keys(values).length }))
  } catch (err) {
    fieldErrors.value = errors.fields(err)
    throw err
  }
}

function discard(): void {
  drafts.value = Object.fromEntries(items.value.map((i) => [i.key, toDraft(i)]))
  fieldErrors.value = {}
}

onBeforeRouteLeave(async () => {
  if (changedItems.value.length === 0) return true
  return confirmAction({ title: t('common.unsaved_changes'), confirmLabel: t('common.discard'), destructive: true })
})
</script>

<template>
  <section class="jt-page settings">
    <StateView v-if="!res.data.value" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <p v-if="!canChange" class="hint" role="note">{{ t('marketing.s_readonly') }}</p>

      <Card :title="t('marketing.s_title')" :subtitle="t('marketing.s_hint')">
        <div v-for="item in switches" :key="item.key" class="row" :class="{ 'is-changed': changed(item) }">
          <div class="row__text">
            <span class="row__label">{{ label(item.key) }}</span>
            <span class="row__hint">{{ hint(item.key) }}</span>
            <span v-if="item.updated_at" class="row__meta">{{ t('marketing.s_changed_at', { time: fmt.dateTime(item.updated_at) }) }}</span>
          </div>
          <ToggleSwitch
            :id="item.key"
            :model-value="drafts[item.key] === true"
            :disabled="!canChange"
            :label="drafts[item.key] === true ? t('marketing.s_on') : t('marketing.s_off')"
            @update:model-value="(v) => (drafts[item.key] = v)"
          />
        </div>
      </Card>

      <Card :title="t('marketing.s_broadcast_title')">
        <div v-for="item in broadcastItems" :key="item.key" class="row" :class="{ 'is-changed': changed(item), 'row--list': item.type === 'int_list' }">
          <div class="row__text">
            <label class="row__label" :for="item.key">{{ label(item.key) }}</label>
            <span class="row__hint">
              {{ hint(item.key) }}
              <template v-if="item.type === 'int' && item.constraints.min !== undefined && item.constraints.max !== undefined"> · {{ t('settings.range', { min: item.constraints.min, max: item.constraints.max }) }}</template>
            </span>
            <span v-if="fieldErrors[item.key]" class="row__error" role="alert">{{ fieldErrors[item.key] }}</span>
          </div>
          <TextInput
            :id="item.key"
            class="row__input"
            :class="{ 'jt-mono': item.type === 'int_list' }"
            :model-value="String(drafts[item.key] ?? '')"
            :inputmode="item.type === 'int' ? 'numeric' : undefined"
            :disabled="!canChange"
            :invalid="Boolean(fieldErrors[item.key])"
            @update:model-value="(v) => (drafts[item.key] = v)"
          />
        </div>
      </Card>

      <div v-if="changedItems.length" class="savebar jt-glass" role="region" :aria-label="t('settings.unsaved', { n: changedItems.length })">
        <span>{{ t('settings.unsaved', { n: changedItems.length }) }}</span>
        <div class="jt-row">
          <AppButton variant="plain" @click="discard">{{ t('common.discard') }}</AppButton>
          <AppButton variant="primary" :disabled="!canChange" @click="requestSave">{{ t('common.save') }}</AppButton>
        </div>
      </div>

      <ReasonDialog
        v-model:open="reasonOpen"
        :title="t('marketing.s_save_title')"
        :message="t('marketing.s_save_message', { list: summary })"
        :placeholder="t('marketing.reason_placeholder')"
        :max-length="300"
        :min-length="3"
        :confirm-label="t('common.save')"
        :submit="save"
      />
    </template>
  </section>
</template>

<style scoped>
.settings {
  max-width: 920px;
  padding-bottom: 80px;
}

.hint {
  padding: 10px 14px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  color: var(--jt-color-label-secondary);
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-5);
  padding: var(--jt-space-3) 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.row:last-child {
  border-bottom: 0;
}

.row__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.row__label {
  font: var(--jt-font-body-strong);
}

.is-changed .row__label::after {
  content: ' •';
  color: var(--jt-color-blue);
}

.row__hint {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.row__meta {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}

.row__error {
  font: var(--jt-font-small);
  color: var(--jt-color-red-text);
}

.row__input {
  flex: none;
  width: 140px;
}

/* A list of Telegram ids takes the full width under its label. */
.row--list {
  flex-direction: column;
  align-items: stretch;
  gap: var(--jt-space-2);
}

.row--list .row__input {
  width: 100%;
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
