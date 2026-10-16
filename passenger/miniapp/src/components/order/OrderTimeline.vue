<!-- Status history of an order (order_status_events), oldest first. -->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { OrderEvent } from '../../api/types'
import { formatDateTime, type Locale } from '../../utils/format'

defineProps<{ events: OrderEvent[]; locale: Locale }>()
const { t } = useI18n()
</script>

<template>
  <ol class="timeline">
    <li v-for="(e, i) in events" :key="`${e.created_at}-${i}`" class="timeline__item">
      <span class="timeline__dot" :class="{ 'is-last': i === events.length - 1 }" aria-hidden="true" />
      <span class="timeline__text">
        <span class="timeline__status">{{ e.from_status === null ? t('detail.created') : t(`order_status.${e.to_status}`) }}</span>
        <span class="timeline__meta">
          {{ formatDateTime(e.created_at, locale) }} · {{ t(`detail.actor_${e.actor_type}`) }}
        </span>
        <span v-if="e.reason && e.reason !== 'no_driver_found'" class="timeline__reason">{{ e.reason }}</span>
        <span v-else-if="e.reason === 'no_driver_found'" class="timeline__reason">{{ t('order.reason_no_driver_found') }}</span>
      </span>
    </li>
  </ol>
</template>

<style scoped>
.timeline {
  display: flex;
  flex-direction: column;
}

.timeline__item {
  position: relative;
  display: flex;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-4);
}

.timeline__item:not(:last-child)::before {
  content: '';
  position: absolute;
  left: 5px;
  top: 14px;
  bottom: 0;
  width: 2px;
  background: var(--jt-color-fill-tertiary);
}

.timeline__dot {
  flex: none;
  width: 12px;
  height: 12px;
  margin-top: 4px;
  border-radius: 50%;
  background: var(--jt-color-fill-tertiary);
}

.timeline__dot.is-last {
  background: var(--jt-color-primary);
}

.timeline__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.timeline__status {
  font: var(--jt-font-callout);
}

.timeline__meta {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.timeline__reason {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}
</style>
