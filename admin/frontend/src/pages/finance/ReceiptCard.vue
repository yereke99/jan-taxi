<!--
  Kaspi receipt of a top-up: the fields read from the PDF (№ чека, amount,
  seller and BIN, date, masked payer, producer), the automatic decision with
  its reasons, the file checks, and the file itself shown inline. Earlier
  receipts of the same top-up can be opened in the same viewer.
-->
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { INFO_RECEIPT_FLAGS, REJECT_DUPLICATE_RECEIPT, type AdminReceipt, type AdminTopUpDetail } from '../../api/finance'
import { Card, ImageViewer, KeyValueList, type KeyValueItem } from '../../components'
import { useFormat } from '../../composables/useFormat'
import ReceiptDocument from './ReceiptDocument.vue'

const props = defineProps<{ topup: AdminTopUpDetail }>()

const { t, te } = useI18n()
const fmt = useFormat()

const receipt = computed<AdminReceipt | null>(() => props.topup.receipt)
const earlier = computed(() => props.topup.receipts.filter((r) => r.id !== receipt.value?.id))
const duplicate = computed(() => props.topup.rejected_reason === REJECT_DUPLICATE_RECEIPT)

/** File in the viewer: the latest upload unless an earlier receipt was picked. */
const shownUrl = ref<string | null>(null)
watch(
  () => props.topup.id,
  () => (shownUrl.value = null),
)
const viewerUrl = computed(() => shownUrl.value ?? props.topup.receipt_url)
const shownReceipt = computed(() => props.topup.receipts.find((r) => r.file_url === shownUrl.value) ?? null)
const imageViewer = ref<number | null>(null)

const amountMismatch = computed(() => receipt.value?.amount != null && receipt.value.amount !== props.topup.amount)

const fields = computed<KeyValueItem[]>(() => {
  const r = receipt.value
  if (!r) return []
  return [
    { key: 'serial', label: t('finance.receipt_serial'), value: r.receipt_serial, mono: true },
    { key: 'amount', label: t('finance.receipt_amount'), value: fmt.money(r.amount) },
    { key: 'paid_at', label: t('finance.receipt_paid_at'), value: fmt.dateTime(r.paid_at) },
    { key: 'recipient', label: t('finance.receipt_recipient'), value: r.recipient_name },
    { key: 'bin', label: t('finance.receipt_bin'), value: r.recipient_bin, mono: true },
    { key: 'payer', label: t('finance.receipt_payer'), value: r.payer_masked },
    { key: 'method', label: t('finance.receipt_method'), value: r.payment_method },
    { key: 'producer', label: t('finance.receipt_producer'), value: r.producer, mono: true },
    { key: 'parse', label: t('finance.receipt_parse_status'), value: label('finance.parse_', r.parse_status) },
    { key: 'uploaded', label: t('finance.receipt_uploaded_at'), value: fmt.dateTime(r.created_at) },
    { key: 'sha', label: 'SHA-256', value: r.file_sha256, mono: true },
  ]
})

const reasons = computed(() => (receipt.value?.reasons ?? []).map((code) => ({ code, text: label('finance.receipt_reason.', code) })))
const flags = computed(() =>
  (receipt.value?.flags ?? [])
    .map((code) => ({ code, text: label('finance.receipt_flag.', code), info: INFO_RECEIPT_FLAGS.includes(code) }))
    .sort((a, b) => Number(a.info) - Number(b.info)),
)
const blockingFlags = computed(() => flags.value.filter((f) => !f.info).length)

function label(prefix: string, code: string): string {
  const key = `${prefix}${code}`
  return te(key, 'kk') ? t(key) : code
}

function fileName(r: AdminReceipt | null): string {
  return r?.receipt_serial ? `receipt_${r.receipt_serial}` : `receipt_${props.topup.reference}`
}
</script>

