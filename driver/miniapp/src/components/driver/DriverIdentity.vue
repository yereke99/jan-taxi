<!--
  Top row of the driver's main lists: initials avatar, full name and the
  driver code. Neutral initials only; photos stay on the profile screen.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useSessionStore } from '../../stores/session'

const session = useSessionStore()

const name = computed(() => [session.me?.first_name, session.me?.last_name].filter(Boolean).join(' '))
const initials = computed(() =>
  [session.me?.first_name, session.me?.last_name]
    .map((s) => (s ?? '').trim().charAt(0).toUpperCase())
    .filter(Boolean)
    .map((c) => `${c}.`)
    .join(''),
)
</script>

<template>
  <div class="who">
    <span class="who__avatar" aria-hidden="true">{{ initials }}</span>
    <span class="who__text">
      <span class="who__name">{{ name }}</span>
      <span v-if="session.driver?.code" class="who__code jt-tabular">{{ session.driver.code }}</span>
    </span>
  </div>
</template>

<style scoped>
.who {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-width: 0;
}

.who__avatar {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: var(--jt-color-fill-tertiary);
  font: 600 15px/1 var(--jt-font-family);
  color: var(--jt-color-label-secondary);
}

.who__text {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.who__name {
  font: var(--jt-font-headline);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.who__code {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}
</style>
