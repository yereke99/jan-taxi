<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  iOS 26 segmented control (e.g. Taxi / Parcel / Other): capsule track and a
  sliding white thumb with a spring (§15.1). `glass` makes the track glass for
  use directly over the map. Arrow keys move the selection.
-->
<script setup lang="ts" generic="V extends string">
import { computed } from 'vue'
import { haptics } from '../../telegram/haptics'
import type { SegmentedOption } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: V
    options: SegmentedOption<V>[]
    ariaLabel?: string
    disabled?: boolean
    /** Glass track for use over the map (§15.1). */
    glass?: boolean
  }>(),
  { ariaLabel: undefined, disabled: false, glass: false },
)

const emit = defineEmits<{
  'update:modelValue': [value: V]
  change: [value: V]
}>()

const index = computed(() => Math.max(0, props.options.findIndex((o) => o.value === props.modelValue)))
const thumbStyle = computed(() => ({
  width: `calc((100% - 6px) / ${props.options.length || 1})`,
  transform: `translateX(${index.value * 100}%)`,
}))

function select(opt: SegmentedOption<V>): void {
  if (props.disabled || opt.disabled || opt.value === props.modelValue) return
  haptics.selection()
  emit('update:modelValue', opt.value)
  emit('change', opt.value)
}

function onKeydown(e: KeyboardEvent): void {
  const step = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
  if (!step) return
  e.preventDefault()
  const n = props.options.length
  for (let k = 1; k <= n; k++) {
    const opt = props.options[(index.value + step * k + n * k) % n]
    if (opt && !opt.disabled) {
      select(opt)
      const buttons = (e.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('button')
      buttons[props.options.indexOf(opt)]?.focus()
      return
    }
  }
}
</script>

<template>
  <div
    class="jt-segmented"
    :class="{ 'is-disabled': disabled, 'jt-glass': glass }"
    role="radiogroup"
    :aria-label="ariaLabel"
    @keydown="onKeydown"
  >
    <span class="jt-segmented__thumb" :style="thumbStyle" aria-hidden="true" />
    <button
      v-for="opt in options"
      :key="opt.value"
      type="button"
      role="radio"
      class="jt-segmented__item"
      :class="{ 'is-selected': opt.value === modelValue }"
      :aria-checked="opt.value === modelValue"
      :tabindex="opt.value === modelValue ? 0 : -1"
      :disabled="disabled || opt.disabled"
      @click="select(opt)"
    >
      {{ opt.label }}
    </button>
  </div>
</template>

<style scoped>
.jt-segmented {
  position: relative;
  display: flex;
  height: var(--jt-segmented-height);
  padding: 3px;
  border-radius: var(--jt-radius-segmented);
  user-select: none;
  -webkit-user-select: none;
}

.jt-segmented:not(.jt-glass) {
  background: rgba(118, 118, 128, 0.12);
}

.jt-segmented.is-disabled {
  opacity: var(--jt-opacity-disabled);
}

.jt-segmented__thumb {
  position: absolute;
  top: 3px;
  bottom: 3px;
  left: 3px;
  border-radius: var(--jt-radius-segment);
  background: var(--jt-color-bg);
  box-shadow:
    0 3px 8px rgba(0, 0, 0, 0.12),
    0 3px 1px rgba(0, 0, 0, 0.04);
  transition: transform var(--jt-duration-spring) var(--jt-ease-spring);
}

.jt-segmented__item {
  position: relative;
  flex: 1 1 0;
  min-width: 0;
  /* The visible control is 36px; the hit area extends to 44px. */
  margin: -4px 0;
  padding: 4px var(--jt-space-2);
  font: 500 14px/18px var(--jt-font-family);
  color: var(--jt-color-label);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.jt-segmented__item.is-selected {
  font-weight: 600;
}

.jt-segmented__item:disabled {
  color: var(--jt-color-label-tertiary);
}

@media (prefers-reduced-motion: reduce) {
  .jt-segmented__thumb {
    transition: none;
  }
}
</style>
