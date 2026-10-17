<!--
  Active trip (§5): map with the route and the current leg, one primary
  action per state — arrived → start → intermediate stops in seq order →
  complete — 2GIS hand-off, contacts, all stops, and cancelling before the
  start with a reason. When the order ends (here or elsewhere) its final
  state is summarised.
-->
<script setup lang="ts">
import { CircleCheck, CircleX } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import FareBreakdown from '../components/driver/FareBreakdown.vue'
import NavigationButtons from '../components/driver/NavigationButtons.vue'
import StopList from '../components/driver/StopList.vue'
import TripContacts from '../components/driver/TripContacts.vue'
import TripMap from '../components/driver/TripMap.vue'
import { JButton, JEmptyState, JSheet, JSkeleton, showActionSheet, toast, type SheetSnap } from '../components/ui'
import { useFormat } from '../composables/useFormat'
import { usePolling } from '../composables/usePolling'
import { cancelReasons, actionLabelKey } from '../features/trip/labels'
import { canDriverCancel, currentTarget, primaryAction, remainingStops, stopLetter } from '../features/trip/sequence'
import { useDriverErrorText } from '../composables/useDriverErrorText'
import { ROUTE } from '../router/names'
import { isApiError } from '../services/http'
import { useLocationStore } from '../stores/location'
import { useTripStore } from '../stores/trip'
import { haptics } from '../telegram'
import { useAddressText } from '../i18n/core'

const { t } = useI18n()
const addr = useAddressText()
const fmt = useFormat()
const errorText = useDriverErrorText()
const router = useRouter()
const trip = useTripStore()
const location = useLocationStore()

const snap = ref<SheetSnap>('half')
const sheetHeight = ref(0)
const cancelling = ref(false)

const order = computed(() => trip.active)
const finished = computed(() => (trip.active ? null : trip.finished))
const action = computed(() => (order.value ? primaryAction(order.value) : null))
const target = computed(() => (order.value ? currentTarget(order.value) : null))
const remaining = computed(() => (order.value ? remainingStops(order.value) : []))
const canCancel = computed(() => (order.value ? canDriverCancel(order.value) : false))

const heading = computed(() => {
  const o = order.value
  const s = target.value
  if (!o) return ''
  if (o.status === 'accepted') return t('trip.heading_pickup')
  if (o.status === 'driver_arriving') return t('trip.waiting_pickup')
  if (s && s.kind === 'destination') return t('trip.heading_destination')
  return s ? t('trip.heading_stop', { letter: stopLetter(s.seq) }) : ''
})

const actionLabel = computed(() => {
  const a = action.value
  if (!a) return ''
  return a.kind === 'complete_stop' ? t(actionLabelKey(a), { letter: a.letter }) : t(actionLabelKey(a))
})

async function perform(): Promise<void> {
  const a = action.value
  if (!a || trip.busy) return
  try {
    await trip.perform(a)
    haptics.notification('success')
  } catch (err) {
    haptics.notification('error')
    toast.error(errorText(err))
    // The order moved on elsewhere (passenger cancelled, admin): show the real state.
    if (isApiError(err) && (err.code === 'invalid_transition' || err.status === 404)) void trip.refresh()
  }
}

async function cancel(): Promise<void> {
  const o = order.value
  if (!o || cancelling.value) return
  const reasons = cancelReasons(o.category, o.status)
  const choice = await showActionSheet({
    title: t('trip.cancel_title'),
    message: t('trip.cancel_message'),
    actions: reasons.map((r) => ({ id: r, label: t(`trip.cancel_reasons.${r}`), destructive: true })),
  })
  if (!choice) return
  cancelling.value = true
  try {
    await trip.cancel(t(`trip.cancel_reasons.${choice}`))
    toast.show(t('trip.cancelled_toast'))
  } catch (err) {
    toast.error(errorText(err))
    void trip.refresh()
  } finally {
    cancelling.value = false
  }
}

function done(): void {
  trip.clearFinished()
  void router.replace({ name: ROUTE.home })
}

usePolling(() => trip.refresh(), 15_000)

onMounted(() => {
  void trip.refresh()
  if (!location.current) void location.locateOnce().catch(() => undefined)
})

onBeforeUnmount(() => {
  if (!trip.active) trip.clearFinished()
})
</script>

