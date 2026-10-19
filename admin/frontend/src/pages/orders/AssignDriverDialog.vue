<!--
  Assign or reassign an order: search approved drivers by code, phone, plate
  or name (any city: dispatch is by radius around pickup A, not by city),
  nearest to the pickup first, pick one, confirm. The API keeps the approval,
  active-order and balance checks but skips the online, location and radius
  checks; it reports those as warnings (offline, location_required,
  location_stale, out_of_radius), which the dialog shows after assigning.
  The vehicle must fit the order's class, seats and cargo (422
  vehicle_mismatch). The new driver pays the commission at once; on a
  re-assignment the previous driver's commission is refunded only when ticked.
-->
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { driversApi, type AdminDriverListItem, type DriverLastLocation } from '../../api/drivers'
import { ordersApi, type AdminOrder } from '../../api/orders'
import { AppButton, AppSpinner, Modal, TextInput, ToggleSwitch } from '../../components'
import { useErrors } from '../../composables/useErrors'
import { useFormat } from '../../composables/useFormat'
import { useVehicleClasses } from '../../composables/useVehicleClasses'
import { isAbortError, isApiError } from '../../services/api'
import { distanceMeters } from '../../utils/geo'
import { chargedCommission } from './commission'

const props = defineProps<{ open: boolean; order: AdminOrder }>()
const emit = defineEmits<{ 'update:open': [open: boolean]; assigned: [order: AdminOrder]; stale: [] }>()

const { t, te } = useI18n()
const fmt = useFormat()
const errors = useErrors()
const vc = useVehicleClasses()
const q = ref('')
/** Refund the previous driver's commission on a re-assignment. */
const refundPrevious = ref(false)
const results = ref<AdminDriverListItem[]>([])
const searching = ref(false)
const selected = ref<AdminDriverListItem | null>(null)
const busy = ref(false)
const formError = ref('')
/** Warnings of a completed assignment; the dialog stays open to show them. */
const result = ref<{ code: string; warnings: string[] } | null>(null)
/** Last known locations of online or busy drivers, by driver id. */
const locations = ref(new Map<string, DriverLastLocation>())
let timer: ReturnType<typeof setTimeout> | undefined
let ctrl: AbortController | null = null
let locCtrl: AbortController | null = null

const reassign = ref(props.order.driver !== null)
const pickup = computed(() => props.order.stops.find((s) => s.kind === 'pickup') ?? props.order.stops[0] ?? null)

watch(
  () => props.open,
  (open) => {
    if (!open) return
    // Fixed while open: the order gains a driver once the assignment succeeds.
    reassign.value = props.order.driver !== null
    refundPrevious.value = false
    q.value = ''
    selected.value = null
    formError.value = ''
    result.value = null
    void loadLocations()
    void search()
  },
)

watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => void search(), 250)
})

async function loadLocations(): Promise<void> {
  locCtrl?.abort()
  const mine = new AbortController()
  locCtrl = mine
  try {
    const res = await driversApi.locations(undefined, mine.signal)
    if (locCtrl === mine) locations.value = new Map(res.items.map((i) => [i.driver.id, i.location]))
  } catch {
    // Distances are a hint only; the list works without them.
  }
}

async function search(): Promise<void> {
  ctrl?.abort()
  const mine = new AbortController()
  ctrl = mine
  searching.value = true
  try {
    const page = await driversApi.list({ status: 'approved', q: q.value, online: q.value ? '' : 'true' }, 1, 20, mine.signal)
    if (ctrl === mine) results.value = page.items.filter((d) => d.id !== props.order.driver?.id)
  } catch (err) {
    if (!isAbortError(err) && ctrl === mine) formError.value = errors.text(err)
  } finally {
    if (ctrl === mine) searching.value = false
  }
}

function distanceTo(d: AdminDriverListItem): number | null {
  const loc = locations.value.get(d.id)
  const p = pickup.value
  return loc && p ? distanceMeters(loc, p) : null
}

