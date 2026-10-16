<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- Status pill. Tone carries meaning (utils/status.ts maps statuses to tones). -->
<script setup lang="ts">
import type { Tone } from './types'

withDefaults(defineProps<{ tone?: Tone; label?: string }>(), { tone: 'neutral', label: '' })
</script>

<template>
  <span class="jt-badge" :class="`jt-badge--${tone}`">
    <slot>{{ label }}</slot>
  </span>
</template>

<style scoped>
.jt-badge {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  min-height: 22px;
  padding: 2px var(--jt-space-2);
  border-radius: var(--jt-radius-round);
  font: 600 13px/18px var(--jt-font-family);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.jt-badge--neutral {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
}

.jt-badge--dark {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.jt-badge--success {
  background: var(--jt-color-green-tint);
  color: var(--jt-color-green-text);
}

.jt-badge--danger {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.jt-badge--info {
  background: var(--jt-color-blue-tint);
  color: var(--jt-color-blue-text);
}
</style>
