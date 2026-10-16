<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- iOS switch, 51×31, green when on (e.g. the driver's online toggle). -->
<script setup lang="ts">
import { haptics } from '../../telegram/haptics'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    /** Accessible name; required when no visible label is associated. */
    label?: string
    disabled?: boolean
    /** Shows a busy state while the change is being saved. */
    busy?: boolean
  }>(),
  { label: undefined, disabled: false, busy: false },
)

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

function toggle(): void {
  if (props.disabled || props.busy) return
  haptics.impact('light')
  emit('update:modelValue', !props.modelValue)
}
</script>

<template>
  <button
    type="button"
    role="switch"
    class="jt-switch"
    :class="{ 'is-on': modelValue, 'is-busy': busy }"
    :aria-checked="modelValue"
    :aria-label="label"
    :aria-busy="busy || undefined"
    :disabled="disabled"
    @click="toggle"
  >
    <span class="jt-switch__track" aria-hidden="true">
      <span class="jt-switch__knob" />
    </span>
  </button>
</template>

<style scoped>
.jt-switch {
  /* 44px hit area around the 51×31 control. */
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: var(--jt-tap-target);
  min-height: var(--jt-tap-target);
  margin: -6px 0;
}

.jt-switch__track {
  position: relative;
  display: block;
  width: var(--jt-switch-width);
  height: var(--jt-switch-height);
  border-radius: var(--jt-radius-round);
  background: rgba(120, 120, 128, 0.16);
  transition: background-color var(--jt-duration-base) var(--jt-ease);
}

.jt-switch__knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 27px;
  height: 27px;
  border-radius: 50%;
  background: #fff;
  box-shadow:
    0 3px 8px rgba(0, 0, 0, 0.15),
    0 3px 1px rgba(0, 0, 0, 0.06);
  transition: transform var(--jt-duration-base) var(--jt-ease);
}

.is-on .jt-switch__track {
  background: var(--jt-color-green);
}

.is-on .jt-switch__knob {
  transform: translateX(20px);
}

.is-busy {
  opacity: var(--jt-opacity-pressed);
}

.jt-switch:disabled {
  opacity: var(--jt-opacity-disabled);
  cursor: default;
}
</style>
