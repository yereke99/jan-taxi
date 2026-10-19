<!-- One order in a list: category, pickup (with its settlement) → destination, date, vehicle class, fare and status. -->
<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrderSummary } from '../../api/types'
import { orderLocality } from '../../domain/orderView'
import { formatDateTime, formatMoney, type Locale } from '../../utils/format'
import { orderStatusTone } from '../../utils/status'
import CategoryIcon from '../CategoryIcon.vue'
import { JBadge } from '../ui'
import { useVehicleText } from '../../composables/useVehicleText'
import { useAddressText } from '../../i18n/core'

const props = withDefaults(
  defineProps<{
    order: OrderSummary
    locale: Locale
    /** The pickup's locality line seen while booking (orders placed in this session). */
    pickupSubtitle?: string | null
  }>(),
  { pickupSubtitle: null },
)
const emit = defineEmits<{ open: [order: OrderSummary] }>()
const { t } = useI18n()
const addr = useAddressText()
const vehicleText = useVehicleText()
const locality = computed(() => orderLocality(props.order, props.pickupSubtitle))
</script>

<template>
  <button type="button" class="summary" @click="emit('open', order)">
    <span class="summary__icon"><CategoryIcon :category="order.category" /></span>
    <span class="summary__main">
      <span class="summary__route">
        <span class="summary__address">{{ addr(order.pickup.address) }}</span>
        <span v-if="locality" class="summary__place">{{ locality }}</span>
        <span class="summary__arrow" aria-hidden="true">↓</span>
        <span class="summary__address">{{ addr(order.destination.address) }}</span>
      </span>
      <span class="summary__meta">
        {{ formatDateTime(order.created_at, locale) }} · {{ vehicleText.className(order.vehicle_class, order.vehicle_class_name) }}<template v-if="order.stop_count > 2">
          · {{ t('orders.stops_more', { n: order.stop_count - 2 }) }}</template
        >
      </span>
      <span class="summary__badge"><JBadge :tone="orderStatusTone(order.status)" :label="t(`order_status.${order.status}`)" /></span>
    </span>
    <span class="summary__fare jt-tabular">{{ formatMoney(order.fare, locale) }}</span>
    <ChevronRight :size="18" class="summary__chevron" aria-hidden="true" />
  </button>
</template>

<style scoped>
.summary {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  width: 100%;
  padding: var(--jt-space-3) var(--jt-space-3) var(--jt-space-3) var(--jt-space-4);
  background: var(--jt-color-bg);
  text-align: left;
}

.summary + .summary::before {
  content: '';
  position: absolute;
  top: 0;
  left: 64px;
  right: 0;
  height: var(--jt-hairline);
  background: var(--jt-color-separator);
}

.summary:active {
  background: var(--jt-color-fill-tertiary);
}

.summary__icon {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.summary__main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.summary__route {
  display: flex;
  flex-direction: column;
}

.summary__address {
  font: var(--jt-font-subheadline);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary__place {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary__arrow {
  font: var(--jt-font-caption);
  color: var(--jt-color-label-tertiary);
  line-height: 12px;
}

.summary__meta {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.summary__badge {
  display: flex;
}

.summary__fare {
  flex: none;
  align-self: flex-start;
  font: var(--jt-font-headline);
}

.summary__chevron {
  flex: none;
  align-self: center;
  color: var(--jt-color-label-tertiary);
}
</style>
