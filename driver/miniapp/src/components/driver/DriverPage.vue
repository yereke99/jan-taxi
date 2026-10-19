<!--
  Non-map screen layout (§15): iOS large-title navigation bar over an
  inset-grouped scroll area, an optional sticky footer for the screen's one
  primary action (kept above the keyboard and the home indicator) and room
  for the floating tab bar on main sections.

  :bar="false" (home) draws no navigation bar: the page's own header starts
  right under the Telegram header and safe area, the title stays for screen
  readers, and a glass edge appears under the Telegram header once content
  scrolls beneath it.
-->
<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { JNavBar } from '../ui'
import { useTelegramFullscreen } from '../../telegram'

const props = withDefaults(
  defineProps<{
    title: string
    large?: boolean
    /** false: no navigation bar, the page draws its own compact header. */
    bar?: boolean
    /** Leaves room for the floating tab bar. */
    tabBar?: boolean
    /** White background instead of the grouped gray. */
    plain?: boolean
  }>(),
  { large: true, bar: true, tabBar: false, plain: false },
)

const tg = useTelegramFullscreen()
const scrollEl = ref<HTMLElement | null>(null)
/** Without a bar: content has scrolled under the Telegram header. */
const scrolled = ref(false)

function onScroll(): void {
  scrolled.value = (scrollEl.value?.scrollTop ?? 0) > 0
}

onMounted(() => {
  if (!props.bar) scrollEl.value?.addEventListener('scroll', onScroll, { passive: true })
})
onBeforeUnmount(() => scrollEl.value?.removeEventListener('scroll', onScroll))

defineExpose({
  scrollToTop: (): void => scrollEl.value?.scrollTo({ top: 0, behavior: 'smooth' }),
  scrollEl,
})
</script>

<template>
  <div class="dpage" :class="{ 'jt-grouped': !plain, 'is-plain': plain, 'has-tabbar': tabBar, 'has-footer': $slots.footer, 'is-keyboard': tg.state.keyboardOpen }">
    <div ref="scrollEl" class="dpage__scroll jt-scroll">
      <JNavBar v-if="bar" :title="title" :large="large">
        <template v-if="$slots.leading" #leading><slot name="leading" /></template>
        <template v-if="$slots.trailing" #trailing><slot name="trailing" /></template>
        <template v-if="$slots['large-trailing']" #large-trailing><slot name="large-trailing" /></template>
      </JNavBar>
      <template v-else>
        <div class="dpage__edge" :class="{ 'is-scrolled jt-glass': scrolled }" aria-hidden="true" />
        <h1 class="jt-visually-hidden">{{ title }}</h1>
      </template>
      <slot name="top" />
      <div class="dpage__content">
        <slot />
      </div>
    </div>
    <div v-if="$slots.footer" class="dpage__footer">
      <slot name="footer" />
    </div>
  </div>
</template>

<style scoped>
.dpage {
  display: flex;
  flex-direction: column;
  background: var(--jt-color-grouped-bg);
}

.dpage.is-plain {
  background: var(--jt-color-bg);
}

.dpage__scroll {
  flex: 1;
  min-height: 0;
}

/* Keeps scrolled content from showing through the Telegram header (no bar). */
.dpage__edge {
  position: sticky;
  top: 0;
  z-index: var(--jt-z-navbar);
  height: var(--jt-safe-top);
  transition:
    background-color var(--jt-duration-fast) linear,
    box-shadow var(--jt-duration-fast) linear;
}

.dpage__edge:not(.is-scrolled) {
  background: transparent;
}

.dpage__edge.is-scrolled {
  border: 0;
  box-shadow: 0 var(--jt-hairline) 0 var(--jt-color-separator);
}

.dpage__content {
  padding: var(--jt-space-2) 0 calc(max(var(--jt-safe-bottom), var(--jt-float-inset)) + var(--jt-space-6));
}

.dpage.has-tabbar .dpage__content {
  padding-bottom: calc(
    max(var(--jt-safe-bottom), var(--jt-float-inset)) + var(--jt-float-inset) + var(--jt-tabbar-height) + var(--jt-space-6)
  );
}

.dpage.has-footer .dpage__content {
  padding-bottom: var(--jt-space-4);
}

.dpage__footer {
  flex: none;
  padding: var(--jt-space-3) calc(var(--jt-side-margin) + var(--jt-safe-right))
    calc(max(var(--jt-safe-bottom), var(--jt-float-inset)) + var(--jt-space-2)) calc(var(--jt-side-margin) + var(--jt-safe-left));
  background: var(--jt-color-bg);
  box-shadow: 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

/* iOS reports the keyboard overlap instead of shrinking the viewport. */
.dpage.is-keyboard .dpage__footer {
  padding-bottom: calc(var(--jt-keyboard) + var(--jt-space-2));
}
</style>
