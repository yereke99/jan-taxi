<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Floating Liquid Glass tab bar (§15.1): a capsule 8px above the bottom safe
  area with 24px icons and 11px semibold labels; the selected item sits in a
  darker glass pill. `hidden` slides it away (e.g. during an active trip
  action sheet). Scrolling pages reserve space with
  padding-bottom: calc(var(--jt-tabbar-height) + var(--jt-safe-bottom) + 16px).

  v-model  selected item value; `select` also fires when the current item is
  tapped again (scroll-to-top / pop-to-root).
-->
<script setup lang="ts" generic="V extends string">
import { haptics } from '../../telegram/haptics'
import type { TabBarItem } from './types'

const props = withDefaults(
  defineProps<{
    modelValue: V
    items: TabBarItem<V>[]
    hidden?: boolean
    ariaLabel?: string
  }>(),
  { hidden: false, ariaLabel: undefined },
)

const emit = defineEmits<{
  'update:modelValue': [value: V]
  select: [value: V]
}>()

function choose(item: TabBarItem<V>): void {
  if (item.value !== props.modelValue) {
    haptics.selection()
    emit('update:modelValue', item.value)
  }
  emit('select', item.value)
}

function badgeText(b: TabBarItem<V>['badge']): string {
  return typeof b === 'number' && b > 0 ? (b > 99 ? '99+' : String(b)) : ''
}
</script>

<template>
  <Transition name="jt-tabbar">
    <nav v-show="!hidden" class="jt-tabbar jt-glass" :aria-label="ariaLabel">
      <button
        v-for="item in items"
        :key="item.value"
        type="button"
        class="jt-tabbar__item"
        :class="{ 'is-selected': item.value === modelValue }"
        :aria-current="item.value === modelValue ? 'page' : undefined"
        @click="choose(item)"
      >
        <span class="jt-tabbar__icon">
          <component :is="item.icon" :size="24" :stroke-width="item.value === modelValue ? 2.25 : 1.9" aria-hidden="true" />
          <span v-if="item.badge" class="jt-tabbar__badge" :class="{ 'is-dot': !badgeText(item.badge) }">
            {{ badgeText(item.badge) }}
          </span>
        </span>
        <span class="jt-tabbar__label">{{ item.label }}</span>
      </button>
    </nav>
  </Transition>
</template>

<style scoped>
.jt-tabbar {
  position: fixed;
  left: calc(var(--jt-safe-left) + var(--jt-side-margin));
  right: calc(var(--jt-safe-right) + var(--jt-side-margin));
  bottom: calc(max(var(--jt-safe-bottom), var(--jt-float-inset)) + var(--jt-float-inset));
  z-index: var(--jt-z-navbar);
  display: flex;
  align-items: stretch;
  gap: 2px;
  max-width: 480px;
  height: var(--jt-tabbar-height);
  margin: 0 auto;
  padding: 4px;
  border-radius: var(--jt-radius-capsule);
}

.jt-tabbar__item {
  position: relative;
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  border-radius: var(--jt-radius-capsule);
  color: var(--jt-color-label-secondary);
  transition:
    background-color var(--jt-duration-base) var(--jt-ease),
    color var(--jt-duration-base) var(--jt-ease);
}

.jt-tabbar__item.is-selected {
  background: var(--jt-glass-selected);
  color: var(--jt-color-label);
}

.jt-tabbar__item:active {
  opacity: var(--jt-opacity-pressed);
}

.jt-tabbar__icon {
  position: relative;
  display: flex;
}

.jt-tabbar__label {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font: 600 11px/13px var(--jt-font-family);
}

.jt-tabbar__badge {
  position: absolute;
  top: -4px;
  left: 16px;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-red);
  color: #fff;
  font: 600 12px/18px var(--jt-font-family);
  text-align: center;
}

.jt-tabbar__badge.is-dot {
  top: -2px;
  left: 18px;
  min-width: 10px;
  width: 10px;
  height: 10px;
  padding: 0;
}

.jt-tabbar-enter-active,
.jt-tabbar-leave-active {
  transition:
    transform var(--jt-duration-spring) var(--jt-ease-spring),
    opacity var(--jt-duration-fast) var(--jt-ease);
}

.jt-tabbar-enter-from,
.jt-tabbar-leave-to {
  transform: translateY(calc(100% + 24px));
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .jt-tabbar-enter-active,
  .jt-tabbar-leave-active,
  .jt-tabbar__item {
    transition: none;
  }
}
</style>
