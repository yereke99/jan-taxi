<!-- Passenger cancellations in the period by the reason they chose (§22.3), as monochrome bars. -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CancelReasonCounts } from '../../api/system'
import { Card } from '../../components'
import { useFormat } from '../../composables/useFormat'

const props = defineProps<{ counts: CancelReasonCounts | null | undefined }>()

const { t } = useI18n()
const fmt = useFormat()

const ORDER = ['long_wait', 'driver_issue', 'vehicle_issue', 'changed_plans', 'other', 'unspecified'] as const

const rows = computed(() => {
  const c = props.counts
  if (!c) return []
  const total = ORDER.reduce((s, k) => s + (c[k] ?? 0), 0)
  const max = Math.max(1, ...ORDER.map((k) => c[k] ?? 0))
  return ORDER.filter((k) => k !== 'unspecified' || (c[k] ?? 0) > 0).map((k) => {
    const n = c[k] ?? 0
    return { key: k, label: t(`cancel_reason.${k}`), n, share: total ? Math.round((n / total) * 100) : 0, width: `${(n / max) * 100}%` }
  })
})

const total = computed(() => rows.value.reduce((s, r) => s + r.n, 0))
</script>

<template>
  <Card :title="t('dashboard.cancel_reasons')" :subtitle="t('dashboard.cancel_reasons_hint', { n: fmt.count(total) })">
    <p v-if="total === 0" class="jt-muted">{{ t('dashboard.cancel_reasons_none') }}</p>
    <ul v-else class="reasons">
      <li v-for="r in rows" :key="r.key" class="reasons__row">
        <span class="reasons__label">{{ r.label }}</span>
        <span class="reasons__bar" aria-hidden="true"><span class="reasons__fill" :style="{ width: r.width }" /></span>
        <span class="reasons__n jt-num">{{ fmt.count(r.n) }}</span>
        <span class="reasons__share jt-num jt-muted">{{ r.share }}%</span>
      </li>
    </ul>
  </Card>
</template>

<style scoped>
.reasons {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.reasons__row {
  display: grid;
  grid-template-columns: minmax(140px, 220px) minmax(0, 1fr) 56px 44px;
  align-items: center;
  gap: var(--jt-space-3);
}

.reasons__bar {
  height: 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.reasons__fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--jt-color-primary);
}

.reasons__n,
.reasons__share {
  text-align: right;
}

.reasons__share {
  font: var(--jt-font-small);
}
</style>
