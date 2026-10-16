<!-- One metric: label, big value, optional detail line; a link when the operator may open the list behind it. -->
<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import type { RouteLocationRaw } from 'vue-router'

defineProps<{ label: string; value: string; detail?: string; to?: RouteLocationRaw | null; tone?: 'neutral' | 'attention' }>()
</script>

<template>
  <component :is="to ? 'RouterLink' : 'div'" :to="to ?? undefined" class="kpi" :class="[{ 'kpi--link': to }, `kpi--${tone ?? 'neutral'}`]">
    <span class="kpi__label">
      {{ label }}
      <ChevronRight v-if="to" :size="14" aria-hidden="true" />
    </span>
    <span class="kpi__value jt-num">{{ value }}</span>
    <span v-if="detail" class="kpi__detail">{{ detail }}</span>
  </component>
</template>

<style scoped>
.kpi {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: var(--jt-space-4) var(--jt-space-5);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  box-shadow:
    var(--jt-shadow-card),
    inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  color: var(--jt-color-label);
  text-decoration: none;
}

.kpi--link:hover {
  text-decoration: none;
  box-shadow:
    var(--jt-shadow-card),
    inset 0 0 0 1px var(--jt-color-label-tertiary);
}

.kpi__label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.kpi__value {
  font: var(--jt-font-large-title);
  letter-spacing: -0.4px;
}

.kpi--attention .kpi__value {
  color: var(--jt-color-red-text);
}

.kpi__detail {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
