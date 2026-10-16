<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  iOS 26 capsule button (§15, §15.1): primary = black fill/white label,
  secondary = gray fill, plain = text only, destructive = gray fill with red
  label. Large is 52px. Pressed .55, disabled .3. Primary taps fire a light
  haptic.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { haptics } from '../../telegram/haptics'
import JSpinner from './JSpinner.vue'
import type { ButtonSize, ButtonVariant } from './types'

const props = withDefaults(
  defineProps<{
    variant?: ButtonVariant
    size?: ButtonSize
    block?: boolean
    loading?: boolean
    disabled?: boolean
    type?: 'button' | 'submit'
    /** Accessible label when the button has only an icon. */
    ariaLabel?: string
  }>(),
  { variant: 'primary', size: 'large', block: false, loading: false, disabled: false, type: 'button', ariaLabel: undefined },
)

const emit = defineEmits<{ click: [event: MouseEvent] }>()

const inactive = computed(() => props.disabled || props.loading)

function onClick(e: MouseEvent): void {
  if (inactive.value) return
  if (props.variant === 'primary') haptics.impact('light')
  emit('click', e)
}
</script>

<template>
  <button
    :type="type"
    class="jt-button"
    :class="[`jt-button--${variant}`, `jt-button--${size}`, { 'jt-button--block': block, 'is-loading': loading }]"
    :disabled="disabled"
    :aria-disabled="inactive || undefined"
    :aria-busy="loading || undefined"
    :aria-label="ariaLabel"
    @click="onClick"
  >
    <span class="jt-button__content">
      <slot name="icon" />
      <span v-if="$slots.default" class="jt-button__label"><slot /></span>
    </span>
    <span v-if="loading" class="jt-button__spinner">
      <JSpinner :color="variant === 'primary' ? 'light' : 'dark'" />
    </span>
  </button>
</template>

<style scoped>
.jt-button {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: var(--jt-tap-target);
  border-radius: var(--jt-radius-button);
  font: var(--jt-font-headline);
  white-space: nowrap;
  transition: opacity 120ms linear;
  user-select: none;
  -webkit-user-select: none;
}

.jt-button--large {
  height: var(--jt-button-height);
  padding: 0 var(--jt-space-5);
}

.jt-button--medium {
  height: var(--jt-button-height-medium);
  padding: 0 var(--jt-space-4);
}

.jt-button--small {
  height: var(--jt-button-height-small);
  min-height: var(--jt-button-height-small);
  padding: 0 var(--jt-space-3);
  font: 600 15px/20px var(--jt-font-family);
}

.jt-button--block {
  display: flex;
  width: 100%;
}

.jt-button--primary {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.jt-button--secondary {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label);
}

.jt-button--plain {
  background: transparent;
  color: var(--jt-color-label);
  font-weight: 400;
}

.jt-button--destructive {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-red);
}

.jt-button:active:not(:disabled):not(.is-loading) {
  opacity: var(--jt-opacity-pressed);
}

.jt-button:disabled {
  opacity: var(--jt-opacity-disabled);
  cursor: default;
}

.jt-button__content {
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-2);
  min-width: 0;
}

.jt-button__label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.is-loading .jt-button__content {
  visibility: hidden;
}

.jt-button__spinner {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
