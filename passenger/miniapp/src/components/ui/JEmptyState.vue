<!-- Shared with the other Mini App — keep both copies identical (make verify-shared). -->
<script setup lang="ts">
withDefaults(defineProps<{ title: string; description?: string }>(), { description: '' })
</script>

<template>
  <div class="jt-empty" role="status">
    <div v-if="$slots.icon" class="jt-empty__icon" aria-hidden="true"><slot name="icon" /></div>
    <p class="jt-empty__title">{{ title }}</p>
    <p v-if="description" class="jt-empty__description">{{ description }}</p>
    <div v-if="$slots.action" class="jt-empty__action"><slot name="action" /></div>
  </div>
</template>

<style scoped>
.jt-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: var(--jt-space-2);
  padding: var(--jt-space-8) var(--jt-space-6);
}

.jt-empty__icon {
  display: flex;
  margin-bottom: var(--jt-space-2);
  color: var(--jt-color-label-tertiary);
}

.jt-empty__title {
  font: var(--jt-font-title3);
}

.jt-empty__description {
  max-width: 320px;
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.jt-empty__action {
  margin-top: var(--jt-space-4);
  width: 100%;
  max-width: 320px;
}
</style>
