<!--
  Read-only route of an order: letters in order, completed stops greyed while
  the trip runs, and the settlement of pickup A under its address when given.
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { OrderStop } from '../../api/types'
import { STOP_LETTERS } from '../../maps/constants'
import StopBadge from '../StopBadge.vue'
import { useAddressText } from '../../i18n/core'

withDefaults(
  defineProps<{
    stops: OrderStop[]
    showDone?: boolean
    /** Settlement of pickup A ("Алматы"), shown under its address. */
    pickupLocality?: string | null
  }>(),
  { showDone: false, pickupLocality: null },
)
const { t } = useI18n()
const addr = useAddressText()
</script>

<template>
  <ol class="order-stops">
    <li v-for="(s, i) in stops" :key="s.id" class="order-stops__row" :class="{ 'is-done': showDone && s.completed_at }">
      <StopBadge :letter="STOP_LETTERS[i] ?? ''" :done="showDone && s.completed_at !== null" size="small" />
      <span class="order-stops__text">
        <span class="order-stops__address">{{ addr(s.address) }}</span>
        <span v-if="s.address_details" class="order-stops__details">{{ s.address_details }}</span>
        <span v-if="i === 0 && pickupLocality" class="order-stops__details">{{ pickupLocality }}</span>
      </span>
      <span v-if="showDone && s.completed_at" class="order-stops__done">{{ t('order.stop_done') }}</span>
    </li>
  </ol>
</template>

<style scoped>
.order-stops {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.order-stops__row {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.order-stops__row.is-done .order-stops__address {
  color: var(--jt-color-label-tertiary);
}

.order-stops__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.order-stops__address {
  font: var(--jt-font-callout);
  overflow-wrap: anywhere;
}

.order-stops__details {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.order-stops__done {
  flex: none;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-tertiary);
}
</style>
