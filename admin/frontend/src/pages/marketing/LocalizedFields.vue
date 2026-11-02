<!--
  One text in Kazakh, Russian and English (campaign title and description,
  message text, button label). Errors arrive per language under the API's
  paths: "<name>.kk", "<name>.ru", "<name>.en".
-->
<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LocalizedText } from '../../api/common'
import { TextArea, TextInput } from '../../components'

const props = withDefaults(
  defineProps<{
    modelValue: LocalizedText
    label: string
    /** Field path for errors, e.g. "title" or "button.label". */
    name: string
    errors?: Record<string, string>
    hint?: string
    multiline?: boolean
    rows?: number
    /** Code-point counter for multi-line texts. */
    maxLength?: number
    required?: boolean
    disabled?: boolean
  }>(),
  { errors: () => ({}), hint: '', multiline: false, rows: 3, maxLength: 0, required: false, disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: LocalizedText] }>()

const { t } = useI18n()
const uid = useId()
const LANGS = ['kk', 'ru', 'en'] as const

function set(lang: keyof LocalizedText, v: string): void {
  emit('update:modelValue', { ...props.modelValue, [lang]: v })
}
</script>

<template>
  <fieldset class="loc" :disabled="disabled">
    <legend class="loc__label">
      {{ label }}
      <span v-if="required" class="loc__required" :title="t('common.required')" aria-hidden="true">*</span>
    </legend>
    <div v-for="lang in LANGS" :key="lang" class="loc__row">
      <label class="loc__tag" :for="`${uid}-${lang}`">{{ lang.toUpperCase() }}</label>
      <div class="loc__input">
        <TextArea
          v-if="multiline"
          :id="`${uid}-${lang}`"
          :model-value="modelValue[lang]"
          :rows="rows"
          :max-length="maxLength"
          :invalid="Boolean(errors[`${name}.${lang}`])"
          @update:model-value="(v) => set(lang, v)"
        />
        <TextInput
          v-else
          :id="`${uid}-${lang}`"
          :model-value="modelValue[lang]"
          :invalid="Boolean(errors[`${name}.${lang}`])"
          @update:model-value="(v) => set(lang, v)"
        />
        <p v-if="errors[`${name}.${lang}`]" class="loc__error" role="alert">{{ errors[`${name}.${lang}`] }}</p>
      </div>
    </div>
    <p v-if="errors[name]" class="loc__error" role="alert">{{ errors[name] }}</p>
    <p v-if="hint" class="loc__hint">{{ hint }}</p>
  </fieldset>
</template>

<style scoped>
.loc {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.loc__label {
  margin-bottom: 2px;
  padding: 0;
  font: 500 13px/18px var(--jt-font-family);
}

.loc__required {
  margin-left: 2px;
  color: var(--jt-color-red);
}

.loc__row {
  display: grid;
  grid-template-columns: 32px minmax(0, 1fr);
  align-items: start;
  gap: 8px;
}

.loc__tag {
  padding-top: 9px;
  font: 600 11px/16px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.loc__input {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.loc__hint {
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.loc__error {
  font: var(--jt-font-small);
  color: var(--jt-color-red);
}
</style>
