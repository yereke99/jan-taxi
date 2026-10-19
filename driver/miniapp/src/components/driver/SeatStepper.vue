<!--
  iOS-style stepper (UIStepper): a gray capsule split into − and +, bounded
  by min/max. The value itself is shown by the caller next to it.
-->
<script setup lang="ts">
import { Minus, Plus } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { haptics } from '../../telegram'

const props = withDefaults(defineProps<{ modelValue: number; min?: number; max: number; disabled?: boolean; label: string }>(), {
  min: 1,
  disabled: false,
})
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()

const { t } = useI18n()
const canDec = computed(() => !props.disabled && props.modelValue > props.min)
const canInc = computed(() => !props.disabled && props.modelValue < props.max)

function step(delta: number): void {
  const next = Math.min(props.max, Math.max(props.min, props.modelValue + delta))
  if (next === props.modelValue) return
  haptics.selection()
  emit('update:modelValue', next)
}
</script>

<template>
  <div class="stepper" role="group" :aria-label="label">
    <button type="button" class="stepper__btn" :disabled="!canDec" :aria-label="t('registration.vehicle_class.seats_less')" @click="step(-1)">
      <Minus :size="20" :stroke-width="2.25" aria-hidden="true" />
    </button>
    <span class="stepper__sep" aria-hidden="true" />
    <button type="button" class="stepper__btn" :disabled="!canInc" :aria-label="t('registration.vehicle_class.seats_more')" @click="step(1)">
      <Plus :size="20" :stroke-width="2.25" aria-hidden="true" />
    </button>
  </div>
</template>

<style scoped>
.stepper {
  display: inline-flex;
  align-items: center;
  height: 36px;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-color-fill-secondary);
  user-select: none;
}

.stepper__btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 52px;
  height: 100%;
  color: var(--jt-color-label);
  touch-action: manipulation;
}

.stepper__btn:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
}

.stepper__btn:disabled {
  opacity: var(--jt-opacity-disabled);
}

.stepper__sep {
  width: var(--jt-hairline);
  height: 18px;
  background: var(--jt-color-separator);
}
</style>
