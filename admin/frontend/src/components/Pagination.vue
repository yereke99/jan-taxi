<!-- Page navigation for admin lists ({items, page, page_size, total}, §10). -->
<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ page: number; pageSize: number; total: number }>()
const emit = defineEmits<{ 'update:page': [page: number] }>()

const { t } = useI18n()
const pages = computed(() => Math.max(1, Math.ceil(props.total / Math.max(1, props.pageSize))))
const from = computed(() => (props.total === 0 ? 0 : (props.page - 1) * props.pageSize + 1))
const to = computed(() => Math.min(props.total, props.page * props.pageSize))

/** Compact page list: first, last, current ±1, with gaps as null. */
const items = computed<Array<number | null>>(() => {
  const n = pages.value
  const cur = props.page
  const set = new Set([1, n, cur - 1, cur, cur + 1].filter((p) => p >= 1 && p <= n))
  const sorted = [...set].sort((a, b) => a - b)
  const out: Array<number | null> = []
  sorted.forEach((p, i) => {
    if (i > 0 && p - sorted[i - 1]! > 1) out.push(null)
    out.push(p)
  })
  return out
})

function go(p: number): void {
  if (p >= 1 && p <= pages.value && p !== props.page) emit('update:page', p)
}
</script>

<template>
  <nav class="pager" :aria-label="t('pagination.label')">
    <span class="pager__range">{{ t('pagination.range', { from, to, total }) }}</span>
    <div class="pager__controls">
      <button type="button" class="pager__btn" :disabled="page <= 1" :aria-label="t('pagination.previous')" @click="go(page - 1)">
        <ChevronLeft :size="16" aria-hidden="true" />
      </button>
      <template v-for="(p, i) in items" :key="p ?? `gap-${i}`">
        <span v-if="p === null" class="pager__gap" aria-hidden="true">…</span>
        <button
          v-else
          type="button"
          class="pager__btn"
          :class="{ 'is-current': p === page }"
          :aria-current="p === page ? 'page' : undefined"
          @click="go(p)"
        >
          {{ p }}
        </button>
      </template>
      <button type="button" class="pager__btn" :disabled="page >= pages" :aria-label="t('pagination.next')" @click="go(page + 1)">
        <ChevronRight :size="16" aria-hidden="true" />
      </button>
    </div>
  </nav>
</template>

<style scoped>
.pager {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-4);
  padding: var(--jt-space-3) 0;
  font: var(--jt-font-small);
  color: var(--jt-color-label-secondary);
}

.pager__controls {
  display: flex;
  align-items: center;
  gap: 2px;
}

.pager__btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: var(--jt-control-height-sm);
  height: var(--jt-control-height-sm);
  padding: 0 6px;
  border-radius: 6px;
  color: var(--jt-color-label);
  font-variant-numeric: tabular-nums;
}

.pager__btn:hover:not(:disabled) {
  background: var(--jt-color-hover);
}

.pager__btn.is-current {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.pager__btn:disabled {
  opacity: var(--jt-opacity-disabled);
  cursor: default;
}

.pager__gap {
  padding: 0 4px;
}
</style>
