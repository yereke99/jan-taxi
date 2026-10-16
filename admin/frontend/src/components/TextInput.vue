<script setup lang="ts">
import { ref } from 'vue'

withDefaults(
  defineProps<{
    modelValue: string
    type?: 'text' | 'email' | 'password' | 'search' | 'tel' | 'number' | 'url'
    placeholder?: string
    invalid?: boolean
    disabled?: boolean
    readonly?: boolean
    autocomplete?: string
    inputmode?: 'text' | 'email' | 'tel' | 'numeric' | 'decimal' | 'search' | 'url'
  }>(),
  {
    type: 'text',
    placeholder: '',
    invalid: false,
    disabled: false,
    readonly: false,
    autocomplete: 'off',
    inputmode: undefined,
  },
)

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const input = ref<HTMLInputElement | null>(null)

defineExpose({ focus: () => input.value?.focus() })
</script>

<template>
  <input
    ref="input"
    class="input"
    :class="{ 'is-invalid': invalid }"
    :value="modelValue"
    :type="type"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :autocomplete="autocomplete"
    :inputmode="inputmode"
    :aria-invalid="invalid || undefined"
    @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
  />
</template>

<style scoped>
.input {
  width: 100%;
  height: var(--jt-control-height-lg);
  padding: 0 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  outline: none;
  transition: box-shadow 120ms linear;
}

.input:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.input.is-invalid {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.input:disabled {
  opacity: var(--jt-opacity-disabled);
}

.input::placeholder {
  color: var(--jt-color-label-tertiary);
}
</style>