<template>
  <div class="tpage">
    <TripMap
      v-if="order"
      :order="order"
      :position="location.current"
      :sheet-height="sheetHeight"
      live
      @locate="location.locateOnce().catch(() => undefined)"
    />

    <template v-if="order">
      <JSheet
        v-model:snap="snap"
        :snap-points="['peek', 'half', 'full']"
        :peek-height="200"
        :aria-label="t('trip.title')"
        @height="(h) => (sheetHeight = h)"
      >
        <template #header>
          <div class="tpage__head">
            <p class="tpage__heading">{{ heading }}</p>
            <p v-if="target" class="tpage__target">{{ addr(target.address) }}</p>
            <p v-if="target?.address_details" class="tpage__details">{{ target.address_details }}</p>
            <p class="tpage__meta jt-tabular">
              {{ t('trip.order_number', { number: order.number }) }} · {{ fmt.money(order.fare) }} · {{ t(`payment_method.${order.payment_method}`) }}
            </p>
          </div>
        </template>

        <NavigationButtons :remaining="remaining" :position="location.current" />

        <TripContacts class="tpage__group" :order="order" />

        <section class="tpage__card">
          <h2 class="tpage__section">{{ t('trip.stops_title') }}</h2>
          <StopList :stops="order.stops" :current-id="target?.id ?? null" live />
        </section>

        <section v-if="order.cargo_description || order.comment" class="tpage__card">
          <template v-if="order.cargo_description">
            <h2 class="tpage__section">{{ t('order.cargo') }}</h2>
            <p class="tpage__text">{{ order.cargo_description }}</p>
          </template>
          <template v-if="order.comment">
            <h2 class="tpage__section">{{ t('order.comment') }}</h2>
            <p class="tpage__text">{{ order.comment }}</p>
          </template>
        </section>

        <FareBreakdown
          class="tpage__group"
          :fare="order.fare"
          :commission="order.commission_amount"
          :commission-bps="order.commission_bps"
          :payment-method="order.payment_method"
        />

        <JButton v-if="canCancel" variant="destructive" block :loading="cancelling" @click="cancel">{{ t('trip.cancel') }}</JButton>

        <template #footer>
          <JButton v-if="action" block :loading="trip.busy" @click="perform">{{ actionLabel }}</JButton>
        </template>
      </JSheet>
    </template>

    <div v-else-if="finished" class="tpage__done jt-grouped">
      <component
        :is="finished.status === 'completed' ? CircleCheck : CircleX"
        :size="56"
        :stroke-width="1.5"
        class="tpage__done-icon"
        :class="{ 'is-ok': finished.status === 'completed' }"
        aria-hidden="true"
      />
      <template v-if="finished.status === 'completed'">
        <h1 class="tpage__done-title">{{ t('trip.completed_title') }}</h1>
        <p class="tpage__done-text">
          {{ t('trip.completed_text', { fare: fmt.money(finished.fare), payment: t(`payment.${finished.payment_method}`) }) }}
        </p>
        <div class="tpage__done-fare">
          <FareBreakdown :fare="finished.fare" :commission="finished.commission_amount" :commission-bps="finished.commission_bps" />
        </div>
      </template>
      <template v-else>
        <h1 class="tpage__done-title">{{ t('trip.cancelled_title') }}</h1>
        <p class="tpage__done-text">{{ finished.cancelled_by ? t(`trip.cancelled_by.${finished.cancelled_by}`) : '' }}</p>
        <p v-if="finished.cancel_reason" class="tpage__done-text">{{ t('trip.reason', { reason: finished.cancel_reason }) }}</p>
      </template>
      <div class="tpage__done-actions">
        <JButton block @click="done">{{ t('trip.done') }}</JButton>
      </div>
    </div>

    <div v-else-if="!trip.loaded" class="tpage__state" aria-busy="true">
      <JSkeleton height="200px" radius="22px" />
    </div>

    <div v-else class="tpage__state">
      <JEmptyState :title="t('trip.none_title')" :description="t('trip.none_text')">
        <template #action>
          <JButton block @click="router.replace({ name: ROUTE.orders })">{{ t('home.menu_orders') }}</JButton>
        </template>
      </JEmptyState>
    </div>
  </div>
</template>

<style scoped>
.tpage {
  position: absolute;
  inset: 0;
  overflow: hidden;
  background: var(--jt-color-grouped-bg);
}

.tpage__head {
  padding: var(--jt-space-1) 0 var(--jt-space-3);
}

/* The sheet heading ("Heading to pickup"); .tpage__state is the empty/loading page. */
.tpage__heading {
  font: var(--jt-font-subheadline);
  font-weight: 600;
  color: var(--jt-color-label-secondary);
}

.tpage__target {
  margin-top: 2px;
  font: var(--jt-font-title3);
  overflow-wrap: anywhere;
}

.tpage__details {
  font: var(--jt-font-subheadline);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.tpage__meta {
  margin-top: var(--jt-space-1);
  font: var(--jt-font-footnote);
  color: var(--jt-color-label-secondary);
}

.tpage__group {
  margin: 0 0 var(--jt-space-4);
}

.tpage__group :deep(.jt-list-group__body) {
  background: var(--jt-color-fill-secondary);
}

.tpage__group :deep(.jt-row) {
  background: transparent;
}

.tpage__card {
  margin-bottom: var(--jt-space-4);
  padding: var(--jt-space-3) var(--jt-space-4);
  border-radius: var(--jt-radius-card);
  background: var(--jt-color-fill-secondary);
}

.tpage__section {
  margin: var(--jt-space-1) 0;
  font: var(--jt-font-headline);
}

.tpage__text {
  margin-bottom: var(--jt-space-2);
  font: var(--jt-font-body);
  overflow-wrap: anywhere;
}

.tpage__done {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--jt-space-2);
  padding: calc(var(--jt-safe-top) + var(--jt-space-8)) 0 calc(max(var(--jt-safe-bottom), var(--jt-float-inset)) + var(--jt-space-4));
  overflow-y: auto;
  background: var(--jt-color-grouped-bg);
  text-align: center;
}

.tpage__done-icon {
  color: var(--jt-color-label-secondary);
}

.tpage__done-icon.is-ok {
  color: var(--jt-color-green);
}

.tpage__done-title {
  font: var(--jt-font-title1);
}

.tpage__done-text {
  padding: 0 calc(var(--jt-side-margin) + var(--jt-space-2));
  font: var(--jt-font-body);
  color: var(--jt-color-label-secondary);
  overflow-wrap: anywhere;
}

.tpage__done-fare {
  align-self: stretch;
  margin-top: var(--jt-space-4);
  text-align: left;
}

.tpage__done-actions {
  align-self: stretch;
  margin-top: auto;
  padding: var(--jt-space-4) var(--jt-side-margin) 0;
}

.tpage__state {
  padding: calc(var(--jt-safe-top) + var(--jt-space-8)) var(--jt-side-margin);
}
</style>
