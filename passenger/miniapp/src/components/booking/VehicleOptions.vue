<!--
  Vehicle class options of a quote (ARCHITECTURE §21.5) as a compact iOS
  list: class icon, name, "up to 4 seats" (taxi) or "up to 400 l"
  (deliveries), the price with the nearest driver's ETA under it, and a black
  check on the chosen row. The chosen row shows the passenger's current fare, the
  others their recommended fare. A row is chosen only by a genuine tap
  (useTapGuard): the click that ends a scroll or a sheet drag over the list
  never changes the class.
-->
<script setup lang="ts">
import { Check } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { RouteQuoteOption } from '../../api/types'
import { useTapGuard } from '../../composables/useTapGuard'
import { useVehicleText } from '../../composables/useVehicleText'
import { etaMinutes } from '../../domain/vehicleClass'
import { formatMoney, type Locale } from '../../utils/format'
import VehicleClassIcon from '../VehicleClassIcon.vue'

const props = withDefaults(
  defineProps<{
    options: RouteQuoteOption[]
    /** vehicle_class of the chosen option. */
    selected: string | null
    /** Deliveries show cargo volume instead of seats. */
    delivery: boolean
    /** The passenger's fare for the chosen option. */
    fare: number | null
    locale: Locale
    disabled?: boolean
  }>(),
  { disabled: false },
)

const emit = defineEmits<{ select: [vehicleClass: string] }>()

const { t } = useI18n()
const text = useVehicleText()
const tap = useTapGuard()

function capacity(o: RouteQuoteOption): string {
  if (props.delivery) return o.cargo_volume_l > 0 ? text.volumeText(o.cargo_volume_l) : ''
  return o.seats_max > 0 ? text.seatsText(o.seats_max) : ''
}

function eta(o: RouteQuoteOption): string {
  const n = etaMinutes(o.eta_s)
  return n !== null ? t('confirm.eta', { n }) : ''
}

function price(o: RouteQuoteOption): number {
  return o.vehicle_class === props.selected && props.fare !== null ? props.fare : o.fare.recommended
}

function choose(e: MouseEvent, o: RouteQuoteOption): void {
  if (!tap.isTap(e)) return
  if (!props.disabled && o.vehicle_class !== props.selected) emit('select', o.vehicle_class)
}
</script>

<template>
  <div class="vclass" role="radiogroup" :aria-label="t('confirm.vehicle_class')" :aria-disabled="disabled || undefined">
    <button
      v-for="o in options"
      :key="o.vehicle_class"
      type="button"
      role="radio"
      class="vclass__row"
      :class="{ 'is-selected': o.vehicle_class === selected }"
      :aria-checked="o.vehicle_class === selected"
      :title="o.examples || undefined"
      :disabled="disabled"
      v-on="tap.handlers"
      @click="choose($event, o)"
    >
      <VehicleClassIcon :icon="o.icon" :size="44" class="vclass__icon" />
      <span class="vclass__text">
        <span class="vclass__name">{{ text.className(o.vehicle_class, o.name) }}</span>
        <span v-if="capacity(o)" class="vclass__caption">{{ capacity(o) }}</span>
      </span>
      <span class="vclass__end">
        <span class="vclass__price jt-tabular">{{ formatMoney(price(o), locale) }}</span>
        <span v-if="eta(o)" class="vclass__eta jt-tabular">{{ eta(o) }}</span>
      </span>
      <span class="vclass__check" aria-hidden="true">
        <Check v-if="o.vehicle_class === selected" :size="22" :stroke-width="2.5" />
      </span>
    </button>
  </div>
</template>

<style scoped>
.vclass {
  display: flex;
  flex-direction: column;
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
  overflow: hidden;
}

.vclass__row {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
  min-height: 60px;
  padding: var(--jt-space-2) var(--jt-space-3) var(--jt-space-2) var(--jt-space-4);
  text-align: left;
  color: var(--jt-color-label);
}

.vclass__row + .vclass__row::before {
  content: '';
  position: absolute;
  top: 0;
  left: 72px;
  right: 0;
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.vclass__row:active:not(:disabled) {
  background: var(--jt-color-fill-tertiary);
}

.vclass__row:disabled {
  cursor: default;
}

.vclass__icon {
  color: var(--jt-color-label);
}

.vclass__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.vclass__name {
  font: var(--jt-font-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.is-selected .vclass__name,
.is-selected .vclass__price {
  font-weight: 600;
}

.vclass__caption {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.vclass__end {
  flex: none;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
}

.vclass__price {
  font: var(--jt-font-body);
}

.vclass__eta {
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.vclass__check {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  color: var(--jt-color-label);
}
</style>
