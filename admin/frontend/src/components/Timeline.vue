<!-- Vertical history list (status changes, moderation events, top-up events). -->
<script setup lang="ts">
import type { TimelineItem } from './types'

defineProps<{ items: TimelineItem[]; empty?: string }>()
</script>

<template>
  <ol v-if="items.length" class="timeline">
    <li v-for="item in items" :key="item.key" class="timeline__item" :class="`is-${item.tone ?? 'neutral'}`">
      <span class="timeline__dot" aria-hidden="true" />
      <div class="timeline__body">
        <p class="timeline__title">{{ item.title }}</p>
        <p v-if="item.detail" class="timeline__detail">{{ item.detail }}</p>
        <p class="timeline__meta">
          <time>{{ item.time }}</time>
          <template v-if="item.meta"> · {{ item.meta }}</template>
        </p>
      </div>
    </li>
  </ol>
  <p v-else class="jt-muted">{{ empty }}</p>
</template>

<style scoped>
.timeline {
  position: relative;
  display: flex;
  flex-direction: column;
}

.timeline__item {
  position: relative;
  display: flex;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-3);
}

.timeline__item:not(:last-child)::before {
  content: '';
  position: absolute;
  left: 4.5px;
  top: 14px;
  bottom: 0;
  width: 1px;
  background: var(--jt-color-fill-tertiary);
}

.timeline__dot {
  flex: none;
  width: 10px;
  height: 10px;
  margin-top: 5px;
  border-radius: 50%;
  background: var(--jt-color-gray);
}

.is-success .timeline__dot {
  background: var(--jt-color-green);
}

.is-danger .timeline__dot {
  background: var(--jt-color-red);
}

.is-dark .timeline__dot {
  background: var(--jt-color-primary);
}

.timeline__body {
  min-width: 0;
}

.timeline__title {
  font: var(--jt-font-body-strong);
}

.timeline__detail {
  margin-top: 2px;
  overflow-wrap: anywhere;
}

.timeline__meta {
  margin-top: 2px;
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
}
</style>
