<!-- Capsule button (§15.1): primary black, secondary gray, plain text, destructive red label. -->
<script setup lang="ts">
import AppSpinner from './AppSpinner.vue'
import type { ButtonSize, ButtonVariant } from './types'

const props = withDefaults(
  defineProps<{
    variant?: ButtonVariant
    size?: ButtonSize
    type?: 'button' | 'submit'
    loading?: boolean
    disabled?: boolean
    block?: boolean
  }>(),
  { variant: 'secondary', size: 'md', type: 'button', loading: false, disabled: false, block: false },
)

const emit = defineEmits<{ click: [event: MouseEvent] }>()

function onClick(e: MouseEvent): void {
  if (!props.loading && !props.disabled) emit('click', e)
}
</script>

<template>
  <button
    :type="type"
    class="btn"
    :class="[`btn--${variant}`, `btn--${size}`, { 'btn--block': block, 'is-loading': loading }]"
    :disabled="disabled"
    :aria-busy="loading || undefined"
    @click="onClick"
  >
    <AppSpinner v-if="loading" :size="14" />
    <slot name="icon" />
    <span v-if="$slots.default" class="btn__label"><slot /></span>
  </button>
</template>

<style scoped>
.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: var(--jt-control-height);
  padding: 0 16px;
  border-radius: var(--jt-radius-round);
  font: 500 14px/20px var(--jt-font-family);
  white-space: nowrap;
  user-select: none;
  transition:
    opacity 120ms linear,
    background-color 120ms linear;
}

.btn--sm {
  height: var(--jt-control-height-sm);
  padding: 0 12px;
  font-size: 13px;
}

.btn--lg {
  height: var(--jt-control-height-lg);
  padding: 0 20px;
  font-weight: 600;
}

.btn--block {
  display: flex;
  width: 100%;
}

.btn--primary {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.btn--primary:hover:not(:disabled) {
  background: #1c1c1e;
}

.btn--danger {
  background: var(--jt-color-red);
  color: #ffffff;
}

.btn--secondary {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.btn--plain {
  background: transparent;
  color: var(--jt-color-label);
}

.btn--plain:hover:not(:disabled) {
  background: var(--jt-color-hover);
}

.btn--destructive {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-red);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.btn--secondary:hover:not(:disabled),
.btn--destructive:hover:not(:disabled) {
  background: var(--jt-color-fill-tertiary);
}

.btn:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
}

.btn:disabled {
  opacity: var(--jt-opacity-disabled);
  cursor: default;
}

.is-loading {
  cursor: progress;
}
</style>
