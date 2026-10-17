<!--
  Fare picker: an iOS-style stepper over the quote's allowed fares. The value
  rolls like a picker wheel; it can never go below the quote minimum or above
  the maximum (domain/fare). Arrow keys, Page Up/Down, Home/End work on the
  focused value (role="spinbutton").
-->
<script setup lang="ts">
import { Minus, Plus } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FareOptions } from '../../api/types'
import { allowedFares, canDecreaseFare, canIncreaseFare, clampFare, stepFare } from '../../domain/fare'
import { haptics } from '../../telegram'
import { formatMoney, type Locale } from '../../utils/format'

const props = defineProps<{
  fare: FareOptions
  modelValue: number | null
  locale: Locale
  error?: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: number] }>()

const { t } = useI18n()
const value = computed(() => clampFare(props.fare, props.modelValue))
const list = computed(() => allowedFares(props.fare))
const raisedBy = computed(() => value.value - props.fare.recommended)
const direction = ref<'up' | 'down'>('up')

watch(value, (next, prev) => {
  direction.value = prev === undefined || next >= prev ? 'up' : 'down'
})

function set(next: number): void {
  if (next === value.value) return
  haptics.selection()
  emit('update:modelValue', next)
}

function step(dir: 1 | -1): void {
  set(stepFare(props.fare, value.value, dir))
}

function onKeydown(e: KeyboardEvent): void {
  const l = list.value
  if (l.length === 0) return
  const i = l.indexOf(value.value)
  let next: number | undefined
  switch (e.key) {
    case 'ArrowUp':
    case 'ArrowRight':
      next = stepFare(props.fare, value.value, 1)
      break
    case 'ArrowDown':
    case 'ArrowLeft':
      next = stepFare(props.fare, value.value, -1)
      break
    case 'PageUp':
      next = l[Math.min(l.length - 1, i + 5)]
      break
    case 'PageDown':
      next = l[Math.max(0, i - 5)]
      break
    case 'Home':
      next = l[0]
      break
    case 'End':
      next = l[l.length - 1]
      break
    default:
      return
  }
  e.preventDefault()
  if (next !== undefined) set(next)
}
</script>

<template>
  <div class="fare" :class="{ 'has-error': error }">
    <div class="fare__row">
      <button
        type="button"
        class="fare__step"
        :disabled="!canDecreaseFare(fare, value)"
        :aria-label="t('confirm.fare_decrease')"
        @click="step(-1)"
      >
        <Minus :size="22" :stroke-width="2.25" aria-hidden="true" />
      </button>

      <div
        class="fare__value"
        role="spinbutton"
        tabindex="0"
        :aria-label="t('confirm.fare')"
        :aria-valuemin="list[0]"
        :aria-valuemax="list[list.length - 1]"
        :aria-valuenow="value"
        :aria-valuetext="formatMoney(value, locale)"
        @keydown="onKeydown"
      >
        <span class="fare__window">
          <Transition :name="`fare-roll-${direction}`">
            <span :key="value" class="fare__amount jt-tabular">{{ formatMoney(value, locale) }}</span>
          </Transition>
        </span>
        <span v-if="raisedBy > 0" class="fare__note">{{ t('confirm.fare_raised', { amount: formatMoney(raisedBy, locale) }) }}</span>
      </div>

      <button
        type="button"
        class="fare__step"
        :disabled="!canIncreaseFare(fare, value)"
        :aria-label="t('confirm.fare_increase')"
        @click="step(1)"
      >
        <Plus :size="22" :stroke-width="2.25" aria-hidden="true" />
      </button>
    </div>
    <p v-if="error" class="fare__error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.fare__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.fare__step {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 52px;
  height: 52px;
  border-radius: 50%;
  background: var(--jt-fare-step-bg, var(--jt-color-fill-secondary));
  color: var(--jt-color-label);
  transition: opacity 120ms linear;
}

.fare__step:active:not(:disabled) {
  opacity: var(--jt-opacity-pressed);
}

.fare__step:disabled {
  opacity: var(--jt-opacity-disabled);
}

.fare__value {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  border-radius: 14px;
  outline-offset: 4px;
}

.fare__window {
  position: relative;
  display: block;
  width: 100%;
  height: 34px;
  overflow: hidden;
  text-align: center;
}

.fare__amount {
  position: absolute;
  inset: 0;
  font: var(--jt-font-title1);
}

.fare__note {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-align: center;
}

.fare__error {
  margin-top: var(--jt-space-2);
  font: var(--jt-font-footnote);
  color: var(--jt-color-red-text);
  text-align: center;
}

.fare-roll-up-enter-active,
.fare-roll-up-leave-active,
.fare-roll-down-enter-active,
.fare-roll-down-leave-active {
  transition:
    transform var(--jt-duration-base) var(--jt-ease-spring),
    opacity var(--jt-duration-fast) linear;
}

.fare-roll-up-enter-from,
.fare-roll-down-leave-to {
  transform: translateY(70%);
  opacity: 0;
}

.fare-roll-up-leave-to,
.fare-roll-down-enter-from {
  transform: translateY(-70%);
  opacity: 0;
}
</style>