/** Nearest to the pickup first; drivers without a known location last. */
const rows = computed(() =>
  results.value
    .map((d) => ({ d, distance: distanceTo(d), location: locations.value.get(d.id) ?? null }))
    .sort((a, b) => (a.distance ?? Infinity) - (b.distance ?? Infinity)),
)

function lacksFunds(d: AdminDriverListItem): boolean {
  return d.wallet_available !== null && d.wallet_available < props.order.commission_amount
}

const selectedRow = computed(() => rows.value.find((r) => r.d.id === selected.value?.id) ?? null)

/** Commission the current driver paid, refundable on a re-assignment. */
const previousCharge = computed(() => chargedCommission(props.order.ledger, props.order.driver?.id))

/** What the order needs: class, and seats or cargo space. */
const need = computed(() => {
  const o = props.order
  const load =
    o.passenger_count !== null && o.passenger_count !== undefined
      ? t('classes.seats_n', { n: o.passenger_count })
      : o.cargo_volume_l > 0
        ? t('classes.liters', { n: fmt.count(o.cargo_volume_l) })
        : ''
  return [vc.name(o.vehicle_class, o.vehicle_class_name), load].filter(Boolean).join(' · ')
})

function vehicleText(d: AdminDriverListItem): string {
  const v = d.vehicle
  if (!v) return ''
  const cls = v.vehicle_class ? ` · ${vc.name(v.vehicle_class, v.vehicle_class_name)}${v.seats ? `, ${t('classes.seats_n', { n: v.seats })}` : ''}` : ''
  return `${v.make} ${v.model} · ${v.plate}${cls}`
}

function warningText(code: string): string {
  return te(`orders.warning_${code}`, 'kk') ? t(`orders.warning_${code}`) : code
}

