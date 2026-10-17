<!--
  Top-up detail: the Kaspi receipt (parsed fields, decision, file checks and
  the PDF inline), what the driver reported, duplicate warnings, the recorded
  payment, who decided (an admin or the system), the history, and the Kaspi
  verification form with confirm / reject / refund (finance.manage).
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminRef } from '../../api/common'
import { REJECT_DUPLICATE_RECEIPT, topUpsApi, type AdminTopUpDetail, type DuplicateWarning } from '../../api/finance'
import { AppButton, Card, DetailHeader, KeyValueList, ReasonDialog, StateView, StatusBadge, Timeline, toast, type KeyValueItem, type TimelineItem } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { LIMITS } from '../../forms/validation'
import { useAuthStore } from '../../stores/auth'
import ReceiptCard from './ReceiptCard.vue'
import TopUpVerifyForm from './TopUpVerifyForm.vue'

const props = defineProps<{ id: string }>()

const { t, te } = useI18n()
const fmt = useFormat()
const auth = useAuthStore()
const res = useResource((signal) => topUpsApi.get(props.id, signal))
void res.reload()

const topup = computed(() => res.data.value)
const isOpen = computed(() => topup.value?.status === 'created' || topup.value?.status === 'pending_verification')
const canManage = computed(() => auth.can('finance.manage'))
const rejectOpen = ref(false)
const refundOpen = ref(false)
const duplicateReceipt = computed(() => topup.value?.rejected_reason === REJECT_DUPLICATE_RECEIPT)
/** Who credited a confirmed top-up: an admin, or the system from a receipt. */
const auto = computed(() => topup.value?.status === 'confirmed' || topup.value?.status === 'refunded' ? topup.value.confirmed_automatically : null)

function providerLabel(provider: string): string {
  const key = `finance.provider_${provider}`
  return te(key, 'kk') ? t(key) : provider
}

function adminName(a: AdminRef | null): string {
  return a ? a.full_name || a.email : t('finance.system_actor')
}

const request = computed<KeyValueItem[]>(() => {
  const x = topup.value
  if (!x) return []
  return [
    { key: 'reference', label: t('finance.reference'), value: x.reference, mono: true },
    { key: 'amount', label: t('finance.requested_amount'), value: fmt.money(x.amount) },
    { key: 'provider', label: t('finance.provider'), value: providerLabel(x.provider) },
    { key: 'payment_url', label: t('finance.payment_url'), value: x.payment_url, mono: true },
    { key: 'created', label: t('common.created'), value: fmt.dateTime(x.created_at) },
    { key: 'expires', label: t('finance.expires_at'), value: fmt.dateTime(x.expires_at) },
  ]
})

const report = computed<KeyValueItem[]>(() => {
  const x = topup.value
  if (!x) return []
  return [
    { key: 'ref', label: t('finance.driver_reference'), value: x.driver_payment_reference, mono: true },
    { key: 'note', label: t('finance.driver_note'), value: x.driver_note },
    { key: 'paid', label: t('finance.reported_paid'), value: fmt.dateTime(x.driver_reported_paid_at) },
  ]
})

const payment = computed<KeyValueItem[]>(() => {
  const p = topup.value?.payment_transaction
  if (!p) return []
  return [
    { key: 'txn', label: t('finance.transaction_id'), value: p.provider_transaction_id, mono: true },
    { key: 'amount', label: t('finance.verified_amount'), value: fmt.money(p.amount) },
    { key: 'paid', label: t('finance.paid_at'), value: fmt.dateTime(p.paid_at) },
    { key: 'payer', label: t('finance.payer_info'), value: p.payer_info },
    { key: 'provider', label: t('finance.provider'), value: providerLabel(p.provider) },
    { key: 'source', label: t('finance.recorded_via'), value: sourceLabel(p.source) },
    { key: 'by', label: t('finance.recorded_by'), value: adminName(p.recorded_by) },
    { key: 'at', label: t('common.created'), value: fmt.dateTime(p.created_at) },
  ]
})

function sourceLabel(source: string): string {
  if (source === 'webhook') return t('finance.source_webhook')
  if (source === 'receipt_auto') return t('finance.source_receipt_auto')
  return t('finance.source_manual')
}

