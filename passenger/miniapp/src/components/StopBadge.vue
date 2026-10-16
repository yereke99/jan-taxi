<!-- Letter badge of a route point (A pickup … E destination), matching the map markers. -->
<script setup lang="ts">
withDefaults(
  defineProps<{
    letter: string
    /** Completed stop on an active trip (grey). */
    done?: boolean
    /** Point not chosen yet (outlined). */
    empty?: boolean
    size?: 'small' | 'regular'
  }>(),
  { done: false, empty: false, size: 'regular' },
)
</script>

<template>
  <span class="stop-badge" :class="[`stop-badge--${size}`, { 'is-done': done, 'is-empty': empty }]" aria-hidden="true">
    {{ letter }}
  </span>
</template>

<style scoped>
.stop-badge {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.stop-badge--regular {
  width: 26px;
  height: 26px;
  font-size: 14px;
}

.stop-badge--small {
  width: 20px;
  height: 20px;
  font-size: 12px;
}

.stop-badge.is-done {
  background: var(--jt-color-gray);
}

.stop-badge.is-empty {
  background: transparent;
  color: var(--jt-color-label);
  box-shadow: inset 0 0 0 1.5px var(--jt-color-label);
}
</style>
