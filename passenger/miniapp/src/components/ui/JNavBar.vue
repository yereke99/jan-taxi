<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!--
  iOS navigation bar for non-map screens: a large title that scrolls with the
  content and a compact bar whose centred title fades in once the large title
  has scrolled away; the bar turns into Liquid Glass while content scrolls
  under it (§15.1). Back navigation is Telegram's native BackButton
  (useBackButton), so there is no in-page back control. Place it as the first
  child of the scrolling page container.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useSlots } from 'vue'

const props = withDefaults(
  defineProps<{
    title: string
    /** false renders only the compact bar with the title always visible. */
    large?: boolean
  }>(),
  { large: true },
)

const slots = useSlots()
const compactOverlaysTitle = computed(() => props.large && !slots.leading && !slots.trailing)
const barEl = ref<HTMLElement | null>(null)
const largeEl = ref<HTMLElement | null>(null)
const compactVisible = ref(!props.large)
let observer: IntersectionObserver | null = null
let sizeObserver: ResizeObserver | null = null

function observeLargeTitle(): void {
  if (!props.large || !largeEl.value || typeof IntersectionObserver === 'undefined') return
  observer?.disconnect()
  const barHeight = barEl.value?.getBoundingClientRect().height ?? 44
  // Without in-page controls the compact row overlays the large title rather
  // than reserving an empty 44px row below Telegram's own controls. Collapse
  // only after the large title disappears behind that safe area.
  const coveredTop = Math.max(0, barHeight - (compactOverlaysTitle.value ? 44 : 0))
  observer = new IntersectionObserver(
    ([entry]) => {
      if (entry) compactVisible.value = !entry.isIntersecting
    },
    { root: largeEl.value.closest('.jt-scroll'), threshold: 0, rootMargin: `-${coveredTop}px 0px 0px 0px` },
  )
  observer.observe(largeEl.value)
}

onMounted(() => {
  observeLargeTitle()
  if (props.large && barEl.value && typeof ResizeObserver !== 'undefined') {
    // Fullscreen entry and rotation can change Telegram's safe area.
    sizeObserver = new ResizeObserver(observeLargeTitle)
    sizeObserver.observe(barEl.value)
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  sizeObserver?.disconnect()
})
</script>

<template>
  <header class="jt-navbar">
    <div ref="barEl" class="jt-navbar__bar" :class="{ 'is-scrolled jt-glass': compactVisible, 'is-overlay': compactOverlaysTitle }">
      <div class="jt-navbar__side"><slot name="leading" /></div>
      <h1 class="jt-navbar__title" :class="{ 'is-visible': compactVisible }" :aria-hidden="large ? 'true' : undefined">
        {{ title }}
      </h1>
      <div class="jt-navbar__side jt-navbar__side--trailing"><slot name="trailing" /></div>
    </div>
    <div v-if="large && $slots['large-trailing']" class="jt-navbar__large-row">
      <h1 ref="largeEl" class="jt-navbar__large">{{ title }}</h1>
      <div class="jt-navbar__large-trailing"><slot name="large-trailing" /></div>
    </div>
    <h1 v-else-if="large" ref="largeEl" class="jt-navbar__large">{{ title }}</h1>
  </header>
</template>

<style scoped>
/*
 * The <header> generates no box, so the sticky compact bar sticks to the
 * page's scroll container (not to the header, which would scroll away).
 */
.jt-navbar {
  display: contents;
}

.jt-navbar__bar {
  position: sticky;
  top: 0;
  z-index: var(--jt-z-navbar);
  display: grid;
  grid-template-columns: minmax(var(--jt-tap-target), 1fr) auto minmax(var(--jt-tap-target), 1fr);
  align-items: center;
  min-height: calc(44px + var(--jt-safe-top));
  padding: var(--jt-safe-top) calc(var(--jt-side-margin) + var(--jt-safe-right)) 0
    calc(var(--jt-side-margin) + var(--jt-safe-left));
  border: 0;
  transition:
    background-color var(--jt-duration-fast) linear,
    box-shadow var(--jt-duration-fast) linear;
}

.jt-navbar__bar:not(.is-scrolled) {
  background: transparent;
}

.jt-navbar__bar.is-overlay {
  margin-bottom: -44px;
}

.jt-navbar__bar.is-overlay:not(.is-scrolled) {
  pointer-events: none;
}

/* Glass bar: no floating shadow, a hairline separator instead (iOS). */
.jt-navbar__bar.is-scrolled {
  border: 0;
  box-shadow: 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.jt-navbar__side {
  display: flex;
  align-items: center;
  min-height: var(--jt-tap-target);
}

.jt-navbar__side--trailing {
  justify-content: flex-end;
}

.jt-navbar__title {
  font: var(--jt-font-headline);
  text-align: center;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  opacity: 0;
  transition: opacity var(--jt-duration-fast) linear;
}

/* While hidden the compact title takes no width, so wide leading/trailing
   content (e.g. the driver's name and online switch) gets the whole row. */
.jt-navbar__title:not(.is-visible) {
  max-width: 0;
}

.jt-navbar__title.is-visible {
  opacity: 1;
}

.jt-navbar__large-row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  padding-right: calc(var(--jt-side-margin) + var(--jt-safe-right));
}

.jt-navbar__large-row .jt-navbar__large {
  flex: 1;
  min-width: 0;
  padding-right: 0;
  overflow: hidden;
  overflow-wrap: normal;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.jt-navbar__large-trailing {
  flex: none;
  padding-bottom: var(--jt-space-2);
}

.jt-navbar__large {
  padding: 3px calc(var(--jt-side-margin) + var(--jt-safe-right)) var(--jt-space-2)
    calc(var(--jt-side-margin) + var(--jt-safe-left));
  font: var(--jt-font-large-title);
  letter-spacing: var(--jt-tracking-large-title);
  overflow-wrap: anywhere;
}
</style>
