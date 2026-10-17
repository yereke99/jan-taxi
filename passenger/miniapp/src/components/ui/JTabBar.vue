<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  Floating Liquid Glass tab bar (§15.1): a borderless capsule 8px above the
  bottom safe area with 24px icons and 11px semibold labels. The selected item
  sits in a glass droplet that glides between items: a transform-only spring
  (translate + a slight horizontal stretch mid-way), so switching never
  touches layout and stays at 60 fps. `hidden` slides the bar away (full-screen
  flows whose primary action it would cover). Scrolling pages reserve space
  with padding-bottom: calc(var(--jt-tabbar-height) + var(--jt-safe-bottom) + 16px).

  v-model  selected item value; `select` also fires when the current item is
  tapped again (scroll-to-top / pop-to-root).
-->
<script setup lang="ts" generic="V extends string">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { haptics } from '../../telegram/haptics'
import { prefersReducedMotion } from '../../utils/motion'
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

const DROP_MS = 340
/** Peak horizontal stretch of the droplet while it travels. */
const DROP_STRETCH = 1.14

const bar = ref<HTMLElement | null>(null)
const drop = ref<HTMLElement | null>(null)
const buttons = ref<HTMLElement[]>([])
const selectedIndex = computed(() => props.items.findIndex((i) => i.value === props.modelValue))
let placedX: number | null = null
let animation: Animation | null = null
let observer: ResizeObserver | null = null

/** Moves the droplet under the selected item; `animate` glides it there. */
function place(animate: boolean): void {
  const el = drop.value
  const target = buttons.value[selectedIndex.value]
  if (!el || !target) {
    if (el) el.style.opacity = '0'
    return
  }
  const x = target.offsetLeft
  el.style.width = `${target.offsetWidth}px`
  el.style.opacity = '1'
  const to = `translate3d(${x}px, 0, 0)`
  animation?.cancel()
  if (animate && placedX !== null && placedX !== x && !prefersReducedMotion() && typeof el.animate === 'function') {
    const from = `translate3d(${placedX}px, 0, 0)`
    const mid = `translate3d(${(placedX + x) / 2}px, 0, 0) scaleX(${DROP_STRETCH})`
    animation = el.animate([{ transform: from }, { transform: mid, offset: 0.45 }, { transform: to }], {
      duration: DROP_MS,
      easing: 'cubic-bezier(0.2, 0.9, 0.25, 1.05)',
    })
  }
  el.style.transform = to
  placedX = x
}

function setButton(el: unknown, index: number): void {
  if (el instanceof HTMLElement) buttons.value[index] = el
}

function choose(item: TabBarItem<V>): void {
  if (item.value !== props.modelValue) {
    haptics.selection()
    emit('update:modelValue', item.value)
  } else {
    haptics.impact('light')
  }
  emit('select', item.value)
}

function badgeText(b: TabBarItem<V>['badge']): string {
  return typeof b === 'number' && b > 0 ? (b > 99 ? '99+' : String(b)) : ''
}

watch(selectedIndex, () => void nextTick(() => place(true)))
watch(
  () => [props.items.length, props.hidden],
  () => void nextTick(() => place(false)),
)

onMounted(() => {
  place(false)
  if (typeof ResizeObserver !== 'undefined' && bar.value) {
    observer = new ResizeObserver(() => place(false))
    observer.observe(bar.value)
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  animation?.cancel()
})
</script>

<template>
  <Transition name="jt-tabbar">
    <nav v-show="!hidden" ref="bar" class="jt-tabbar jt-liquid" :aria-label="ariaLabel">
      <span ref="drop" class="jt-tabbar__drop" aria-hidden="true" />
      <button
        v-for="(item, index) in items"
        :key="item.value"
        :ref="(el) => setButton(el, index)"
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

.jt-tabbar__drop {
  position: absolute;
  top: 4px;
  bottom: 4px;
  left: 0;
  width: 0;
  border-radius: var(--jt-radius-capsule);
  background: var(--jt-liquid-drop-bg);
  box-shadow: var(--jt-liquid-drop-shadow);
  opacity: 0;
  transform-origin: center;
  will-change: transform;
  pointer-events: none;
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
    color var(--jt-duration-base) var(--jt-ease),
    transform var(--jt-duration-spring) var(--jt-ease-spring);
}

.jt-tabbar__item.is-selected {
  color: var(--jt-color-label);
}

.jt-tabbar__item:active {
  transform: scale(0.96);
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
