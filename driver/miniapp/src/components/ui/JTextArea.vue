<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Multi-line variant of JTextField (comments, cargo description). Grows with
  its content up to `maxRows`. The counter follows the backend rule (§11).
-->
<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId, watch } from 'vue'
import { cleanedLength } from '../../utils/text'

const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    placeholder?: string
    error?: string
    hint?: string
    max?: number
    rows?: number
    maxRows?: number
    disabled?: boolean
    readonly?: boolean
    enterkeyhint?: 'enter' | 'done' | 'go' | 'next' | 'previous' | 'search' | 'send'
  }>(),
  {
    label: '',
    placeholder: '',
    error: '',
    hint: '',
    max: undefined,
    rows: 3,
    maxRows: 8,
    disabled: false,
    readonly: false,
    enterkeyhint: undefined,
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  focus: [event: FocusEvent]
  blur: [event: FocusEvent]
}>()

const id = useId()
const area = ref<HTMLTextAreaElement | null>(null)
const length = computed(() => cleanedLength(props.modelValue))
const over = computed(() => props.max !== undefined && length.value > props.max)
const invalid = computed(() => Boolean(props.error) || over.value)
const describedBy = computed(() => [props.error || props.hint ? `${id}-msg` : '', props.max !== undefined ? `${id}-count` : ''].filter(Boolean).join(' ') || undefined)

const LINE_HEIGHT = 22
const PADDING_Y = 22

function autosize(): void {
  const el = area.value
  if (!el) return
  el.style.height = 'auto'
  const min = props.rows * LINE_HEIGHT + PADDING_Y
  const max = props.maxRows * LINE_HEIGHT + PADDING_Y
  el.style.height = `${Math.min(max, Math.max(min, el.scrollHeight))}px`
}

function onInput(e: Event): void {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
}

watch(
  () => props.modelValue,
  () => void nextTick(autosize),
)
onMounted(autosize)

defineExpose({ focus: () => area.value?.focus(), blur: () => area.value?.blur() })
</script>

<template>
  <div class="jt-area" :class="{ 'is-invalid': invalid, 'is-disabled': disabled }">
    <label v-if="label" :for="id" class="jt-area__label">{{ label }}</label>
    <textarea
      :id="id"
      ref="area"
      class="jt-area__input"
      :value="modelValue"
      :rows="rows"
      :placeholder="placeholder"
      :disabled="disabled"
      :readonly="readonly"
      :enterkeyhint="enterkeyhint"
      :aria-invalid="invalid || undefined"
      :aria-describedby="describedBy"
      @input="onInput"
      @focus="emit('focus', $event)"
      @blur="emit('blur', $event)"
    />
    <div v-if="error || hint || max !== undefined" class="jt-area__meta">
      <span v-if="error || hint" :id="`${id}-msg`" class="jt-area__message" :role="error ? 'alert' : undefined">
        {{ error || hint }}
      </span>
      <span v-if="max !== undefined" :id="`${id}-count`" class="jt-area__counter" :class="{ 'is-over': over }">
        {{ length }}/{{ max }}
      </span>
    </div>
  </div>
</template>

<style scoped>
.jt-area {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.jt-area__label {
  padding: 0 var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.jt-area__input {
  display: block;
  width: 100%;
  min-height: var(--jt-field-height);
  padding: 11px var(--jt-space-3);
  border: 0;
  outline: none;
  resize: none;
  border-radius: var(--jt-radius-field);
  background: var(--jt-color-fill-secondary);
  box-shadow: inset 0 0 0 1px transparent;
  font: var(--jt-font-body);
  font-size: 17px;
  color: var(--jt-color-label);
}

.jt-area__input::placeholder {
  color: var(--jt-color-label-tertiary);
}

.is-invalid .jt-area__input {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.jt-area__meta {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-2);
  padding: 0 var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.is-invalid .jt-area__message {
  color: var(--jt-color-red);
}

.jt-area__counter {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
}

.jt-area__counter.is-over {
  color: var(--jt-color-red);
}

.is-disabled {
  opacity: var(--jt-opacity-disabled);
}
</style>
