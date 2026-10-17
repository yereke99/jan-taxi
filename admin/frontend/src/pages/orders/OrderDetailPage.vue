<!--
  Order detail for operators: route map, stops, parties, money, cargo,
  history, offers, wallet reservation/ledger and the dispute, with the
  operator actions (cancel, assign/reassign, open/resolve dispute).
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ordersApi, type AdminOrder, type Dispute } from '../../api/orders'
import { AppButton, Card, DataTable, DetailHeader, KeyValueList, ReasonDialog, StateView, StatusBadge, Timeline, toast, type Column, type KeyValueItem, type TimelineItem } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { LIMITS } from '../../forms/validation'
import { STOP_LETTERS } from '../../maps/constants'
import { useAuthStore } from '../../stores/auth'
import { isActiveOrderStatus } from '../../utils/status'
import AssignDriverDialog from './AssignDriverDialog.vue'
import OrderRouteMap from './OrderRouteMap.vue'
import ResolveDisputeDialog from './ResolveDisputeDialog.vue'

const props = defineProps<{ id: string }>()

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const res = useResource((signal) => ordersApi.get(props.id, signal))
void res.reload()

const order = computed(() => res.data.value)
const canManage = computed(() => auth.can('orders.manage'))
const cancelOpen = ref(false)
const assignOpen = ref(false)
const disputeOpen = ref(false)
const resolveOpen = ref(false)

const canCancel = computed(() => !!order.value && isActiveOrderStatus(order.value.status))
const canAssign = computed(() => !!order.value && ['searching', 'accepted', 'driver_arriving'].includes(order.value.status))
const canDispute = computed(
  () => !!order.value && ['in_progress', 'completed'].includes(order.value.status) && order.value.dispute?.status !== 'open',
)
const openDispute = computed(() => (order.value?.dispute?.status === 'open' ? order.value.dispute : null))

function name(p: { first_name: string; last_name: string } | null | undefined): string {
  return p ? `${p.first_name} ${p.last_name}`.trim() || '—' : '—'
}

function actorLabel(type: string | null | undefined): string {
  return type ? (te(`actor.${type}`, 'kk') ? t(`actor.${type}`) : type) : '—'
}

const fareItems = computed<KeyValueItem[]>(() => {
  const o = order.value
  if (!o) return []
  return [
    { key: 'fare', label: t('orders.fare'), value: fmt.money(o.fare) },
    { key: 'commission', label: t('orders.commission'), value: `${fmt.money(o.commission_amount)} · ${fmt.percentFromBps(o.commission_bps)}` },
    { key: 'payment', label: t('orders.payment_method'), value: t(`payment_method.${o.payment_method}`) },
    { key: 'distance', label: t('orders.distance'), value: fmt.distance(o.distance_m) },
    { key: 'duration', label: t('orders.duration'), value: fmt.duration(o.duration_s) },
    { key: 'category', label: t('orders.category'), value: t(`category.${o.category}`) },
  ]
})

const passengerItems = computed<KeyValueItem[]>(() => {
  const p = order.value?.passenger
  if (!p) return []
  return [
    { key: 'name', label: t('drivers.full_name'), value: name(p) },
    { key: 'phone', label: t('common.phone'), value: p.phone, mono: true },
    { key: 'tg', label: 'Telegram', value: p.username ? `@${p.username}` : String(p.telegram_user_id), mono: true },
    { key: 'blocked', label: t('orders.blocked'), value: p.is_blocked ? t('common.yes') : t('common.no') },
  ]
})

const cargoItems = computed<KeyValueItem[]>(() => {
  const o = order.value
  if (!o) return []
  const items: KeyValueItem[] = []
  if (o.service_type === 'delivery') {
    items.push(
      { key: 'cargo_type', label: t('orders.cargo_type'), value: t(`cargo_type.${o.cargo_type}`) },
      { key: 'cargo', label: t('orders.cargo_description'), value: o.cargo_description },
      { key: 'recipient', label: t('orders.recipient_phone'), value: o.recipient_phone, mono: true },
      { key: 'ack', label: t('orders.prohibited_ack'), value: o.prohibited_items_ack ? t('common.yes') : t('common.no') },
    )
  }
  items.push({ key: 'comment', label: t('common.comment'), value: o.comment })
  return items
})