const decisions = computed<KeyValueItem[]>(() => {
  const x = topup.value
  if (!x) return []
  const out: KeyValueItem[] = []
  if (x.confirmed_at) {
    const by = x.confirmed_automatically ? t('finance.auto_confirmed_by') : adminName(x.confirmed_by)
    out.push({ key: 'confirmed', label: t('finance.confirmed_by'), value: `${by} · ${fmt.dateTime(x.confirmed_at)}` })
  }
  if (x.rejected_at)
    out.push(
      { key: 'rejected', label: t('finance.rejected_by'), value: `${adminName(x.rejected_by)} · ${fmt.dateTime(x.rejected_at)}` },
      {
        key: 'rejected_reason',
        label: t('common.reason'),
        value: x.rejected_reason === REJECT_DUPLICATE_RECEIPT ? t('finance.reject_reason_duplicate_receipt') : x.rejected_reason,
      },
    )
  if (x.refunded_at)
    out.push(
      { key: 'refunded', label: t('finance.refunded_by'), value: `${adminName(x.refunded_by)} · ${fmt.dateTime(x.refunded_at)}` },
      { key: 'refund_reason', label: t('common.reason'), value: x.refund_reason },
    )
  return out
})

const history = computed<TimelineItem[]>(() =>
  [...(topup.value?.events ?? [])].reverse().map((e) => ({
    key: e.id,
    time: fmt.dateTime(e.created_at),
    ...eventTitle(e),
    meta: te(`actor.${e.actor_type}`, 'kk') ? t(`actor.${e.actor_type}`) : e.actor_type,
    tone: e.to_status === 'confirmed' ? 'success' : e.to_status === 'rejected' || e.to_status === 'refunded' ? 'danger' : 'neutral',
  })),
)

function warningText(w: DuplicateWarning): string {
  const status = w.status && te(`topup_status.${w.status}`, 'kk') ? t(`topup_status.${w.status}`) : (w.status ?? '—')
  if (w.kind === 'same_driver_payment_reference') return t('finance.dup_same_reference', { reference: w.reference ?? '—', status })
  if (w.kind === 'provider_transaction_recorded') return t('finance.dup_transaction', { txn: w.provider_transaction_id ?? '—', reference: w.reference ?? '—' })
  if (w.kind === 'receipt_serial_used') return t('finance.dup_receipt_serial', { txn: w.provider_transaction_id ?? '—', reference: w.reference ?? '—', status })
  return w.kind
}

function onConfirmed(updated: AdminTopUpDetail): void {
  res.set(updated)
  toast.success(t('finance.confirmed_ok', { amount: fmt.money(updated.verified_amount), code: updated.driver.code }))
}

function eventTitle(e: AdminTopUpDetail['events'][number]): { title: string; detail: string | null } {
  // System notes are machine codes (receipt_uploaded, expired …); free-text notes are shown as written.
  if (e.note && te(`finance.note_${e.note}`, 'kk')) return { title: t(`finance.note_${e.note}`), detail: null }
  return { title: t(`topup_status.${e.to_status}`), detail: e.note }
}

async function reject(reason: string): Promise<void> {
  res.set(await topUpsApi.reject(props.id, reason))
  toast.success(t('finance.rejected_ok'))
}

async function refund(reason: string): Promise<void> {
  res.set(await topUpsApi.refund(props.id, reason))
  toast.success(t('finance.refunded_ok'))
}

useRealtimeEvents(['topup.updated', 'topup.confirmed'], (ev) => {
  if (ev.data.topup_id === props.id) void res.reload({ silent: true })
})
useResync(() => void res.reload({ silent: true }))
</script>

