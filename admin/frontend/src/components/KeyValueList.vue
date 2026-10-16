<!-- Definition list for detail panes. Slot `value-<key>` overrides a value. -->
<script setup lang="ts">
import type { KeyValueItem } from './types'

defineProps<{ items: KeyValueItem[] }>()
</script>

<template>
  <dl class="kv">
    <template v-for="(item, i) in items" :key="item.key ?? i">
      <dt class="kv__label">{{ item.label }}</dt>
      <dd class="kv__value" :class="{ 'jt-mono': item.mono }">
        <slot :name="`value-${item.key ?? i}`" :item="item">
          {{ item.value === null || item.value === undefined || item.value === '' ? '—' : item.value }}
        </slot>
      </dd>
    </template>
  </dl>
</template>

<style scoped>
.kv {
  display: grid;
  /* Labels take what they need up to 45% of the width, then wrap. */
  grid-template-columns: fit-content(45%) minmax(0, 1fr);
  column-gap: var(--jt-space-5);
}

.kv__label,
.kv__value {
  padding: 8px 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.kv__label {
  color: var(--jt-color-label-secondary);
}

.kv__value {
  min-width: 0;
  overflow-wrap: anywhere;
}
</style>