const timeline = computed<TimelineItem[]>(() =>
  [...(order.value?.events ?? [])].reverse().map((e) => ({
    key: e.id,
    time: fmt.dateTime(e.created_at),
    title: t(`order_status.${e.to_status}`),
    detail: e.reason,
    meta: actorLabel(e.actor_type),
    tone: e.to_status === 'completed' ? 'success' : e.to_status === 'cancelled' || e.to_status === 'disputed' ? 'danger' : 'neutral',
  })),
)

type OfferRow = AdminOrder['offers'][number]
const offerColumns = computed<Column<OfferRow>[]>(() => [
  { key: 'driver', label: t('orders.driver'), value: (r) => `${r.driver.code} · ${name(r.driver)}` },
  { key: 'status', label: t('common.status'), format: (v) => (te(`offer_status.${String(v)}`, 'kk') ? t(`offer_status.${String(v)}`) : String(v)) },
  { key: 'distance_to_pickup_m', label: t('orders.to_pickup'), align: 'right', format: (v) => fmt.distance(v as number | null) },
  { key: 'sent_at', label: t('orders.sent_at'), format: (v) => fmt.dateTime(v as string) },
])

/** Driver codes known from the order (assigned driver and every offer) for ledger rows. */
const driverCodes = computed(() => {
  const m = new Map<string, string>()
  for (const o of order.value?.offers ?? []) m.set(o.driver.id, o.driver.code)
  if (order.value?.driver) m.set(order.value.driver.id, order.value.driver.code)
  return m
})

function stopLetter(seq: number): string {
  return STOP_LETTERS[seq] ?? String(seq + 1)
}

function apply(updated: AdminOrder): void {
  res.set(updated)
  for (const w of updated.warnings ?? []) toast.show(te(`orders.warning_${w}`, 'kk') ? t(`orders.warning_${w}`) : w)
}

async function cancel(reason: string): Promise<void> {
  apply(await ordersApi.cancel(props.id, reason))
  toast.success(t('orders.cancelled_ok'))
}

async function dispute(reason: string): Promise<void> {
  apply(await ordersApi.openDispute(props.id, reason))
  toast.success(t('orders.dispute_opened'))
}

function onAssigned(updated: AdminOrder): void {
  // Assignment warnings (offline, location, radius) are shown in the dialog.
  res.set(updated)
  toast.success(t('orders.assigned_ok', { code: updated.driver?.code ?? '' }))
}

function onResolved(_: Dispute): void {
  toast.success(t('orders.dispute_resolved'))
  void res.reload({ silent: true })
}