<template>
  <Card :title="t('finance.receipt_title')" :subtitle="receipt ? t('finance.receipt_subtitle') : ''">
    <template v-if="receipt" #actions>
      <span class="pill" :class="receipt.decision === 'auto_confirmed' ? 'pill--success' : 'pill--warning'">
        {{ label('finance.decision_', receipt.decision) }}
      </span>
    </template>

    <div class="receipt" :class="{ 'receipt--single': !receipt }">
      <div v-if="receipt" class="receipt__data">
        <KeyValueList :items="fields">
          <template #value-amount>
            <span class="jt-num">{{ fmt.money(receipt.amount) }}</span>
            <span v-if="amountMismatch" class="receipt__mismatch">{{ t('finance.receipt_amount_mismatch', { amount: fmt.money(topup.amount) }) }}</span>
          </template>
          <template #value-sha>
            <span class="receipt__hash" :title="receipt.file_sha256">{{ receipt.file_sha256.slice(0, 16) }}…</span>
          </template>
        </KeyValueList>

        <section v-if="reasons.length" class="receipt__block">
          <h3 class="receipt__title">{{ t('finance.receipt_reasons') }}</h3>
          <ul class="receipt__list">
            <li v-for="r in reasons" :key="r.code" class="receipt__item receipt__item--warning">{{ r.text }}</li>
          </ul>
        </section>

        <section class="receipt__block">
          <h3 class="receipt__title">{{ t('finance.receipt_flags') }}</h3>
          <p v-if="blockingFlags === 0" class="receipt__ok">{{ t('finance.flags_none') }}</p>
          <ul v-if="flags.length" class="receipt__list">
            <li v-for="f in flags" :key="f.code" class="receipt__item" :class="f.info ? 'receipt__item--info' : 'receipt__item--danger'">
              {{ f.text }}
              <span class="receipt__code jt-mono">{{ f.code }}</span>
              <span v-if="f.info" class="receipt__code">· {{ t('finance.flag_info') }}</span>
            </li>
          </ul>
        </section>

        <section v-if="earlier.length" class="receipt__block">
          <h3 class="receipt__title">{{ t('finance.earlier_receipts') }}</h3>
          <ul class="receipt__earlier">
            <li v-for="r in earlier" :key="r.id">
              <span class="jt-mono">{{ r.receipt_serial ?? '—' }}</span>
              <span class="jt-muted">{{ fmt.money(r.amount) }} · {{ fmt.dateTime(r.created_at) }} · {{ label('finance.decision_', r.decision) }}</span>
              <button type="button" class="jt-link-button" :aria-pressed="shownUrl === r.file_url" @click="shownUrl = shownUrl === r.file_url ? null : r.file_url">
                {{ t('finance.show_file') }}
              </button>
            </li>
          </ul>
        </section>
      </div>

      <div class="receipt__file">
        <p v-if="!receipt && !duplicate && topup.has_receipt" class="receipt__note">{{ t('finance.image_receipt_note') }}</p>
        <ReceiptDocument
          v-if="topup.has_receipt || shownUrl"
          :url="viewerUrl"
          :label="t('finance.receipt_file')"
          :filename="fileName(shownReceipt ?? receipt)"
          @open-image="imageViewer = 0"
        />
        <p v-else class="jt-muted">{{ t('finance.no_receipt') }}</p>
      </div>
    </div>
    <ImageViewer v-model:index="imageViewer" :images="viewerUrl ? [{ url: viewerUrl, label: t('finance.receipt_file') }] : []" />
  </Card>
</template>

<style scoped>
.receipt {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--jt-space-5);
  align-items: start;
}

.receipt--single {
  grid-template-columns: minmax(0, 1fr);
}

@media (max-width: 1280px) {
  .receipt {
    grid-template-columns: minmax(0, 1fr);
  }
}

.receipt__data,
.receipt__file {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-4);
  min-width: 0;
}

.receipt__block {
  display: flex;
  flex-direction: column;
  gap: var(--jt-space-2);
}

.receipt__title {
  font: var(--jt-font-body-strong);
}

.receipt__list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.receipt__item {
  padding: 6px 10px;
  border-radius: var(--jt-radius-control);
  font: var(--jt-font-small);
}

.receipt__item--warning {
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
}

.receipt__item--danger {
  background: var(--jt-color-red-tint);
  color: var(--jt-color-red-text);
}

.receipt__item--info {
  background: var(--jt-color-fill-secondary);
  color: var(--jt-color-label-secondary);
}

.receipt__code {
  margin-left: 4px;
  opacity: 0.75;
  font-size: 11px;
}

.receipt__ok {
  font: var(--jt-font-small);
  color: var(--jt-color-green-text);
}

.receipt__mismatch {
  display: block;
  font: var(--jt-font-small);
  color: var(--jt-color-red-text);
}

.receipt__hash {
  font-size: 12px;
}

.receipt__note {
  padding: 10px 12px;
  border-radius: var(--jt-radius-inner);
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
  font: var(--jt-font-small);
}

.receipt__earlier {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.receipt__earlier li {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px var(--jt-space-2);
  font: var(--jt-font-small);
}

.pill {
  display: inline-flex;
  align-items: center;
  height: 24px;
  padding: 0 10px;
  border-radius: var(--jt-radius-round);
  font: 600 12px/16px var(--jt-font-family);
  white-space: nowrap;
}

.pill--success {
  background: var(--jt-color-green-tint);
  color: var(--jt-color-green-text);
}

.pill--warning {
  background: var(--jt-color-orange-tint);
  color: var(--jt-color-orange-text);
}
</style>
