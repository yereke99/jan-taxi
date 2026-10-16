<!--
  Top-up detail: what the driver reported (reference, note, receipt image),
  duplicate warnings, the recorded payment, the history, and the Kaspi
  verification form with confirm / reject / refund (finance.manage).
-->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { topUpsApi, type AdminTopUpDetail } from '../../api/finance'
import { AppButton, Card, DetailHeader, ImageViewer, KeyValueList, ReasonDialog, SecureImage, StateView, StatusBadge, Timeline, toast, type KeyValueItem, type TimelineItem } from '../../components'
import { useFormat } from '../../composables/useFormat'
import { useRealtimeEvents, useResync } from '../../composables/useRealtime'
import { useResource } from '../../composables/useResource'
import { LIMITS } from '../../forms/validation'
import { useAuthStore } from '../../stores/auth'
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
const viewer = ref<number | null>(null)

const request = computed<KeyValueItem[]>(() => {
  const x = topup.value
  if (!x) return []
  return [
    { key: 'reference', label: t('finance.reference'), value: x.reference, mono: true },
    { key: 'amount', label: t('finance.requested_amount'), value: fmt.money(x.amount) },
    { key: 'provider', label: t('finance.provider'), value: x.provider === 'kaspi_manual' ? t('finance.provider_kaspi_manual') : x.provider },
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
    { key: 'source', label: t('finance.recorded_via'), value: p.source === 'webhook' ? t('finance.source_webhook') : t('finance.source_manual') },
    { key: 'by', label: t('finance.recorded_by'), value: p.recorded_by ? p.recorded_by.full_name || p.recorded_by.email : null },
    { key: 'at', label: t('common.created'), value: fmt.dateTime(p.created_at) },
  ]
})

const decisions = computed<KeyValueItem[]>(() => {
  const x = topup.value
  if (!x) return []
  const out: KeyValueItem[] = []
  if (x.confirmed_at) out.push({ key: 'confirmed', label: t('finance.confirmed_by'), value: `${x.confirmed_by?.full_name ?? '—'} · ${fmt.dateTime(x.confirmed_at)}` })
  if (x.rejected_at)
    out.push(
      { key: 'rejected', label: t('finance.rejected_by'), value: `${x.rejected_by?.full_name ?? '—'} · ${fmt.dateTime(x.rejected_at)}` },
      { key: 'rejected_reason', label: t('common.reason'), value: x.rejected_reason },
    )
  if (x.refunded_at)
    out.push(
      { key: 'refunded', label: t('finance.refunded_by'), value: `${x.refunded_by?.full_name ?? '—'} · ${fmt.dateTime(x.refunded_at)}` },
      { key: 'refund_reason', label: t('common.reason'), value: x.refund_reason },
    )
  return out
})

const history = computed<TimelineItem[]>(() =>
  [...(topup.value?.events ?? [])].reverse().map((e) => ({
    key: e.id,
    time: fmt.dateTime(e.created_at),
    // System notes are machine codes (receipt_uploaded, expired …); free-text notes are shown as written.
    title: e.note && te(`finance.note_${e.note}`, 'kk') ? t(`finance.note_${e.note}`) : t(`topup_status.${e.to_status}`),
    detail: e.note && te(`finance.note_${e.note}`, 'kk') ? null : e.note,
    meta: te(`actor.${e.actor_type}`, 'kk') ? t(`actor.${e.actor_type}`) : e.actor_type,
    tone: e.to_status === 'confirmed' ? 'success' : e.to_status === 'rejected' || e.to_status === 'refunded' ? 'danger' : 'neutral',
  })),
)

function warningText(w: AdminTopUpDetail['duplicate_warnings'][number]): string {
  if (w.kind === 'same_driver_payment_reference') return t('finance.dup_same_reference', { reference: w.reference ?? '—', status: w.status ? t(`topup_status.${w.status}`) : '—' })
  if (w.kind === 'provider_transaction_recorded') return t('finance.dup_transaction', { txn: w.provider_transaction_id ?? '—', reference: w.reference ?? '—' })
  return w.kind
}

function onConfirmed(updated: AdminTopUpDetail): void {
  res.set(updated)
  toast.success(t('finance.confirmed_ok', { amount: fmt.money(updated.verified_amount), code: updated.driver.code }))
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
          <Card :title="t('finance.request')">
            <KeyValueList :items="request" />
          </Card>
          <Card :title="t('finance.driver_report')" :subtitle="t('finance.driver_report_hint')">
            <div class="report">
              <KeyValueList :items="report" />
              <SecureImage
                v-if="topup.has_receipt"
                :url="topup.receipt_url"
                :label="t('finance.receipt')"
                height="280px"
                fit="contain"
                @open="viewer = 0"
              />
              <p v-else class="jt-muted">{{ t('finance.no_receipt') }}</p>
            </div>
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

      <ImageViewer v-model:index="viewer" :images="topup.receipt_url ? [{ url: topup.receipt_url, label: t('finance.receipt') }] : []" />
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

.report {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 240px;
  gap: var(--jt-space-4);
  align-items: start;
}

.dups {
  padding: 12px 16px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
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