useRealtimeEvents(
  ['order.accepted', 'order.cancelled', 'order.driver_arriving', 'order.started', 'order.stop_completed', 'order.completed', 'order.disputed', 'order.taken', 'driver.location_updated'],
  (ev) => {
    if (ev.data.order_id === props.id) void res.reload({ silent: true })
  },
  800,
)
useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!order" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <DetailHeader :title="`${t('nav.order_detail')} #${order.number}`" :back="{ name: 'orders' }" :back-label="t('nav.orders')">
        <template #badges>
          <StatusBadge kind="order" :status="order.status" />
        </template>
        <template #subtitle>
          {{ t(`category.${order.category}`) }} · {{ fmt.name(order.city.name) }} · {{ fmt.dateTime(order.created_at) }}
          <span class="jt-mono jt-muted"> · {{ order.id }}</span>
        </template>
        <template v-if="canManage" #actions>
          <AppButton v-if="canAssign" @click="assignOpen = true">{{ order.driver ? t('orders.reassign') : t('orders.assign') }}</AppButton>
          <AppButton v-if="canDispute" @click="disputeOpen = true">{{ t('orders.open_dispute') }}</AppButton>
          <AppButton v-if="openDispute" variant="primary" @click="resolveOpen = true">{{ t('orders.resolve') }}</AppButton>
          <AppButton v-if="canCancel" variant="destructive" @click="cancelOpen = true">{{ t('orders.cancel_order') }}</AppButton>
        </template>
      </DetailHeader>

      <div class="detail">
        <div class="detail__main">
          <Card :title="t('orders.route')" flush>
            <OrderRouteMap :stops="order.stops" :route="order.route" :driver-location="order.driver_location" />
            <ol class="stops">
              <li v-for="s in order.stops" :key="s.id" class="stops__item">
                <span class="stops__letter">{{ stopLetter(s.seq) }}</span>
                <div class="stops__text">
                  <p>{{ s.address }}</p>
                  <p v-if="s.address_details" class="jt-muted">{{ s.address_details }}</p>
                  <p class="jt-muted stops__meta">
                    {{ t(`stop_kind.${s.kind}`) }} · <span class="jt-mono">{{ s.lat.toFixed(6) }}, {{ s.lon.toFixed(6) }}</span>
                    <template v-if="s.completed_at"> · {{ t('orders.stop_completed_at', { time: fmt.dateTime(s.completed_at) }) }}</template>
                  </p>
                </div>
              </li>
            </ol>
          </Card>

          <Card :title="t('orders.history')">
            <Timeline :items="timeline" :empty="t('table.empty')" />
          </Card>

          <Card :title="t('orders.offers')" :subtitle="t('orders.offers_count', { n: order.offers.length })" flush>
            <DataTable :columns="offerColumns" :rows="order.offers" :row-key="(r) => `${r.driver.id}-${r.sent_at}`" :empty-text="t('orders.no_offers')" />
          </Card>
        </div>

        <aside class="detail__side">
          <Card :title="t('orders.money')">
            <KeyValueList :items="fareItems" />
          </Card>

          <Card v-if="order.status === 'cancelled'" :title="t('orders.cancellation')">
            <KeyValueList
              :items="[
                { key: 'by', label: t('orders.cancelled_by'), value: actorLabel(order.cancelled_by) },
                { key: 'reason', label: t('common.reason'), value: order.cancel_reason },
                { key: 'at', label: t('orders.cancelled_at'), value: fmt.dateTime(order.cancelled_at) },
              ]"
            />
          </Card>

          <Card v-if="order.dispute" :title="t('orders.dispute')">
            <template #actions>
              <StatusBadge kind="dispute" :status="order.dispute.status" />
            </template>
            <KeyValueList
              :items="[
                { key: 'opened_by', label: t('orders.opened_by'), value: actorLabel(order.dispute.opened_by_type) },
                { key: 'reason', label: t('common.reason'), value: order.dispute.reason },
                { key: 'prev', label: t('orders.previous_status'), value: t(`order_status.${order.dispute.previous_status}`) },
                { key: 'opened', label: t('common.created'), value: fmt.dateTime(order.dispute.created_at) },
                ...(order.dispute.status === 'resolved'
                  ? [
                      { key: 'outcome', label: t('orders.outcome'), value: order.dispute.outcome ? t(`orders.outcome_${order.dispute.outcome}`) : '—' },
                      { key: 'resolution', label: t('orders.resolution'), value: order.dispute.resolution },
                      { key: 'resolved_by', label: t('orders.resolved_by'), value: order.dispute.resolved_by?.full_name ?? '—' },
                      { key: 'resolved_at', label: t('orders.resolved_at'), value: fmt.dateTime(order.dispute.resolved_at) },
                    ]
                  : []),
              ]"
            />
            <AppButton v-if="openDispute && canManage" class="side-action" variant="primary" block @click="resolveOpen = true">{{ t('orders.resolve') }}</AppButton>
          </Card>

          <Card :title="t('orders.passenger')">
            <KeyValueList :items="passengerItems" />
          </Card>

          <Card :title="t('orders.driver')">
            <template v-if="order.driver">
              <KeyValueList
                :items="[
                  { key: 'code', label: t('drivers.code'), value: order.driver.code, mono: true },
                  { key: 'name', label: t('drivers.full_name'), value: name(order.driver) },
                  { key: 'phone', label: t('common.phone'), value: order.driver.phone, mono: true },
                  { key: 'vehicle', label: t('drivers.vehicle'), value: order.vehicle ? `${order.vehicle.make} ${order.vehicle.model}, ${order.vehicle.color}` : null },
                  { key: 'plate', label: t('drivers.plate'), value: order.vehicle?.plate ?? null, mono: true },
                  { key: 'location', label: t('orders.last_location'), value: order.driver_location ? fmt.relative(order.driver_location.recorded_at) : null },
                ]"
              >
                <template #value-code>
                  <RouterLink :to="{ name: 'driver-detail', params: { id: order.driver.id } }" class="jt-mono">{{ order.driver.code }}</RouterLink>
                </template>
              </KeyValueList>
            </template>
            <p v-else class="jt-muted">{{ t('orders.no_driver') }}</p>
          </Card>

          <Card :title="t('orders.cargo_and_comment')">
            <KeyValueList :items="cargoItems" />
          </Card>

          <Card v-if="order.rating" :title="t('orders.rating')">
            <p class="rating"><strong>{{ order.rating.rating }}</strong> / 5</p>
            <p v-if="order.rating.comment">{{ order.rating.comment }}</p>
          </Card>

          <Card v-if="auth.can('finance.view') || order.reservation" :title="t('orders.wallet')" flush>
            <div class="reservation">
              <template v-if="order.reservation">
                <span>{{ t('orders.reservation') }}: <strong class="jt-num">{{ fmt.money(order.reservation.amount) }}</strong></span>
                <span class="jt-muted">{{ t(`reservation_status.${order.reservation.status}`) }}</span>
              </template>
              <span v-else class="jt-muted">{{ t('orders.no_reservation') }}</span>
            </div>
            <ul v-if="order.ledger.length" class="ledger">
              <li v-for="l in order.ledger" :key="l.id" class="ledger__row">
                <div class="ledger__what">
                  <span>{{ te(`ledger_type.${l.entry_type}`, 'kk') ? t(`ledger_type.${l.entry_type}`) : l.entry_type }}</span>
                  <span class="jt-muted">{{ driverCodes.get(l.driver_id) ?? '' }} · {{ fmt.dateTime(l.created_at) }}</span>
                </div>
                <div class="ledger__amounts">
                  <span v-if="l.balance_delta !== 0" class="jt-mono" :class="l.balance_delta > 0 ? 'jt-positive' : 'jt-negative'">{{ fmt.signedMoney(l.balance_delta) }}</span>
                  <span v-if="l.reserved_delta !== 0" class="jt-mono jt-muted">{{ t('finance.reserve_short') }} {{ fmt.signedMoney(l.reserved_delta) }}</span>
                </div>
              </li>
            </ul>
            <p v-else class="jt-muted ledger__empty">{{ t('orders.no_ledger') }}</p>
          </Card>
        </aside>
      </div>

      <ReasonDialog
        v-model:open="cancelOpen"
        :title="t('orders.cancel_title', { n: order.number })"
        :message="order.driver ? t('orders.cancel_message_driver') : t('orders.cancel_message')"
        :max-length="LIMITS.orderCancelReason"
        :confirm-label="t('orders.cancel_order')"
        destructive
        :submit="cancel"
      />
      <ReasonDialog
        v-model:open="disputeOpen"
        :title="t('orders.open_dispute_title', { n: order.number })"
        :message="t('orders.open_dispute_message')"
        :max-length="LIMITS.disputeReason"
        :confirm-label="t('orders.open_dispute')"
        :submit="dispute"
      />
      <AssignDriverDialog v-model:open="assignOpen" :order="order" @assigned="onAssigned" @stale="res.reload({ silent: true })" />
      <ResolveDisputeDialog v-model:open="resolveOpen" :dispute="openDispute" :commission="order.commission_amount" @resolved="onResolved" />
    </template>
  </section>
