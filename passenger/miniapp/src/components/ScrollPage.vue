<!--
  Non-map screen (§15 "Layout"): iOS large title that collapses into a glass
  navigation bar as content scrolls under it, inset-grouped content on the
  grouped background, bottom safe area respected.
-->
<script setup lang="ts">
import { JNavBar } from './ui'

withDefaults(defineProps<{ title: string; large?: boolean }>(), { large: true })
</script>

<template>
  <div class="page jt-scroll jt-grouped">
    <JNavBar :title="title" :large="large">
      <template v-if="$slots.trailing" #trailing><slot name="trailing" /></template>
    </JNavBar>
    <div class="page__content">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.page {
  position: absolute;
  inset: 0;
}

.page__content {
  padding: var(--jt-space-2) 0 calc(var(--jt-safe-bottom) + var(--jt-space-8));
}
</style>
