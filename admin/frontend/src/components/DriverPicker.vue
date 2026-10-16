<!--
  Driver search field: type a code, phone, plate or name and pick a driver
  from GET /admin/drivers. v-model is the driver id ('' for none).
-->
<script setup lang="ts">
import { Search, X } from '@lucide/vue'
import { onBeforeUnmount, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { driversApi, type AdminDriverListItem } from '../api/drivers'
import { isAbortError } from '../services/api'
import AppSpinner from './AppSpinner.vue'

const props = withDefaults(defineProps<{ modelValue: string; status?: string; cityId?: string; placeholder?: string }>(), {
  status: '',
  cityId: '',
  placeholder: '',
})
const emit = defineEmits<{ 'update:modelValue': [id: string]; select: [driver: AdminDriverListItem | null] }>()

const { t } = useI18n()
const listId = useId()
const text = ref('')
const label = ref('')
const results = ref<AdminDriverListItem[]>([])
const open = ref(false)
const loading = ref(false)
const active = ref(-1)
let timer: ReturnType<typeof setTimeout> | undefined
let ctrl: AbortController | null = null

function describe(d: { code: string; first_name: string; last_name: string }): string {
  return `${d.code} · ${d.first_name} ${d.last_name}`.trim()
}

// Resolve the label of a driver id that came from the URL.
watch(
  () => props.modelValue,
  async (id) => {
    if (!id) {
      label.value = ''
      return
    }
    if (label.value) return
    try {
      const d = await driversApi.get(id)
      if (props.modelValue === id) label.value = describe(d)
    } catch {
      label.value = id.slice(0, 8)
    }
  },
  { immediate: true },
)

async function search(q: string): Promise<void> {
  ctrl?.abort()
  if (q.trim().length < 2) {
    results.value = []
    loading.value = false
    return
  }
  const mine = new AbortController()
  ctrl = mine
  loading.value = true
  try {
    const page = await driversApi.list({ q, status: props.status, city_id: props.cityId }, 1, 8, mine.signal)
    if (ctrl === mine) {
      results.value = page.items
      active.value = page.items.length ? 0 : -1
    }
  } catch (err) {
    if (!isAbortError(err)) results.value = []
  } finally {
    if (ctrl === mine) loading.value = false
  }
}

function onInput(e: Event): void {
  text.value = (e.target as HTMLInputElement).value
  open.value = true
  clearTimeout(timer)
  timer = setTimeout(() => void search(text.value), 250)
}

function choose(d: AdminDriverListItem): void {
  label.value = describe(d)
  text.value = ''
  open.value = false
  results.value = []
  emit('update:modelValue', d.id)
  emit('select', d)
}

function clear(): void {
  label.value = ''
  emit('update:modelValue', '')
  emit('select', null)
}

function onKey(e: KeyboardEvent): void {
  if (!open.value || results.value.length === 0) return
  if (e.key === 'ArrowDown') active.value = (active.value + 1) % results.value.length
  else if (e.key === 'ArrowUp') active.value = (active.value - 1 + results.value.length) % results.value.length
  else if (e.key === 'Enter' && results.value[active.value]) choose(results.value[active.value]!)
  else if (e.key === 'Escape') open.value = false
  else return
  e.preventDefault()
}

onBeforeUnmount(() => {
  clearTimeout(timer)
  ctrl?.abort()
})
</script>

<template>
  <div class="picker">
    <div v-if="modelValue" class="picker__chosen">
      <span class="picker__label">{{ label || '…' }}</span>
      <button type="button" class="picker__clear" :aria-label="t('common.reset')" @click="clear"><X :size="14" /></button>
    </div>
    <label v-else class="picker__field">
      <Search :size="14" aria-hidden="true" />
      <input
        type="search"
        :value="text"
        :placeholder="placeholder || t('drivers.picker_placeholder')"
        role="combobox"
        :aria-expanded="open && results.length > 0"
        :aria-controls="listId"
        autocomplete="off"
        @input="onInput"
        @keydown="onKey"
        @focus="open = true"
        @blur="open = false"
      />
      <AppSpinner v-if="loading" :size="12" />
    </label>
    <ul v-if="open && results.length > 0 && !modelValue" :id="listId" class="picker__list" role="listbox">
      <li
        v-for="(d, i) in results"
        :key="d.id"
        role="option"
        :aria-selected="i === active"
        class="picker__option"
        :class="{ 'is-active': i === active }"
        @mousedown.prevent="choose(d)"
      >
        <span class="jt-mono">{{ d.code }}</span>
        <span>{{ d.first_name }} {{ d.last_name }}</span>
        <span class="jt-muted jt-mono">{{ d.vehicle?.plate ?? '' }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.picker {
  position: relative;
  min-width: 240px;
}

.picker__field,
.picker__chosen {
  display: flex;
  align-items: center;
  gap: 6px;
  height: var(--jt-control-height);
  padding: 0 10px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  color: var(--jt-color-label-secondary);
}

.picker__field:focus-within {
  box-shadow: inset 0 0 0 2px var(--jt-color-primary);
}

.picker__field input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--jt-color-label);
  font: var(--jt-font-body);
}

.picker__chosen {
  color: var(--jt-color-label);
}

.picker__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.picker__clear {
  display: inline-flex;
  color: var(--jt-color-label-secondary);
}

.picker__list {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  z-index: 20;
  min-width: 320px;
  padding: 4px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-bg);
  box-shadow: var(--jt-shadow-popover);
}

.picker__option {
  display: grid;
  grid-template-columns: 72px 1fr auto;
  gap: 8px;
  padding: 6px 8px;
  border-radius: 8px;
  cursor: pointer;
}

.picker__option.is-active {
  background: var(--jt-color-selected);
}
</style>
