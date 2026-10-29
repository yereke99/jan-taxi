<!--
  All stops of an order in seq order: letter, address, the passenger's
  address details (entrance, floor…) once visible to the driver, and
  completion state on a live trip.
-->
<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Stop, StopSummary } from '../../api/types'
import { sortedStops, stopLetter } from '../../features/trip/sequence'
import { useAddressText } from '../../i18n/core'

const props = withDefaults(
  defineProps<{
    stops: readonly (Stop | StopSummary)[]
    /** Id of the stop the driver is heading to (highlighted). */
    currentId?: string | null
    live?: boolean
  }>(),
  { currentId: null, live: false },
)

const { t } = useI18n()
const addr = useAddressText()

const list = computed(() => sortedStops(props.stops))

function details(s: Stop | StopSummary): string | null {
  return 'address_details' in s ? s.address_details : null
}

function done(s: Stop | StopSummary): boolean {
  return props.live && 'completed_at' in s && s.completed_at !== null
}

function kindLabel(s: Stop | StopSummary): string {
  if (s.kind === 'pickup') return t('stops.pickup')
  if (s.kind === 'destination') return t('stops.destination')
  return t('stops.intermediate', { n: s.seq })
}
</script>

<template>
  <ol class="stops">
    <li
      v-for="s in list"
      :key="s.seq"
      class="stops__item"
      :class="{ 'is-done': done(s), 'is-current': 'id' in s && s.id === currentId }"
    >
      <span class="stops__marker">
        <Check v-if="done(s)" :size="14" :stroke-width="3" :aria-label="t('trip.stop_completed')" />
        <template v-else>{{ stopLetter(s.seq) }}</template>
      </span>
      <span class="stops__text">
        <span class="stops__kind">{{ kindLabel(s) }}</span>
        <span class="stops__addr">{{ s.mode === 'manual' ? s.address : addr(s.address) }}</span>
        <span v-if="s.mode === 'manual'" class="stops__details">{{ t('manual.label') }}</span>
        <span v-if="details(s)" class="stops__details">{{ details(s) }}</span>
      </span>
    </li>
  </ol>
</template>

<style scoped>
.stops {
  display: flex;
  flex-direction: column;
}

.stops__item {
  position: relative;
  display: flex;
  gap: var(--jt-space-3);
  padding: var(--jt-space-2) 0;
}

/* Vertical connector between markers. */
.stops__item:not(:last-child)::after {
  content: '';
  position: absolute;
  left: 13px;
  top: 38px;
  bottom: -6px;
  width: 2px;
  background: var(--jt-color-fill-tertiary);
}

.stops__marker {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: var(--jt-font-footnote);
  font-weight: 700;
}

.stops__item.is-done .stops__marker {
  background: var(--jt-color-gray);
}

.stops__item.is-current .stops__marker {
  box-shadow: 0 0 0 3px var(--jt-color-bg), 0 0 0 5px var(--jt-color-primary);
}

.stops__text {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
  padding-top: 2px;
}

.stops__kind {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.stops__addr {
  font: var(--jt-font-body);
  overflow-wrap: anywhere;
}

.stops__item.is-done .stops__addr {
  color: var(--jt-color-label-secondary);
}

.stops__details {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}
</style>
