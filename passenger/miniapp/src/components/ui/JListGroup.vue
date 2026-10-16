<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<!-- iOS inset-grouped list section: white 22px cards on the grouped background (§15.1). -->
<script setup lang="ts">
withDefaults(defineProps<{ header?: string; footer?: string }>(), { header: '', footer: '' })
</script>

<template>
  <section class="jt-list-group">
    <h3 v-if="header || $slots.header" class="jt-list-group__header">
      <slot name="header">{{ header }}</slot>
    </h3>
    <div class="jt-list-group__body" role="list">
      <slot />
    </div>
    <p v-if="footer || $slots.footer" class="jt-list-group__footer">
      <slot name="footer">{{ footer }}</slot>
    </p>
  </section>
</template>

<style scoped>
.jt-list-group {
  margin: 0 var(--jt-side-margin) var(--jt-space-6);
}

.jt-list-group__header {
  padding: 0 var(--jt-space-4) 7px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
  letter-spacing: 0.1px;
}

.jt-list-group__body {
  background: var(--jt-color-bg);
  border-radius: var(--jt-radius-card);
  overflow: hidden;
}

.jt-list-group__footer {
  padding: 7px var(--jt-space-4) 0;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>
