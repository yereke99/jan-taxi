<!-- Before/after viewer for audit entries: changed, added and removed paths highlighted. -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { jsonDiff } from '../utils/jsonDiff'
import ToggleSwitch from './ToggleSwitch.vue'

const props = defineProps<{ before: unknown; after: unknown }>()
const { t } = useI18n()
const showSame = ref(false)

const rows = computed(() => jsonDiff(props.before, props.after))
const visible = computed(() => (showSame.value ? rows.value : rows.value.filter((r) => r.kind !== 'same')))
const sameCount = computed(() => rows.value.filter((r) => r.kind === 'same').length)
</script>

<template>
  <div class="diff">
    <div class="diff__bar">
      <span class="jt-muted">{{ t('system.audit.diff_summary', { n: rows.length - sameCount }) }}</span>
      <ToggleSwitch v-if="sameCount > 0" v-model="showSame" :label="t('system.audit.show_unchanged', { n: sameCount })" />
    </div>
    <p v-if="rows.length === 0" class="jt-muted">{{ t('system.audit.no_payload') }}</p>
    <table v-else class="diff__table">
      <thead>
        <tr>
          <th scope="col">{{ t('system.audit.field') }}</th>
          <th scope="col">{{ t('system.audit.before') }}</th>
          <th scope="col">{{ t('system.audit.after') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in visible" :key="r.path" :class="`is-${r.kind}`">
          <td class="jt-mono diff__path">{{ r.path }}</td>
          <td class="jt-mono diff__val diff__val--before">{{ r.before ?? '' }}</td>
          <td class="jt-mono diff__val diff__val--after">{{ r.after ?? '' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.diff {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.diff__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--jt-space-3);
  font: var(--jt-font-small);
}

.diff__table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: 12px;
}

.diff__table th {
  padding: 6px 8px;
  text-align: left;
  font: 600 11px/16px var(--jt-font-family);
  text-transform: uppercase;
  color: var(--jt-color-label-secondary);
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.diff__table td {
  padding: 6px 8px;
  vertical-align: top;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.diff__path {
  width: 30%;
  color: var(--jt-color-label-secondary);
}

.is-changed .diff__val--before,
.is-removed .diff__val--before {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.is-changed .diff__val--after,
.is-added .diff__val--after {
  background: var(--jt-color-green-tint);
  color: var(--jt-color-green-text);
}

.is-same td {
  color: var(--jt-color-label-secondary);
}
</style>
