<!-- Local date and time (datetime-local, the operator's time zone); '' when empty. -->
<script setup lang="ts">
withDefaults(defineProps<{ modelValue: string; id?: string; invalid?: boolean; min?: string; disabled?: boolean }>(), {
  id: undefined,
  invalid: false,
  min: undefined,
  disabled: false,
})
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <input
    :id="id"
    class="dt"
    type="datetime-local"
    :value="modelValue"
    :min="min"
    :disabled="disabled"
    :aria-invalid="invalid || undefined"
    @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
  />
</template>

<style scoped>
.dt {
  height: var(--jt-control-height-lg);
  padding: 0 10px;
  border: 0;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  font: var(--jt-font-body);
  outline: none;
}

.dt:focus {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.dt[aria-invalid='true'] {
  box-shadow: inset 0 0 0 1px var(--jt-color-red);
}

.dt:disabled {
  opacity: var(--jt-opacity-disabled);
}
</style>
