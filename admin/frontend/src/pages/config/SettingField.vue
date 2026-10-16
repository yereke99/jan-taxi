<!-- One runtime setting rendered by its type (int, bool, text, URL, localized text, lists). -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LocalizedText } from '../../api/common'
import type { SettingItem } from '../../api/config'
import { TextArea, TextInput, ToggleSwitch } from '../../components'
import { useFormat } from '../../composables/useFormat'
import type { Draft } from './settingsModel'

const props = defineProps<{ item: SettingItem; modelValue: Draft; error?: string; changed: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: Draft]; reset: [] }>()

const { t, te } = useI18n()
const fmt = useFormat()
const custom = ref('')

const labelKey = computed(() => `settings.key_${props.item.key.replace(/\./g, '_')}`)
const label = computed(() => (te(labelKey.value, 'kk') ? t(labelKey.value) : props.item.key))
const description = computed(() => fmt.name(props.item.description))
const c = computed(() => props.item.constraints)
const range = computed(() => (c.value.min !== undefined && c.value.max !== undefined ? t('settings.range', { min: c.value.min, max: c.value.max }) : ''))

const sourceLabel = computed(() => t(`settings.source_${props.item.source}`))

/** Known prohibited-item codes (translated); stored codes outside the list are kept and shown too. */
const KNOWN_CODES = ['weapons', 'explosives', 'drugs', 'alcohol_tobacco', 'money_valuables', 'animals', 'hazardous', 'illegal_goods']
const codes = computed(() => [...new Set([...KNOWN_CODES, ...((props.modelValue as string[]) ?? [])])])

function setText(lang: keyof LocalizedText, v: string): void {
  emit('update:modelValue', { ...(props.modelValue as LocalizedText), [lang]: v })
}

function toggleCode(code: string, on: boolean): void {
  const list = (props.modelValue as string[]).filter((x) => x !== code)
  emit('update:modelValue', on ? [...list, code] : list)
}

function addCustom(): void {
  const code = custom.value.trim().toLowerCase()
  if (!code) return
  const list = props.modelValue as string[]
  if (!list.includes(code)) emit('update:modelValue', [...list, code])
  custom.value = ''
}

function codeLabel(code: string): string {
  return te(`prohibited_items.${code}`, 'kk') ? t(`prohibited_items.${code}`) : code
}
</script>

<template>
  <div class="setting" :class="{ 'is-changed': changed }">
    <div class="setting__head">
      <label class="setting__label" :for="item.key">{{ label }}</label>
      <span class="setting__source" :title="item.updated_at ? fmt.dateTime(item.updated_at) : ''">{{ sourceLabel }}</span>
      <button v-if="item.source === 'database'" type="button" class="jt-link-button setting__reset" @click="emit('reset')">{{ t('settings.reset_default') }}</button>
    </div>
    <p class="setting__desc">{{ description }}<template v-if="range"> · {{ range }}</template></p>

    <ToggleSwitch v-if="item.type === 'bool'" :id="item.key" :model-value="modelValue as boolean" :label="(modelValue as boolean) ? t('common.yes') : t('common.no')" @update:model-value="(v) => emit('update:modelValue', v)" />

    <div v-else-if="item.type === 'localized_text'" class="setting__langs">
      <label v-for="lang in ['kk', 'ru', 'en'] as const" :key="lang" class="setting__lang">
        <span class="setting__lang-tag">{{ lang.toUpperCase() }}</span>
        <TextArea
          v-if="(c.max_length ?? 0) > 120"
          :id="lang === 'kk' ? item.key : undefined"
          :model-value="(modelValue as LocalizedText)[lang]"
          :rows="3"
          :max-length="c.max_length"
          :invalid="Boolean(error)"
          @update:model-value="(v) => setText(lang, v)"
        />
        <TextInput v-else :id="lang === 'kk' ? item.key : undefined" :model-value="(modelValue as LocalizedText)[lang]" :invalid="Boolean(error)" @update:model-value="(v) => setText(lang, v)" />
      </label>
    </div>

    <div v-else-if="item.type === 'code_list'" class="setting__codes">
      <label v-for="code in codes" :key="code" class="setting__code">
        <input type="checkbox" :checked="(modelValue as string[]).includes(code)" @change="toggleCode(code, ($event.target as HTMLInputElement).checked)" />
        <span>{{ codeLabel(code) }}</span>
        <span class="jt-mono jt-muted">{{ code }}</span>
      </label>
      <div class="setting__custom">
        <TextInput v-model="custom" :placeholder="t('settings.custom_code')" :aria-label="t('settings.custom_code')" @keydown.enter.prevent="addCustom" />
        <button type="button" class="jt-link-button" @click="addCustom">{{ t('common.add') }}</button>
      </div>
    </div>

    <TextArea
      v-else-if="item.type === 'string_list'"
      :id="item.key"
      :model-value="(modelValue as string[]).join('\n')"
      :rows="5"
      :invalid="Boolean(error)"
      @update:model-value="(v) => emit('update:modelValue', v.split('\n'))"
    />

    <TextInput
      v-else
      :id="item.key"
      :model-value="modelValue as string"
      :type="item.type === 'url' ? 'url' : 'text'"
      :inputmode="item.type === 'int' ? 'numeric' : undefined"
      :placeholder="item.type === 'int_list' ? t('settings.int_list_hint') : ''"
      :invalid="Boolean(error)"
      @update:model-value="(v) => emit('update:modelValue', v)"
    />
    <p v-if="item.type === 'string_list'" class="setting__desc">{{ t('settings.one_per_line') }}</p>
    <p v-if="error" class="setting__error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.setting {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: var(--jt-space-3) 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.setting:last-child {
  border-bottom: 0;
}

.setting__head {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
}

.setting__label {
  font: var(--jt-font-body-strong);
}

.is-changed .setting__label::after {
  content: ' •';
  color: var(--jt-color-blue);
}

.setting__source {
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  font: 500 11px/18px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.setting__reset {
  margin-left: auto;
  font: var(--jt-font-small);
}

.setting__desc {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.setting__langs {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.setting__lang {
  display: grid;
  grid-template-columns: 32px 1fr;
  align-items: start;
  gap: 8px;
}

.setting__lang-tag {
  padding-top: 8px;
  font: 600 11px/16px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.setting__codes {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px var(--jt-space-4);
}

.setting__code {
  display: flex;
  align-items: center;
  gap: 8px;
}

.setting__code input {
  accent-color: #000;
}

.setting__custom {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  max-width: 360px;
}

.setting__error {
  font: var(--jt-font-small);
  color: var(--jt-color-red-text);
}
</style>
