<!--
  Available order card: category with how long ago it was placed and the
  distance from the driver, the fare, then A → final destination with
  +N stops, trip distance and duration, and the comment or cargo preview.
  No passenger data (the API withholds it).
-->
<script setup lang="ts">
import { MessageSquareText, Package } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderCard } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { sortedStops } from '../../features/trip/sequence'
import CategoryIcon from './CategoryIcon.vue'
import { useAddressText } from '../../i18n/core'

const props = defineProps<{ order: DriverOrderCard; now: number }>()
const emit = defineEmits<{ open: [id: string] }>()

const { t } = useI18n()
const addr = useAddressText()
const fmt = useFormat()

const stops = computed(() => sortedStops(props.order.stops))
const pickup = computed(() => stops.value[0] ?? null)
const destination = computed(() => (stops.value.length > 1 ? stops.value[stops.value.length - 1]! : null))
const extraStops = computed(() => Math.max(0, props.order.stop_count - 2))
const preview = computed(() => props.order.cargo_description || props.order.comment || '')
const meta = computed(() => {
  // `now` re-evaluates the relative time every tick of the list's clock.
  void props.now
  const parts = [fmt.relative(props.order.created_at)]
  if (props.order.distance_to_pickup_m !== null) parts.push(t('orders.from_you', { distance: fmt.distance(props.order.distance_to_pickup_m) }))
  return parts.join(' · ')
})
const summary = computed(() => {
  const parts = [fmt.distance(props.order.distance_m), fmt.duration(props.order.duration_s)]
  if (extraStops.value > 0) parts.unshift(t('orders.more_stops', { n: extraStops.value }))
  return parts.join(' · ')
})
</script>

<template>
  <button type="button" class="ocard" @click="emit('open', order.id)">
    <div class="ocard__top">
      <span class="ocard__icon"><CategoryIcon :category="order.category" :size="22" /></span>
      <span class="ocard__head">
        <span class="ocard__cat">{{ t(`category.${order.category}`) }}</span>
        <span class="ocard__meta jt-tabular">{{ meta }}</span>
      </span>
      <span class="ocard__amount jt-tabular">{{ fmt.money(order.fare) }}</span>
    </div>

    <div class="ocard__body">
      <p class="ocard__route">
        <span>{{ addr(pickup?.address) }}</span>
        <template v-if="destination">
          <span class="ocard__arrow" aria-hidden="true"> → </span>
          <span>{{ addr(destination.address) }}</span>
        </template>
      </p>
      <p class="ocard__summary jt-tabular">{{ summary }}</p>
    </div>

    <p v-if="preview" class="ocard__comment">
      <Package v-if="order.cargo_description" :size="18" :stroke-width="1.8" aria-hidden="true" />
      <MessageSquareText v-else :size="18" :stroke-width="1.8" aria-hidden="true" />
      <span>{{ preview }}</span>
    </p>
  </button>
</template>

<style scoped>
.ocard {
  display: flex;
  flex-direction: column;
  width: calc(100% - 2 * var(--jt-side-margin));
  margin: 0 var(--jt-side-margin) var(--jt-space-3);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  text-align: left;
  transition: transform var(--jt-duration-spring) var(--jt-ease-spring);
}

.ocard:active {
  transform: scale(0.98);
}

.ocard__top {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  padding-bottom: var(--jt-space-3);
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.ocard__icon {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--jt-color-fill-secondary);
}

.ocard__head {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.ocard__cat {
  font: var(--jt-font-headline);
}

.ocard__meta {
  font: var(--jt-font-subheadline);
  font-size: 14px;
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ocard__amount {
  flex: none;
  font: 700 28px/34px var(--jt-font-family);
  letter-spacing: -0.4px;
}

.ocard__body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-top: var(--jt-space-3);
}

.ocard__route {
  font: var(--jt-font-headline);
  overflow-wrap: anywhere;
}

.ocard__summary {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.ocard__comment {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-3);
  padding: 10px var(--jt-space-3);
  border-radius: 14px;
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
}

.ocard__comment svg {
  flex: none;
  color: var(--jt-color-label-secondary);
}

.ocard__comment span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
