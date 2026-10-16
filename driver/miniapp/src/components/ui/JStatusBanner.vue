<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- Slim banner pinned under the top safe area (offline / reconnecting). -->
<script setup lang="ts">
import JSpinner from './JSpinner.vue'

withDefaults(defineProps<{ text: string; tone?: 'neutral' | 'danger'; busy?: boolean }>(), {
  tone: 'neutral',
  busy: false,
})
</script>

<template>
  <div class="jt-banner" :class="`jt-banner--${tone}`" role="status" aria-live="polite">
    <JSpinner v-if="busy" color="light" />
    <span class="jt-banner__text">{{ text }}</span>
  </div>
</template>

<style scoped>
.jt-banner {
  position: fixed;
  top: calc(var(--jt-safe-top) + var(--jt-space-2));
  left: 50%;
  z-index: var(--jt-z-banner);
  display: inline-flex;
  align-items: center;
  gap: var(--jt-space-2);
  max-width: calc(100% - 2 * var(--jt-side-margin));
  min-height: 32px;
  padding: 6px var(--jt-space-3);
  border-radius: var(--jt-radius-round);
  transform: translateX(-50%);
  background: var(--jt-color-toast-bg);
  color: #fff;
  box-shadow: var(--jt-shadow-floating);
  font: 600 13px/18px var(--jt-font-family);
  pointer-events: none;
}

.jt-banner--danger {
  background: var(--jt-color-red);
}

.jt-banner__text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
