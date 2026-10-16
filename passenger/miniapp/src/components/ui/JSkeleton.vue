<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- Loading placeholder. `lines` renders a text block; otherwise a single box. -->
<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    width?: string
    height?: string
    radius?: string
    lines?: number
    circle?: boolean
  }>(),
  { width: '100%', height: '16px', radius: '6px', lines: 0, circle: false },
)

const boxStyle = computed(() => ({
  width: props.circle ? props.height : props.width,
  height: props.height,
  borderRadius: props.circle ? '50%' : props.radius,
}))
</script>

<template>
  <span v-if="lines > 0" class="jt-skeleton-lines" aria-hidden="true">
    <span
      v-for="i in lines"
      :key="i"
      class="jt-skeleton"
      :style="{ width: i === lines && lines > 1 ? '60%' : width, height, borderRadius: radius }"
    />
  </span>
  <span v-else class="jt-skeleton" :style="boxStyle" aria-hidden="true" />
</template>

<style scoped>
.jt-skeleton-lines {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.jt-skeleton {
  display: block;
  background: var(--jt-color-fill-secondary);
  animation: jt-skeleton-pulse 1.4s ease-in-out infinite;
}

@keyframes jt-skeleton-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.55;
  }
}

@media (prefers-reduced-motion: reduce) {
  .jt-skeleton {
    animation: none;
  }
}
</style>
