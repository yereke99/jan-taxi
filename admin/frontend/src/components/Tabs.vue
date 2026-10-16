<!-- Capsule segmented tab bar (v-model, §15.1). Arrow keys move between tabs. -->
<script setup lang="ts" generic="V extends string">
import type { TabItem } from './types'

const props = defineProps<{ modelValue: V; tabs: TabItem<V>[]; ariaLabel?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: V] }>()

function onKeydown(e: KeyboardEvent): void {
  const step = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
  if (!step || props.tabs.length === 0) return
  e.preventDefault()
  const i = props.tabs.findIndex((t) => t.value === props.modelValue)
  const next = props.tabs[(i + step + props.tabs.length) % props.tabs.length]
  if (!next) return
  emit('update:modelValue', next.value)
  const buttons = (e.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role="tab"]')
  buttons[props.tabs.indexOf(next)]?.focus()
}
</script>

<template>
  <div class="tabs" role="tablist" :aria-label="ariaLabel" @keydown="onKeydown">
    <button
      v-for="tab in tabs"
      :key="tab.value"
      type="button"
      role="tab"
      class="tab"
      :class="{ 'is-active': tab.value === modelValue }"
      :aria-selected="tab.value === modelValue"
      :tabindex="tab.value === modelValue ? 0 : -1"
      @click="emit('update:modelValue', tab.value)"
    >
      {{ tab.label }}
      <span v-if="tab.count !== undefined" class="tab__count">{{ tab.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.tabs {
  display: inline-flex;
  align-self: flex-start;
  flex-wrap: wrap;
  gap: 2px;
  padding: 3px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-tertiary);
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  padding: 0 14px;
  border-radius: var(--jt-radius-round);
  color: var(--jt-color-label);
  font: 500 13px/18px var(--jt-font-family);
  white-space: nowrap;
  transition:
    background-color var(--jt-duration-fast) var(--jt-ease),
    box-shadow var(--jt-duration-fast) var(--jt-ease);
}

.tab:hover:not(.is-active) {
  background: rgba(255, 255, 255, 0.5);
}

.tab.is-active {
  background: var(--jt-color-bg);
  box-shadow: 0 1px 4px rgba(0, 0, 0, 0.12);
  font-weight: 600;
}

.tab__count {
  min-width: 20px;
  padding: 0 6px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-tertiary);
  font: 600 11px/16px var(--jt-font-family);
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.tab.is-active .tab__count {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}
</style>