<template>
  <section class="jt-page">
    <StateView v-if="!topup" :loading="res.loading.value" :error="res.error.value" @retry="res.reload()" />
    <template v-else>
      <DetailHeader :title="topup.reference" :back="{ name: 'topups' }" :back-label="t('nav.topups')">
        <template #badges>
          <StatusBadge kind="topup" :status="topup.status" />
          <span v-if="auto !== null" class="actor-badge" :class="{ 'actor-badge--auto': auto }">{{ auto ? t('finance.auto_badge') : t('finance.manual_badge') }}</span>
        </template>
        <template #subtitle>
          <RouterLink :to="{ name: 'driver-detail', params: { id: topup.driver.id } }" class="jt-mono">{{ topup.driver.code }}</RouterLink>
          · {{ topup.driver.first_name }} {{ topup.driver.last_name }}
          <template v-if="topup.driver.phone"> · <span class="jt-mono">{{ topup.driver.phone }}</span></template>
          · {{ fmt.money(topup.amount) }}
        </template>
        <template v-if="canManage" #actions>
          <AppButton v-if="isOpen" variant="destructive" @click="rejectOpen = true">{{ t('finance.reject_topup') }}</AppButton>
          <AppButton v-if="topup.status === 'confirmed'" variant="destructive" @click="refundOpen = true">{{ t('finance.refund_topup') }}</AppButton>
        </template>
      </DetailHeader>

      <div v-if="duplicateReceipt" class="dups" role="alert">
        <strong>{{ t('finance.duplicate_rejected_title') }}</strong>
        <p>{{ t('finance.duplicate_rejected') }}</p>
      </div>
      <div v-if="topup.duplicate_warnings.length" class="dups" role="alert">
        <strong>{{ t('finance.dup_title') }}</strong>
        <ul>
          <li v-for="(w, i) in topup.duplicate_warnings" :key="i">
            <RouterLink v-if="w.topup_id" :to="{ name: 'topup-detail', params: { id: w.topup_id } }">{{ warningText(w) }}</RouterLink>
            <span v-else>{{ warningText(w) }}</span>
          </li>
        </ul>
      </div>

      <div class="detail">
        <div class="detail__main">
          <ReceiptCard :topup="topup" />
          <Card :title="t('finance.request')">
            <KeyValueList :items="request" />
          </Card>
          <Card :title="t('finance.driver_report')" :subtitle="t('finance.driver_report_hint')">
            <KeyValueList :items="report" />
          </Card>
          <Card v-if="topup.payment_transaction" :title="t('finance.payment')">
            <KeyValueList :items="payment" />
          </Card>
          <Card v-if="decisions.length" :title="t('finance.decisions')">
            <KeyValueList :items="decisions" />
          </Card>
          <Card :title="t('finance.history')">
            <Timeline :items="history" :empty="t('table.empty')" />
          </Card>
        </div>
        <aside class="detail__side">
          <Card v-if="isOpen && canManage" :title="t('finance.verification')">
            <TopUpVerifyForm :topup="topup" @confirmed="onConfirmed" />
          </Card>
          <Card v-else-if="isOpen" :title="t('finance.verification')">
            <p class="jt-muted">{{ t('finance.no_manage_permission') }}</p>
          </Card>
          <Card v-else :title="t('finance.result')">
            <p class="result">
              <StatusBadge kind="topup" :status="topup.status" />
              <span v-if="topup.verified_amount !== null" class="jt-num result__amount">{{ fmt.money(topup.verified_amount) }}</span>
            </p>
          </Card>
        </aside>
      </div>

      <ReasonDialog
        v-model:open="rejectOpen"
        :title="t('finance.reject_title', { reference: topup.reference })"
        :message="t('finance.reject_message')"
        :max-length="LIMITS.topUpReason"
        :confirm-label="t('finance.reject_topup')"
        destructive
        :submit="reject"
      />
      <ReasonDialog
        v-model:open="refundOpen"
        :title="t('finance.refund_title', { reference: topup.reference })"
        :message="t('finance.refund_message', { amount: fmt.money(topup.verified_amount) })"
        :max-length="LIMITS.topUpReason"
        :confirm-label="t('finance.refund_topup')"
        destructive
        :submit="refund"
      />
    </template>
  </section>
</template>

<style scoped>
.detail {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 400px;
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

.detail__side {
  position: sticky;
  top: 0;
}

.actor-badge {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  border-radius: var(--jt-radius-round);
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
  font: 600 12px/16px var(--jt-font-family);
  white-space: nowrap;
}

.actor-badge--auto {
  background: var(--jt-color-primary);
  color: var(--jt-color-on-primary);
}

.dups {
  padding: 12px 16px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.dups + .dups {
  margin-top: calc(-1 * var(--jt-space-2));
}

.dups p {
  margin-top: 2px;
}

.dups ul {
  margin-top: 4px;
  padding-left: var(--jt-space-4);
  list-style: disc;
}

.dups a {
  color: inherit;
  text-decoration: underline;
}

.result {
  display: flex;
  align-items: center;
  gap: var(--jt-space-3);
}

.result__amount {
  font: var(--jt-font-title);
}
</style>
