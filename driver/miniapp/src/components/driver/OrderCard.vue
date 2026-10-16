<!--
  Available order card: category, A → final destination (+N stops), the
  fare, trip distance/duration, distance to the pickup, comment preview and
  how long ago it was placed. No passenger data (the API withholds it).
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderCard } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { sortedStops, stopLetter } from '../../features/trip/sequence'
import CategoryIcon from './CategoryIcon.vue'

const props = defineProps<{ order: DriverOrderCard; now: number }>()
const emit = defineEmits<{ open: [id: string] }>()

const { t } = useI18n()
const fmt = useFormat()

const stops = computed(() => sortedStops(props.order.stops))
const pickup = computed(() => stops.value[0] ?? null)
const destination = computed(() => (stops.value.length > 1 ? stops.value[stops.value.length - 1]! : null))
const extraStops = computed(() => Math.max(0, props.order.stop_count - 2))
const preview = computed(() => props.order.cargo_description || props.order.comment || '')
const ago = computed(() => {
  // `now` re-evaluates the relative time every tick of the list's clock.
  void props.now
  return fmt.relative(props.order.created_at)
})
</script>

<template>
  <button type="button" class="ocard" @click="emit('open', order.id)">
    <div class="ocard__top">
      <span class="ocard__cat">
        <CategoryIcon :category="order.category" :size="18" />
        {{ t(`category.${order.category}`) }}
      </span>
      <span class="ocard__ago">{{ ago }}</span>
    </div>

    <div class="ocard__body">
      <ol class="ocard__route">
        <li v-if="pickup" class="ocard__stop">
          <span class="ocard__letter">A</span>
          <span class="ocard__addr">{{ pickup.address }}</span>
        </li>
        <li v-if="destination" class="ocard__stop">
          <span class="ocard__letter">{{ stopLetter(destination.seq) }}</span>
          <span class="ocard__addr">{{ destination.address }}</span>
        </li>
        <li v-if="extraStops > 0" class="ocard__more">{{ t('orders.more_stops', { n: extraStops }) }}</li>
      </ol>
      <div class="ocard__fare">
        <span class="ocard__amount jt-tabular">{{ fmt.money(order.fare) }}</span>
        <span class="ocard__pay">{{ t(`payment_method.${order.payment_method}`) }}</span>
      </div>
    </div>

    <p class="ocard__meta jt-tabular">
      <span>{{ fmt.distance(order.distance_m) }} · {{ fmt.duration(order.duration_s) }}</span>
      <span v-if="order.distance_to_pickup_m !== null" class="ocard__pickup">
        {{ t('orders.to_pickup', { distance: fmt.distance(order.distance_to_pickup_m) }) }}
      </span>
    </p>

    <p v-if="preview" class="ocard__comment">{{ preview }}</p>
  </button>
</template>

<style scoped>
.ocard {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
  width: calc(100% - 2 * var(--jt-side-margin));
  margin: 0 var(--jt-side-margin) var(--jt-space-3);
  padding: var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-bg);
  text-align: left;
  transition: transform var(--jt-duration-fast) var(--jt-ease);
}

.ocard:active {
  transform: scale(0.985);
  background: var(--jt-color-fill-secondary);
}

.ocard__top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--jt-space-2);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.ocard__cat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: var(--jt-color-label);
}

.ocard__body {
  display: flex;
  gap: var(--jt-space-3);
}

.ocard__route {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.ocard__stop {
  display: flex;
  align-items: flex-start;
  gap: var(--jt-space-2);
}

.ocard__letter {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: var(--jt-font-caption);
  font-weight: 700;
}

.ocard__addr {
  font: var(--jt-font-body);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.ocard__more {
  padding-left: 30px;
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.ocard__fare {
  flex: none;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.ocard__amount {
  font: var(--jt-font-title2);
}

.ocard__pay {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.ocard__meta {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--jt-space-1) var(--jt-space-3);
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.ocard__pickup {
  color: var(--jt-color-label);
  font-weight: 600;
}

.ocard__comment {
  padding: var(--jt-space-2) var(--jt-space-3);
  border-radius: 12px;
  background: var(--jt-color-fill-secondary);
  font: var(--jt-font-subheadline);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
</style>
