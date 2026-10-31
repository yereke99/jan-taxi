<!--
  Available order card: category with how long ago it was placed and the
  distance from the driver, the fare, then the settlement of pickup A and
  A → final destination with +N stops, trip distance and duration, the
  requested vehicle class with the passenger count or cargo size (§21), the
  comment or cargo preview, and a full-width «Тапсырысты алу» button that
  takes the order in one tap (the same atomic acceptance as the order screen
  and the driver bot). The rest of the card opens the order. No passenger
  data (the API withholds it). The card is an <article> holding two sibling
  buttons (the body keeps its earlier markup), so a tap on «Тапсырысты алу»
  never opens the order.
-->
<script setup lang="ts">
import { MapPin, MessageSquareText, Package } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DriverOrderCard } from '../../api/types'
import { useFormat } from '../../composables/useFormat'
import { sortedStops } from '../../features/trip/sequence'
import { findClass } from '../../features/vehicle/classes'
import { useVehicleText } from '../../features/vehicle/useVehicleText'
import { useSessionStore } from '../../stores/session'
import { JSpinner } from '../ui'
import CategoryIcon from './CategoryIcon.vue'
import VehicleClassIcon from './VehicleClassIcon.vue'
import { useAddressText } from '../../i18n/core'

const props = withDefaults(
  defineProps<{
    order: DriverOrderCard
    now: number
    /** This order is being accepted right now. */
    accepting?: boolean
    /** Another acceptance is running: the button waits. */
    disabled?: boolean
  }>(),
  { accepting: false, disabled: false },
)
const emit = defineEmits<{ open: [id: string]; accept: [id: string] }>()

const { t } = useI18n()
const addr = useAddressText()
const fmt = useFormat()
const vt = useVehicleText()
const session = useSessionStore()

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
const need = computed(() => {
  const o = props.order
  const parts = [o.vehicle_class ? vt.className(o.vehicle_class, o.vehicle_class_name) : '', vt.load(o)].filter(Boolean)
  return parts.join(' · ')
})
const needIcon = computed(() => findClass(session.extras.vehicleClasses, props.order.vehicle_class)?.icon ?? null)
// Unknown metrics (a route with a manually typed stop) are left out instead
// of being shown as dashes; the line disappears when nothing is known.
const summary = computed(() => {
  const o = props.order
  const parts: string[] = []
  if (extraStops.value > 0) parts.push(t('orders.more_stops', { n: extraStops.value }))
  if (o.distance_m !== null) parts.push(fmt.distance(o.distance_m))
  if (o.duration_s !== null) parts.push(fmt.duration(o.duration_s))
  return parts.join(' · ')
})

function accept(): void {
  if (!props.accepting && !props.disabled) emit('accept', props.order.id)
}
</script>

<template>
  <article class="ocard">
    <button type="button" class="ocard__open" @click="emit('open', order.id)">
      <div class="ocard__top">
        <span class="ocard__icon"><CategoryIcon :category="order.category" :size="22" /></span>
        <span class="ocard__head">
          <span class="ocard__cat">{{ t(`category.${order.category}`) }}</span>
          <span class="ocard__meta jt-tabular">{{ meta }}</span>
        </span>
        <span class="ocard__amount jt-tabular">{{ fmt.money(order.fare) }}</span>
      </div>

      <div class="ocard__body">
        <p v-if="order.locality" class="ocard__locality">
          <MapPin :size="15" :stroke-width="2.2" aria-hidden="true" />
          <span class="jt-visually-hidden">{{ t('order.locality') }}:</span>
          <span class="ocard__locality-name">{{ order.locality }}</span>
        </p>
        <p class="ocard__route">
          <span>{{ addr(pickup?.address) }}</span>
          <template v-if="destination">
            <span class="ocard__arrow" aria-hidden="true"> → </span>
            <span>{{ addr(destination.address) }}</span>
          </template>
        </p>
        <p v-if="summary" class="ocard__summary jt-tabular">{{ summary }}</p>
        <p v-if="need" class="ocard__need">
          <VehicleClassIcon v-if="needIcon" :icon="needIcon" :width="30" />
          <span>{{ need }}</span>
        </p>
      </div>

      <p v-if="preview" class="ocard__comment">
        <Package v-if="order.cargo_description" :size="18" :stroke-width="1.8" aria-hidden="true" />
        <MessageSquareText v-else :size="18" :stroke-width="1.8" aria-hidden="true" />
        <span>{{ preview }}</span>
      </p>
    </button>

    <button
      type="button"
      class="ocard__accept"
      :disabled="disabled && !accepting"
      :aria-busy="accepting || undefined"
      :aria-label="`${t('orders.accept')} · ${fmt.money(order.fare)}`"
      @click="accept"
    >
      <JSpinner v-if="accepting" color="light" />
      <span v-else>{{ t('orders.accept') }}</span>
    </button>
  </article>
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

/* Pressing the card body shrinks the card as before; «Тапсырысты алу» only dims itself. */
.ocard:has(.ocard__open:active) {
  transform: scale(0.98);
}

.ocard__open {
  display: flex;
  flex-direction: column;
  width: 100%;
  color: inherit;
  text-align: left;
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

.ocard__locality {
  display: flex;
  align-items: center;
  gap: 4px;
  font: var(--jt-font-subheadline);
  font-weight: 600;
  color: var(--jt-color-label-secondary);
}

.ocard__locality svg {
  flex: none;
  color: var(--jt-color-label);
}

.ocard__locality-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ocard__route {
  font: var(--jt-font-headline);
  overflow-wrap: anywhere;
}

.ocard__summary {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
}

.ocard__need {
  display: flex;
  align-items: center;
  gap: var(--jt-space-2);
  margin-top: var(--jt-space-1);
  font: var(--jt-font-subheadline);
  font-weight: 600;
}

.ocard__need span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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

/* Green means "go" (§15: colour only for meaning), as in the customer's reference. */
.ocard__accept {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: var(--jt-button-height-medium);
  margin-top: var(--jt-space-3);
  border-radius: var(--jt-radius-button);
  background: var(--jt-color-green);
  color: #fff;
  font: var(--jt-font-headline);
  white-space: nowrap;
  transition: opacity 120ms linear;
}

.ocard__accept:active {
  opacity: var(--jt-opacity-pressed);
}

.ocard__accept:disabled {
  opacity: 0.3;
}
</style>
