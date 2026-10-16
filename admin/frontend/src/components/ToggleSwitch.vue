<!-- iOS switch (green when on, §15) sized for desktop; a labelled checkbox underneath. -->
<script setup lang="ts">
withDefaults(defineProps<{ modelValue: boolean; label?: string; disabled?: boolean; id?: string }>(), {
  label: '',
  disabled: false,
  id: undefined,
})
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <label class="switch" :class="{ 'is-disabled': disabled }">
    <input
      :id="id"
      type="checkbox"
      role="switch"
      class="switch__input"
      :checked="modelValue"
      :disabled="disabled"
      :aria-checked="modelValue"
      @change="emit('update:modelValue', ($event.target as HTMLInputElement).checked)"
    />
    <span class="switch__track" aria-hidden="true"><span class="switch__thumb" /></span>
    <span v-if="label || $slots.default" class="switch__label"><slot>{{ label }}</slot></span>
  </label>
</template>

<style scoped>
.switch {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
  user-select: none;
}

.switch.is-disabled {
  opacity: var(--jt-opacity-disabled);
  cursor: default;
}

.switch__input {
  position: absolute;
  opacity: 0;
  width: 1px;
  height: 1px;
}

.switch__track {
  position: relative;
  flex: none;
  width: 40px;
  height: 24px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-tertiary);
  transition: background-color var(--jt-duration-base) var(--jt-ease);
}

.switch__thumb {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #ffffff;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.18);
  transition: transform var(--jt-duration-base) var(--jt-ease-spring);
}

.switch__input:checked + .switch__track {
  background: var(--jt-color-green);
}

.switch__input:checked + .switch__track .switch__thumb {
  transform: translateX(16px);
}

.switch__input:focus-visible + .switch__track {
  outline: 2px solid var(--jt-color-blue);
  outline-offset: 2px;
}

.switch__label {
  font: var(--jt-font-body);
}
</style>
