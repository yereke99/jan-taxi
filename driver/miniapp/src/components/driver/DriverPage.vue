<!--
  Non-map screen layout (§15): iOS large-title navigation bar over an
  inset-grouped scroll area, an optional sticky footer for the screen's one
  primary action (kept above the keyboard and the home indicator) and room
  for the floating tab bar on main sections.
-->
<script setup lang="ts">
import { ref } from 'vue'
import { JNavBar } from '../ui'
import { useTelegramFullscreen } from '../../telegram'

withDefaults(
  defineProps<{
    title: string
    large?: boolean
    /** Leaves room for the floating tab bar. */
    tabBar?: boolean
    /** White background instead of the grouped gray. */
    plain?: boolean
  }>(),
  { large: true, tabBar: false, plain: false },
)

const tg = useTelegramFullscreen()
const scrollEl = ref<HTMLElement | null>(null)

defineExpose({
  scrollToTop: (): void => scrollEl.value?.scrollTo({ top: 0, behavior: 'smooth' }),
  scrollEl,
})
</script>

<template>
  <div class="dpage" :class="{ 'jt-grouped': !plain, 'is-plain': plain, 'has-tabbar': tabBar, 'has-footer': $slots.footer, 'is-keyboard': tg.state.keyboardOpen }">
    <div ref="scrollEl" class="dpage__scroll jt-scroll">
      <JNavBar :title="title" :large="large">
        <template v-if="$slots.leading" #leading><slot name="leading" /></template>
        <template v-if="$slots.trailing" #trailing><slot name="trailing" /></template>
        <template v-if="$slots['large-trailing']" #large-trailing><slot name="large-trailing" /></template>
      </JNavBar>
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
