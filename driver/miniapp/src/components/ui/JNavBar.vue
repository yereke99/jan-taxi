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
import { onBeforeUnmount, onMounted, ref } from 'vue'

const props = withDefaults(
  defineProps<{
    title: string
    /** false renders only the compact bar with the title always visible. */
    large?: boolean
  }>(),
  { large: true },
)

const largeEl = ref<HTMLElement | null>(null)
const compactVisible = ref(!props.large)
let observer: IntersectionObserver | null = null

onMounted(() => {
  if (!props.large || !largeEl.value || typeof IntersectionObserver === 'undefined') return
  observer = new IntersectionObserver(
    ([entry]) => {
      if (entry) compactVisible.value = !entry.isIntersecting
    },
    // The compact bar covers the top 44px plus the safe area; treat the large
    // title as gone once it is fully under that bar.
    { threshold: 0, rootMargin: '-56px 0px 0px 0px' },
  )
  observer.observe(largeEl.value)
})

onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <header class="jt-navbar">
    <div class="jt-navbar__bar" :class="{ 'is-scrolled jt-glass': compactVisible }">
      <div class="jt-navbar__side"><slot name="leading" /></div>
      <h1 class="jt-navbar__title" :class="{ 'is-visible': compactVisible }" :aria-hidden="large ? 'true' : undefined">
        {{ title }}
      </h1>
      <div class="jt-navbar__side jt-navbar__side--trailing"><slot name="trailing" /></div>
    </div>
    <h1 v-if="large" ref="largeEl" class="jt-navbar__large">{{ title }}</h1>
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

.jt-navbar__title.is-visible {
  opacity: 1;
}

.jt-navbar__large {
  padding: 3px calc(var(--jt-side-margin) + var(--jt-safe-right)) var(--jt-space-2)
    calc(var(--jt-side-margin) + var(--jt-safe-left));
  font: var(--jt-font-large-title);
  letter-spacing: var(--jt-tracking-large-title);
  overflow-wrap: anywhere;
}
</style>
