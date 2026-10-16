<!--
  Data table with typed columns, sortable headers, row click, loading and empty
  states. Sorting is server-side by default (emits update:sort); with
  sort-mode="client" rows are sorted locally by column.value.

  Cells: slot `cell-<column.key>` with { row, value } overrides the text.
-->
<script setup lang="ts" generic="Row extends object">
import { ArrowDown, ArrowUp } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppSpinner from './AppSpinner.vue'
import type { Column, SortState } from './types'

const props = withDefaults(
  defineProps<{
    columns: Column<Row>[]
    rows: Row[]
    rowKey: keyof Row | ((row: Row) => string)
    sort?: SortState | null
    sortMode?: 'server' | 'client'
    loading?: boolean
    emptyText?: string
    clickable?: boolean
    caption?: string
  }>(),
  { sort: null, sortMode: 'server', loading: false, emptyText: '', clickable: false, caption: '' },
)

const emit = defineEmits<{
  'update:sort': [sort: SortState | null]
  'row-click': [row: Row, event: MouseEvent | KeyboardEvent]
}>()

defineSlots<
  {
    [K in `cell-${string}`]?: (props: { row: Row; value: unknown }) => unknown
  } & {
    empty?: () => unknown
  }
>()

const { t } = useI18n()

function keyOf(row: Row, index: number): string {
  if (typeof props.rowKey === 'function') return props.rowKey(row)
  const v = row[props.rowKey]
  return v === undefined || v === null ? String(index) : String(v)
}

function valueOf(col: Column<Row>, row: Row): unknown {
  return col.value ? col.value(row) : (row as Record<string, unknown>)[col.key]
}

function textOf(col: Column<Row>, row: Row): string {
  const v = valueOf(col, row)
  if (col.format) return col.format(v, row)
  return v === null || v === undefined || v === '' ? '—' : String(v)
}

function compare(a: unknown, b: unknown): number {
  if (a === b) return 0
  if (a === null || a === undefined) return 1
  if (b === null || b === undefined) return -1
  if (typeof a === 'number' && typeof b === 'number') return a - b
  return String(a).localeCompare(String(b), undefined, { numeric: true })
}

const displayRows = computed(() => {
  if (props.sortMode !== 'client' || !props.sort) return props.rows
  const col = props.columns.find((c) => c.key === props.sort?.key)
  if (!col) return props.rows
  const dir = props.sort.dir === 'asc' ? 1 : -1
  return [...props.rows].sort((a, b) => dir * compare(valueOf(col, a), valueOf(col, b)))
})

function toggleSort(col: Column<Row>): void {
  if (!col.sortable) return
  const cur = props.sort
  if (!cur || cur.key !== col.key) emit('update:sort', { key: col.key, dir: 'asc' })
  else if (cur.dir === 'asc') emit('update:sort', { key: col.key, dir: 'desc' })
  else emit('update:sort', null)
}

function ariaSort(col: Column<Row>): 'ascending' | 'descending' | 'none' | undefined {
  if (!col.sortable) return undefined
  if (props.sort?.key !== col.key) return 'none'
  return props.sort.dir === 'asc' ? 'ascending' : 'descending'
}

function onRowKey(row: Row, e: KeyboardEvent): void {
  if (props.clickable && (e.key === 'Enter' || e.key === ' ')) {
    e.preventDefault()
    emit('row-click', row, e)
  }
}
</script>

<template>
  <div class="table-wrap" :aria-busy="loading || undefined">
    <table class="table">
      <caption v-if="caption" class="jt-visually-hidden">{{ caption }}</caption>
      <thead>
        <tr>
          <th
            v-for="col in columns"
            :key="col.key"
            scope="col"
            :style="{ width: col.width, textAlign: col.align ?? 'left' }"
            :aria-sort="ariaSort(col)"
          >
            <button
              v-if="col.sortable"
              type="button"
              class="th-sort"
              :class="{ 'is-active': sort?.key === col.key }"
              :title="sort?.key === col.key && sort.dir === 'asc' ? t('table.sort_descending') : t('table.sort_ascending')"
              @click="toggleSort(col)"
            >
              {{ col.label }}
              <ArrowUp v-if="sort?.key === col.key && sort.dir === 'asc'" :size="12" aria-hidden="true" />
              <ArrowDown v-else-if="sort?.key === col.key && sort.dir === 'desc'" :size="12" aria-hidden="true" />
            </button>
            <template v-else>{{ col.label }}</template>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="(row, i) in displayRows"
          :key="keyOf(row, i)"
          :class="{ 'is-clickable': clickable }"
          :tabindex="clickable ? 0 : undefined"
          @click="clickable && emit('row-click', row, $event)"
          @keydown="onRowKey(row, $event)"
        >
          <td
            v-for="col in columns"
            :key="col.key"
            :class="{ 'jt-mono': col.mono }"
            :style="{ textAlign: col.align ?? 'left' }"
          >
            <slot :name="`cell-${col.key}`" :row="row" :value="valueOf(col, row)">{{ textOf(col, row) }}</slot>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="loading && rows.length === 0" class="table-state">
      <AppSpinner :size="20" :label="t('common.loading')" />
    </div>
    <div v-else-if="!loading && rows.length === 0" class="table-state">
      <slot name="empty">{{ emptyText || t('table.empty') }}</slot>
    </div>
    <div v-if="loading && rows.length > 0" class="table-overlay"><AppSpinner :size="20" /></div>
  </div>
</template>

<style scoped>
.table-wrap {
  position: relative;
  overflow-x: auto;
  overflow-y: hidden;
  background: var(--jt-color-bg);
  border-radius: var(--jt-radius-card);
  box-shadow:
    var(--jt-shadow-card),
    inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
}

.table {
  width: 100%;
  border-collapse: collapse;
  font: var(--jt-font-body);
}

th:first-child,
td:first-child {
  padding-left: var(--jt-space-5);
}

th:last-child,
td:last-child {
  padding-right: var(--jt-space-5);
}

th {
  height: 36px;
  padding: 0 var(--jt-space-3);
  background: var(--jt-color-bg);
  font: 600 12px/16px var(--jt-font-family);
  color: var(--jt-color-label-secondary);
  text-transform: uppercase;
  letter-spacing: 0.2px;
  white-space: nowrap;
  box-shadow: inset 0 calc(-1 * var(--jt-hairline)) 0 var(--jt-color-separator);
}

.th-sort {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font: inherit;
  color: inherit;
  text-transform: inherit;
  letter-spacing: inherit;
}

.th-sort:hover,
.th-sort.is-active {
  color: var(--jt-color-label);
}

td {
  height: var(--jt-row-height);
  padding: 6px var(--jt-space-3);
  white-space: nowrap;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
  vertical-align: middle;
}

tbody tr:last-child td {
  border-bottom: 0;
}

tr.is-clickable {
  cursor: pointer;
}

tr.is-clickable:hover td {
  background: var(--jt-color-hover);
}

.table-state {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 120px;
  color: var(--jt-color-label-secondary);
}

.table-overlay {
  position: absolute;
  inset: 36px 0 0;
  border-radius: 0 0 var(--jt-radius-card) var(--jt-radius-card);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: var(--jt-space-6);
  background: rgba(255, 255, 255, 0.6);
}
</style>