async function assign(): Promise<void> {
  const d = selected.value
  if (!d || busy.value) return
  busy.value = true
  formError.value = ''
  try {
    const updated = await ordersApi.assign(props.order.id, d.id, reassign.value && refundPrevious.value && previousCharge.value > 0)
    emit('assigned', updated)
    const warnings = updated.warnings ?? []
    if (warnings.length > 0) result.value = { code: updated.driver?.code ?? d.code, warnings }
    else emit('update:open', false)
  } catch (err) {
    formError.value = errors.text(err)
    // A driver accepted (or the order moved on) meanwhile: show the current order.
    if (isApiError(err) && (err.code === 'order_already_taken' || err.code === 'invalid_transition')) emit('stale')
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(() => {
  clearTimeout(timer)
  ctrl?.abort()
  locCtrl?.abort()
})
</script>

<template>
  <Modal :open="open" :title="reassign ? t('orders.reassign_title') : t('orders.assign_title')" width="680px" :persistent="busy" @update:open="(v) => emit('update:open', v)">
    <div v-if="result" class="assign" role="alert">
      <p>{{ t('orders.assigned_with_warnings', { code: result.code }) }}</p>
      <ul class="assign__warnings">
        <li v-for="w in result.warnings" :key="w" class="assign__warn">{{ warningText(w) }}</li>
      </ul>
    </div>
    <div v-else class="assign">
      <p class="jt-muted">{{ t('orders.assign_hint', { commission: fmt.money(order.commission_amount) }) }}</p>
      <p class="assign__need">{{ t('orders.assign_need', { need }) }}</p>
      <TextInput v-model="q" type="search" :placeholder="t('drivers.search_placeholder')" :aria-label="t('common.search')" />
      <div class="assign__list" role="listbox" :aria-label="t('orders.drivers_found')" :aria-busy="searching">
        <AppSpinner v-if="searching && results.length === 0" :size="18" />
        <p v-else-if="results.length === 0" class="jt-muted assign__empty">{{ q ? t('orders.no_drivers_found') : t('orders.no_drivers_online') }}</p>
        <button
          v-for="r in rows"
          :key="r.d.id"
          type="button"
          role="option"
          class="assign__row"
          :class="{ 'is-selected': selected?.id === r.d.id }"
          :aria-selected="selected?.id === r.d.id"
          @click="selected = r.d"
        >
          <span class="jt-mono assign__code">{{ r.d.code }}</span>
          <span class="assign__name">
            {{ r.d.first_name }} {{ r.d.last_name }}
            <span class="jt-muted">{{ vehicleText(r.d) }}</span>
          </span>
          <span class="assign__where jt-muted">
            <template v-if="r.distance !== null && r.location">
              {{ t('orders.driver_location_age', { distance: fmt.distance(r.distance), age: fmt.relative(r.location.recorded_at) }) }}
            </template>
            <template v-else>{{ t('orders.to_pickup_unknown') }}</template>
          </span>
          <span class="assign__state" :class="r.d.is_online ? 'jt-positive' : 'jt-muted'">{{ r.d.is_online ? t('common.online') : t('common.offline') }}</span>
          <span class="jt-num assign__money" :class="{ 'jt-negative': lacksFunds(r.d) }">{{ fmt.money(r.d.wallet_available) }}</span>
        </button>
      </div>
      <p v-if="selected && !selected.is_online" class="assign__warn">{{ t('orders.assign_offline_warning') }}</p>
      <p v-else-if="selected && selectedRow && !selectedRow.location" class="assign__warn">{{ t('orders.assign_no_location') }}</p>
      <p v-if="selected && lacksFunds(selected)" class="assign__warn">{{ t('orders.assign_funds_warning') }}</p>
      <p class="jt-muted assign__note">{{ t('orders.assign_checks_note') }}</p>
      <div v-if="reassign" class="assign__refund">
        <p class="jt-muted">{{ t('orders.reassign_note', { code: order.driver?.code ?? '' }) }}</p>
        <ToggleSwitch v-model="refundPrevious" :disabled="previousCharge === 0">
          {{ t('orders.refund_commission_to', { code: order.driver?.code ?? '' }) }}
          <template v-if="previousCharge > 0"> ({{ fmt.money(previousCharge) }})</template>
        </ToggleSwitch>
      </div>
      <p v-if="formError" class="assign__error" role="alert">{{ formError }}</p>
    </div>
    <template #footer>
      <AppButton v-if="result" variant="primary" @click="emit('update:open', false)">{{ t('orders.done') }}</AppButton>
      <template v-else>
        <AppButton :disabled="busy" @click="emit('update:open', false)">{{ t('common.cancel') }}</AppButton>
        <AppButton variant="primary" :disabled="!selected" :loading="busy" @click="assign">
          {{ selected ? t('orders.assign_to', { code: selected.code }) : t('orders.assign') }}
        </AppButton>
      </template>
    </template>
  </Modal>
</template>

<style scoped>
.assign {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-3);
}

.assign__list {
  display: flex;
  flex-direction: column;
  max-height: 320px;
  min-height: 120px;
  overflow-y: auto;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
  padding: 4px;
}

.assign__empty {
  padding: var(--jt-space-4);
  text-align: center;
}

.assign__row {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr) 132px 72px 90px;
  align-items: center;
  gap: var(--jt-space-2);
  padding: 8px 10px;
  border-radius: 10px;
  text-align: left;
}

.assign__row:hover {
  background: var(--jt-color-hover);
}

.assign__row.is-selected {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.assign__row.is-selected .jt-muted,
.assign__row.is-selected .jt-positive,
.assign__row.is-selected .jt-negative {
  color: inherit;
  opacity: 0.8;
}

.assign__name {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.assign__where,
.assign__state {
  font: var(--jt-font-small);
}

.assign__money {
  text-align: right;
}

.assign__warnings {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.assign__warn {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
  font: var(--jt-font-small);
}

.assign__note {
  font: var(--jt-font-small);
}

.assign__need {
  font: var(--jt-font-small);
}

.assign__refund {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  box-shadow: inset 0 0 0 1px var(--jt-color-border);
}

.assign__error {
  padding: 8px 12px;
  border-radius: var(--jt-radius-control);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
  font: var(--jt-font-small);
}
</style>
