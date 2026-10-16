<!-- Native select styled like a macOS pop-up button. `null` is the empty choice. -->
<script setup lang="ts" generic="V extends string | number">
import { ChevronsUpDown } from '@lucide/vue'
import type { SelectOption } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: V | null
    options: SelectOption<V>[]
    /** Label of the empty choice; omit to require a value. */
    placeholder?: string
    invalid?: boolean
    disabled?: boolean
    /** Set on the <select> (FormField wiring). */
    id?: string
    ariaLabel?: string
  }>(),
  { placeholder: undefined, invalid: false, disabled: false, id: undefined, ariaLabel: undefined },
)

const emit = defineEmits<{ 'update:modelValue': [value: V | null] }>()

function onChange(e: Event): void {
  const idx = (e.target as HTMLSelectElement).selectedIndex - (props.placeholder !== undefined ? 1 : 0)
  emit('update:modelValue', idx >= 0 ? (props.options[idx]?.value ?? null) : null)
}
</script>

<template>
  <span class="select" :class="{ 'is-invalid': invalid, 'is-disabled': disabled }">
    <select :id="id" :disabled="disabled" :aria-invalid="invalid || undefined" :aria-label="ariaLabel" @change="onChange">
      <option v-if="placeholder !== undefined" :selected="modelValue === null" value="">{{ placeholder }}</option>
      <option
        v-for="opt in options"
        :key="String(opt.value)"
        :value="String(opt.value)"
        :selected="opt.value === modelValue"
        :disabled="opt.disabled"
      >
        {{ opt.label }}
      </option>
    </select>
    <ChevronsUpDown class="select__icon" :size="14" aria-hidden="true" />
  </span>
</template>

<style scoped>
.select {
  position: relative;
  display: inline-flex;
  min-width: 160px;
}

select {
  width: 100%;
  height: var(--jt-control-height);
  padding: 0 28px 0 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  appearance: none;
  outline: none;
  cursor: pointer;
}

select:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.is-invalid select {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.is-disabled {
  opacity: var(--jt-opacity-disabled);
}

.select__icon {
  position: absolute;
  right: 8px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--jt-color-label-secondary);
  pointer-events: none;
}
</style>
