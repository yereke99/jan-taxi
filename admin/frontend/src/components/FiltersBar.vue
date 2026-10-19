<!--
  Toolbar above a list: optional debounced search (v-model:search), filter
  controls in the default slot, a reset button and trailing actions.
-->
<script setup lang="ts">
import { Search } from '@lucide/vue'
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppButton from './AppButton.vue'

const props = withDefaults(
  defineProps<{
    search?: string
    searchPlaceholder?: string
    showSearch?: boolean
    showReset?: boolean
    debounceMs?: number
    /** CSS width of the search field (a longer hint needs more room). */
    searchWidth?: string
  }>(),
  { search: '', searchPlaceholder: '', showSearch: true, showReset: true, debounceMs: 300, searchWidth: '280px' },
)

const emit = defineEmits<{ 'update:search': [value: string]; reset: [] }>()

const { t } = useI18n()
const local = ref(props.search)
let timer: ReturnType<typeof setTimeout> | undefined

watch(
  () => props.search,
  (v) => {
    if (v !== local.value) local.value = v
  },
)

function onInput(e: Event): void {
  local.value = (e.target as HTMLInputElement).value
  clearTimeout(timer)
  timer = setTimeout(() => emit('update:search', local.value.trim()), props.debounceMs)
}

function reset(): void {
  clearTimeout(timer)
  local.value = ''
  emit('reset')
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div class="filters" role="search">
    <label v-if="showSearch" class="filters__search" :style="{ width: searchWidth }">
      <Search :size="15" aria-hidden="true" />
      <span class="jt-visually-hidden">{{ t('common.search') }}</span>
      <input type="search" :value="local" :placeholder="searchPlaceholder || t('common.search')" :title="searchPlaceholder || undefined" @input="onInput" />
    </label>
    <slot />
    <AppButton v-if="showReset" variant="plain" size="sm" @click="reset">{{ t('common.reset') }}</AppButton>
    <div class="filters__actions"><slot name="actions" /></div>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--jt-space-2) var(--jt-space-3);
  padding-bottom: var(--jt-space-3);
}

.filters__search {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  width: 280px;
  height: var(--jt-control-height);
  padding: 0 12px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-bg);
  box-shadow: inset 0 0 0 var(--jt-hairline) var(--jt-color-border);
  color: var(--jt-color-label-secondary);
}

.filters__search input {
  flex: 1;
  min-width: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--jt-color-label);
  font: var(--jt-font-body);
}

.filters__actions {
  display: flex;
  gap: var(--jt-space-2);
  margin-left: auto;
}
</style>
