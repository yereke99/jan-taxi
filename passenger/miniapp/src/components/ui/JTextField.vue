<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  iOS fill-style text field (#F2F2F7, radius 10, 44px). With `max` it shows a
  live counter using the backend's rule (code points after cleaning, §11) and
  marks the field invalid when over — it never truncates what the user typed.
-->
<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { X } from '@lucide/vue'
import { cleanedLength } from '../../utils/text'

const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    placeholder?: string
    type?: 'text' | 'tel' | 'email' | 'search' | 'number' | 'url' | 'password'
    inputmode?: 'text' | 'tel' | 'email' | 'search' | 'numeric' | 'decimal' | 'url' | 'none'
    autocomplete?: string
    enterkeyhint?: 'enter' | 'done' | 'go' | 'next' | 'previous' | 'search' | 'send'
    /** Error message shown under the field (also sets aria-invalid). */
    error?: string
    hint?: string
    /** Maximum length in code points; shows the counter. */
    max?: number
    disabled?: boolean
    readonly?: boolean
    clearable?: boolean
  }>(),
  {
    label: '',
    placeholder: '',
    type: 'text',
    inputmode: undefined,
    autocomplete: 'off',
    enterkeyhint: undefined,
    error: '',
    hint: '',
    max: undefined,
    disabled: false,
    readonly: false,
    clearable: false,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  focus: [event: FocusEvent]
  blur: [event: FocusEvent]
  enter: []
}>()

const { t } = useI18n()
const id = useId()
const input = ref<HTMLInputElement | null>(null)
const length = computed(() => cleanedLength(props.modelValue))
const over = computed(() => props.max !== undefined && length.value > props.max)
const invalid = computed(() => Boolean(props.error) || over.value)
const describedBy = computed(() => [props.error || props.hint ? `${id}-msg` : '', props.max !== undefined ? `${id}-count` : ''].filter(Boolean).join(' ') || undefined)

function onInput(e: Event): void {
  emit('update:modelValue', (e.target as HTMLInputElement).value)
}

function clear(): void {
  emit('update:modelValue', '')
  input.value?.focus()
}

defineExpose({ focus: () => input.value?.focus(), blur: () => input.value?.blur() })
</script>

<template>
  <div class="jt-field" :class="{ 'is-invalid': invalid, 'is-disabled': disabled }">
    <label v-if="label" :for="id" class="jt-field__label">{{ label }}</label>
    <div class="jt-field__box">
      <input
        :id="id"
        ref="input"
        class="jt-field__input"
        :value="modelValue"
        :type="type"
        :inputmode="inputmode"
        :autocomplete="autocomplete"
        :enterkeyhint="enterkeyhint"
        :placeholder="placeholder"
        :disabled="disabled"
        :readonly="readonly"
        :aria-invalid="invalid || undefined"
        :aria-describedby="describedBy"
        @input="onInput"
        @focus="emit('focus', $event)"
        @blur="emit('blur', $event)"
        @keydown.enter="emit('enter')"
      />
      <button
        v-if="clearable && modelValue && !disabled && !readonly"
        type="button"
        class="jt-field__clear"
        :aria-label="t('common.clear')"
        @click="clear"
      >
        <X :size="14" :stroke-width="3" aria-hidden="true" />
      </button>
    </div>
    <div v-if="error || hint || max !== undefined" class="jt-field__meta">
      <span v-if="error || hint" :id="`${id}-msg`" class="jt-field__message" :role="error ? 'alert' : undefined">
        {{ error || hint }}
      </span>
      <span v-if="max !== undefined" :id="`${id}-count`" class="jt-field__counter" :class="{ 'is-over': over }">
        {{ length }}/{{ max }}
      </span>
    </div>
  </div>
</template>

<style scoped>
.jt-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.jt-field__label {
  padding: 0 var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.jt-field__box {
  display: flex;
  align-items: center;
  min-height: var(--jt-field-height);
  border-radius: var(--jt-radius-field);
  background: var(--jt-color-fill-secondary);
  box-shadow: inset 0 0 0 1px transparent;
  transition: box-shadow 120ms linear;
}

.is-invalid .jt-field__box {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.jt-field__input {
  flex: 1;
  min-width: 0;
  height: var(--jt-field-height);
  padding: 0 var(--jt-space-3);
  border: 0;
  outline: none;
  background: transparent;
  font: var(--jt-font-body);
  font-size: 17px;
  color: var(--jt-color-label);
}

.jt-field__input::placeholder {
  color: var(--jt-color-label-tertiary);
}

.jt-field__clear {
  display: flex;
  align-items: center;
  justify-content: center;
  width: var(--jt-tap-target);
  height: var(--jt-tap-target);
  color: var(--jt-color-bg);
}

.jt-field__clear :deep(svg) {
  box-sizing: content-box;
  padding: 2px;
  border-radius: 50%;
  background: var(--jt-color-label-tertiary);
}

.jt-field__meta {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-2);
  padding: 0 var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.is-invalid .jt-field__message {
  color: var(--jt-color-red);
}

.jt-field__counter {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}

.jt-field__counter.is-over {
  color: var(--jt-color-red);
}

.is-disabled {
  opacity: var(--jt-opacity-disabled);
}
</style>