</template>

<style scoped>
.detail {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 380px;
  gap: var(--jt-space-4);
  align-items: start;
}

.detail__main,
.detail__side {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  min-width: 0;
}

.stops {
  display: flex;
  flex-direction: column;
  padding: var(--jt-space-2) var(--jt-space-5) var(--jt-space-4);
}

.stops__item {
  display: flex;
  gap: var(--jt-space-3);
  padding: 10px 0;
  border-bottom: var(--jt-hairline) solid var(--jt-color-separator);
}

.stops__item:last-child {
  border-bottom: 0;
}

.stops__letter {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
  font: 600 12px/1 var(--jt-font-family);
}

.stops__text {
  min-width: 0;
  overflow-wrap: anywhere;
}

.stops__meta {
  font: var(--jt-font-caption);
}

.reservation {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding: 0 var(--jt-space-5) var(--jt-space-3);
}

.ledger {
  display: flex;
  flex-direction: column;
  padding: 0 var(--jt-space-5) var(--jt-space-3);
}

.ledger__row {
  display: flex;
  justify-content: space-between;
  gap: var(--jt-space-3);
  padding: 8px 0;
  border-top: var(--jt-hairline) solid var(--jt-color-separator);
}

.ledger__what,
.ledger__amounts {
  display: flex;
  flex-direction: column;
  line-height: 18px;
}

.ledger__amounts {
  align-items: flex-end;
}

.ledger__what .jt-muted,
.ledger__amounts .jt-muted {
  font-size: 12px;
}

.ledger__empty {
  padding: 0 var(--jt-space-5) var(--jt-space-4);
}

.rating {
  font: var(--jt-font-title3);
}

.side-action {
  margin-top: var(--jt-space-3);
}
</style>
