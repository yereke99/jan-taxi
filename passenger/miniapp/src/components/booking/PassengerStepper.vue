<!--
  Passenger count for a taxi order (ARCHITECTURE §21.5): an inset row with
  the label, the count and an iOS stepper (− | +) bounded by min..max.
-->
<script setup lang="ts">
import { Minus, Plus, Users } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { haptics } from '../../telegram'

const props = withDefaults(defineProps<{ modelValue: number; min?: number; max: number; disabled?: boolean }>(), { min: 1, disabled: false })
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()

const { t } = useI18n()

function step(dir: 1 | -1): void {
  const next = props.modelValue + dir
  if (props.disabled || next < props.min || next > props.max) return
  haptics.selection()
  emit('update:modelValue', next)
}
</script>

<template>
  <div class="pax">
    <Users :size="22" :stroke-width="1.8" class="pax__icon" aria-hidden="true" />
    <span class="pax__label">{{ t('confirm.passengers') }}</span>
    <output class="pax__value jt-tabular" aria-live="polite">{{ modelValue }}</output>
    <span class="pax__stepper">
      <button
        type="button"
        class="pax__key"
        :aria-label="t('confirm.passengers_decrease')"
        :disabled="disabled || modelValue <= min"
        @click="step(-1)"
      >
        <Minus :size="20" :stroke-width="2.25" aria-hidden="true" />
      </button>
      <span class="pax__divider" aria-hidden="true" />
      <button
        type="button"
        class="pax__key"
        :aria-label="t('confirm.passengers_increase')"
        :disabled="disabled || modelValue >= max"
        @click="step(1)"
      >
        <Plus :size="20" :stroke-width="2.25" aria-hidden="true" />
      </button>
    </span>
  </div>
</template>

<style scoped>
.pax {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 52px;
  padding: 0 var(--jt-space-3) 0 var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.pax__icon {
  flex: none;
}

.pax__label {
  flex: 1;
  min-width: 0;
  font: var(--jt-font-body);
}

.pax__value {
  min-width: 2ch;
  font: var(--jt-font-headline);
  text-align: right;
}

.pax__stepper {
  flex: none;
  display: flex;
  align-items: center;
  height: 34px;
  border-radius: 9px;
  background: var(--jt-color-fill-tertiary);
}

.pax__key {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  height: 100%;
  color: var(--jt-color-label);
}

/* 44px tall touch area around the 34px stepper. */
.pax__key::after {
  content: '';
  position: absolute;
  inset: -5px 0;
}

.pax__key:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
}

.pax__key:disabled {
  opacity: var(--jt-opacity-disabled);
}

.pax__divider {
  width: 1px;
  height: 18px;
  background: var(--jt-color-separator);
}
</style>
